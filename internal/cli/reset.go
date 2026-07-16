package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/cloudflare"
	"portx/internal/config"
	"portx/internal/credentials"
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
	profile := cmd.String("profile")
	if profile == "" {
		profile = "personal"
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
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
	apiToken, err := store.Get(credentials.APITokenKey(profile))
	if err != nil {
		ui.Warn("no API token; skipping remote delete")
	} else {
		cf := cloudflare.New(apiToken)
		st, _ := state.Open()
		data := st.Data()
		ps := data.Profiles[profile]

		canDeleteDNS := ps.WildcardDNS != nil &&
			ps.WildcardDNS.OwnedByPortx &&
			ps.WildcardDNS.RecordID != "" &&
			prof.ZoneID != ""
		if canDeleteDNS {
			if err := cf.DeleteDNS(ctx, prof.ZoneID, ps.WildcardDNS.RecordID); err != nil {
				ui.Warn("delete DNS: %v", err)
			} else {
				ui.Success("deleted wildcard DNS")
			}
		}
		if prof.TunnelID != "" && prof.AccountID != "" {
			deleteOwnedTunnel(ctx, cf, prof)
		}
	}

	if cmd.Bool("remote-only") {
		return nil
	}
	if !cmd.Bool("keep-local") {
		_ = store.Delete(credentials.APITokenKey(profile))
		_ = store.Delete(credentials.TunnelTokenKey(profile))
		delete(cfg.Profiles, profile)
		if cfg.DefaultProfile == profile {
			cfg.DefaultProfile = "personal"
		}
		if err := config.Save(cfg); err != nil {
			return err
		}
		st, err := state.Open()
		if err == nil {
			data := st.Data()
			delete(data.Profiles, profile)
			_ = st.Replace(data)
		}
		ui.Success("cleared local profile config and secrets")
	}
	ui.Success("reset complete")
	return nil
}

func deleteOwnedTunnel(ctx context.Context, cf *cloudflare.Client, prof config.Profile) {
	t, err := cf.GetTunnel(ctx, prof.AccountID, prof.TunnelID)
	if err != nil {
		ui.Warn("get tunnel: %v", err)
		return
	}
	meta, _ := t.Metadata["owned_by"].(string)
	if meta != "portx" {
		ui.Warn("tunnel %s not marked owned_by=portx; skipping delete", prof.TunnelID)
		return
	}
	if err := cf.DeleteTunnel(ctx, prof.AccountID, prof.TunnelID); err != nil {
		ui.Warn("delete tunnel: %v", err)
		return
	}
	ui.Success("deleted tunnel")
}
