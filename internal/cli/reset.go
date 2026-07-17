package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/auth"
	"portx/internal/cloudflare"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/rpc"
	"portx/internal/state"
	"portx/internal/ui"
)

func resetCommand() *cli.Command {
	return &cli.Command{
		Name:  "reset",
		Usage: "Delete PortX-owned Cloudflare resources and local config for a profile",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "yes", Usage: "skip confirmation"},
			&cli.BoolFlag{Name: "keep-local", Usage: "do not delete local config/state/secrets"},
			&cli.BoolFlag{Name: "remote-only", Usage: "only delete remote resources"},
		},
		Action: runReset,
	}
}

func runReset(ctx context.Context, cmd *cli.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profile := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)
	prof, err := cfg.Profile(profile)
	if err != nil {
		return err
	}

	if !cmd.Bool("yes") {
		desc := fmt.Sprintf("Delete PortX resources for profile %q", profile)
		if prof.TunnelName != "" {
			desc = fmt.Sprintf("Delete tunnel %s and DNS %s (profile %q)", prof.TunnelName, prof.Wildcard, profile)
		}
		ok, err := ui.Confirm(desc+"?", false)
		if err != nil || !ok {
			return apperr.New(apperr.ExitInvalidArgs, "aborted")
		}
	}

	store, err := credentials.Open()
	if err != nil {
		return err
	}
	if err := coordinateRuntime(true); err != nil {
		return fmt.Errorf("stop active PortX runtime: %w", err)
	}
	st, err := state.Open()
	if err != nil {
		return fmt.Errorf("open state before remote cleanup: %w", err)
	}
	data := st.Data()
	ps := data.Profiles[profile]
	needsRemoteCleanup := prof.IsConfigured() || ps.TunnelID != "" || ps.WildcardDNS != nil
	if needsRemoteCleanup {
		apiToken, err := store.Get(credentials.APITokenKey(profile))
		if err != nil {
			return fmt.Errorf("read API token for remote cleanup: %w", err)
		}
		remoteErr := deleteRemoteResources(ctx, cloudflare.New(apiToken), prof, ps)
		if remoteErr != nil {
			return fmt.Errorf("remote cleanup incomplete; local recovery data was retained: %w", remoteErr)
		}
	}

	if cmd.Bool("remote-only") {
		return nil
	}
	if !cmd.Bool("keep-local") {
		var localErr error
		if err := deleteProfileSecrets(store, profile); err != nil {
			localErr = errors.Join(localErr, err)
		}
		if err := auth.RemoveBrowserCredentials(); err != nil {
			localErr = errors.Join(localErr, fmt.Errorf("remove browser credentials: %w", err))
		}
		delete(cfg.Profiles, profile)
		if cfg.DefaultProfile == profile {
			cfg.DefaultProfile = "personal"
			if _, ok := cfg.Profiles[cfg.DefaultProfile]; !ok {
				profiles := make([]string, 0, len(cfg.Profiles))
				for name := range cfg.Profiles {
					profiles = append(profiles, name)
				}
				sort.Strings(profiles)
				if len(profiles) > 0 {
					cfg.DefaultProfile = profiles[0]
				}
			}
		}
		if err := config.Save(cfg); err != nil {
			localErr = errors.Join(localErr, fmt.Errorf("save config: %w", err))
		}
		st, err := state.Open()
		if err != nil {
			localErr = errors.Join(localErr, fmt.Errorf("open state: %w", err))
		} else {
			data := st.Data()
			delete(data.Profiles, profile)
			if err := st.Replace(data); err != nil {
				localErr = errors.Join(localErr, fmt.Errorf("save state: %w", err))
			}
		}
		if localErr != nil {
			return fmt.Errorf("clear local profile %q: %w", profile, localErr)
		}
		ui.Success("cleared local profile config and secrets")
	}
	ui.Success("reset complete")
	return nil
}

func coordinateRuntime(releaseLeases bool) error {
	runtimeDir, err := config.RuntimeDir()
	if err != nil {
		return err
	}
	socketPath := filepath.Join(runtimeDir, "portxd.sock")
	pidPath := filepath.Join(runtimeDir, "portxd.pid")
	client, dialErr := rpc.Dial(socketPath)
	daemonConnected := dialErr == nil
	if daemonConnected {
		leases, err := client.ListLeases()
		if err != nil {
			_ = client.Close()
			return fmt.Errorf("list active routes: %w", err)
		}
		if len(leases) > 0 && !releaseLeases {
			_ = client.Close()
			return fmt.Errorf("%d active route(s); stop them before setup", len(leases))
		}
		if releaseLeases {
			for _, lease := range leases {
				if err := client.ForceRelease(lease.ID); err != nil {
					_ = client.Close()
					return fmt.Errorf("release active route %q: %w", lease.ID, err)
				}
			}
		}
		if err := client.Close(); err != nil {
			return fmt.Errorf("close daemon connection: %w", err)
		}
	}

	pidInfo, pidErr := os.Lstat(pidPath)
	if pidErr != nil && !os.IsNotExist(pidErr) {
		return fmt.Errorf("inspect daemon pid file: %w", pidErr)
	}
	if daemonConnected && os.IsNotExist(pidErr) {
		return errors.New("daemon socket is active but daemon pid file is missing")
	}
	if pidErr == nil && !pidInfo.Mode().IsRegular() {
		return fmt.Errorf("daemon pid path is not a regular file: %q", pidPath)
	}
	if !daemonConnected {
		socketInfo, socketErr := os.Lstat(socketPath)
		if socketErr != nil && !os.IsNotExist(socketErr) {
			return fmt.Errorf("inspect daemon socket: %w", socketErr)
		}
		if socketErr == nil && socketInfo.Mode()&os.ModeSocket != 0 && pidErr != nil {
			return fmt.Errorf("daemon socket is present but unavailable: %w", dialErr)
		}
	}
	if pidErr == nil {
		if err := stopDaemon(pidPath); err != nil {
			return fmt.Errorf("stop daemon: %w", err)
		}
		if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove daemon pid file: %w", err)
		}
	}

	cloudflaredPID := filepath.Join(runtimeDir, "cloudflared.pid")
	if _, err := os.Lstat(cloudflaredPID); err == nil {
		if err := stopProcessFile(cloudflaredPID, "cloudflared", stopOptions{
			interruptTimeout: 5 * time.Second,
			killTimeout:      5 * time.Second,
			processName:      "cloudflared",
		}); err != nil {
			return fmt.Errorf("stop cloudflared: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect cloudflared pid file: %w", err)
	}
	return nil
}

func deleteRemoteResources(
	ctx context.Context,
	cf *cloudflare.Client,
	prof config.Profile,
	ps state.ProfileState,
) error {
	if prof.IsConfigured() && ps.WildcardDNS == nil {
		return errors.New("local state is missing the wildcard DNS recovery record")
	}
	var remoteErr error
	canDeleteDNS := ps.WildcardDNS != nil &&
		ps.WildcardDNS.OwnedByPortx &&
		ps.WildcardDNS.RecordID != "" &&
		prof.ZoneID != ""
	if canDeleteDNS {
		if err := cf.DeleteDNS(ctx, prof.ZoneID, ps.WildcardDNS.RecordID); err != nil &&
			!cloudflare.IsNotFound(err) {
			remoteErr = errors.Join(remoteErr, fmt.Errorf("delete DNS: %w", err))
		} else {
			ui.Success("deleted wildcard DNS")
		}
	}
	if prof.TunnelID != "" && prof.AccountID != "" {
		if err := deleteOwnedTunnel(ctx, cf, prof); err != nil {
			remoteErr = errors.Join(remoteErr, err)
		}
	}
	return remoteErr
}

func deleteProfileSecrets(store credentials.Store, profile string) error {
	var deleteErr error
	for _, key := range []string{
		credentials.APITokenKey(profile),
		credentials.TunnelTokenKey(profile),
	} {
		if err := store.Delete(key); err != nil {
			deleteErr = errors.Join(deleteErr, fmt.Errorf("delete credential %q: %w", key, err))
		}
	}
	return deleteErr
}

func deleteOwnedTunnel(ctx context.Context, cf *cloudflare.Client, prof config.Profile) error {
	t, err := cf.GetTunnel(ctx, prof.AccountID, prof.TunnelID)
	if err != nil {
		if cloudflare.IsNotFound(err) {
			ui.Info("tunnel already deleted")
			return nil
		}
		return fmt.Errorf("get tunnel: %w", err)
	}
	meta, _ := t.Metadata["owned_by"].(string)
	if meta != "portx" {
		return fmt.Errorf("tunnel %s is not marked owned_by=portx; refusing delete", prof.TunnelID)
	}
	if err := cf.DeleteTunnel(ctx, prof.AccountID, prof.TunnelID); err != nil &&
		!cloudflare.IsNotFound(err) {
		return fmt.Errorf("delete tunnel: %w", err)
	}
	ui.Success("deleted tunnel")
	return nil
}
