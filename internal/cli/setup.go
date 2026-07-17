package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/auth"
	"portx/internal/cloudflare"
	"portx/internal/cloudflared"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/state"
	"portx/internal/ui"
)

func setupCommand() *cli.Command {
	return &cli.Command{
		Name:  "setup",
		Usage: "One-time Cloudflare account, tunnel, and wildcard DNS setup",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "token", Usage: "use pasted API token instead of browser login"},
		},
		Action: runSetup,
	}
}

func runSetup(ctx context.Context, cmd *cli.Command) (err error) {
	cfg0, err := config.Load()
	if err != nil {
		return err
	}
	originalCfg := cloneConfig(cfg0)
	profileName := config.ResolveProfile(cmd.String("profile"), "", cfg0.DefaultProfile)
	token, preferAccount, preferZone, err := authenticateSetup(ctx, cmd)
	if err != nil {
		return err
	}

	cf := cloudflare.New(token)
	if err := cf.VerifyToken(ctx); err != nil {
		return err
	}
	if err := coordinateRuntime(false); err != nil {
		return fmt.Errorf("stop active PortX routes before setup: %w", err)
	}

	store, err := credentials.Open()
	if err != nil {
		return err
	}
	st, err := state.Open()
	if err != nil {
		return err
	}
	originalState := st.Data()
	previousState := originalState.Profiles[profileName]
	apiToken, err := snapshotCredential(store, credentials.APITokenKey(profileName))
	if err != nil {
		return err
	}
	tunnelToken, err := snapshotCredential(store, credentials.TunnelTokenKey(profileName))
	if err != nil {
		return err
	}
	rollback := &setupRollback{
		cf:            cf,
		store:         store,
		stateStore:    st,
		originalCfg:   originalCfg,
		originalState: originalState,
		profileName:   profileName,
		apiToken:      apiToken,
		tunnelToken:   tunnelToken,
	}
	defer func() {
		if err != nil && !rollback.keepResources {
			rollback.run()
		}
	}()
	if err := store.Set(credentials.APITokenKey(profileName), token); err != nil {
		return err
	}
	if err := st.SetPhase(state.PhaseAuthenticated); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}

	accounts, err := cf.ListAccounts(ctx)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return apperr.New(apperr.ExitProvision, "no Cloudflare accounts found for this token")
	}
	account, err := pickAccount(accounts, preferAccount)
	if err != nil {
		return err
	}
	rollback.accountID = account.ID

	zones, err := cf.ListZones(ctx, account.ID)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		return apperr.New(apperr.ExitProvision, "no zones found; add a site to Cloudflare first")
	}
	zone, err := pickZone(zones, preferZone)
	if err != nil {
		return err
	}
	rollback.zoneID = zone.ID
	if zone.Status != "active" {
		return apperr.New(apperr.ExitProvision, "zone is not active")
	}

	ns, err := ui.Input(ui.InputOpts{
		Title:       "Hostname namespace",
		Placeholder: "*." + zone.Name,
		Default:     "*." + zone.Name,
		Hint:        fmt.Sprintf("Hosts will look like  app.%s", zone.Name),
	})
	if err != nil {
		return apperr.New(apperr.ExitInvalidArgs, "setup cancelled")
	}
	if !strings.HasPrefix(ns, "*.") {
		return apperr.New(apperr.ExitInvalidArgs, "namespace must look like *.example.dev")
	}
	nsBase := strings.TrimPrefix(ns, "*.")
	zoneName := strings.TrimSuffix(strings.ToLower(zone.Name), ".")
	nsBaseLower := strings.TrimSuffix(strings.ToLower(nsBase), ".")
	if nsBaseLower != zoneName && !strings.HasSuffix(nsBaseLower, "."+zoneName) {
		return apperr.New(apperr.ExitInvalidArgs, "namespace must be inside the selected zone")
	}
	// Cloudflare Universal SSL covers apex + *.zone only (one label).
	// Namespaces like *.proxy.zone need Advanced Certificate Manager.
	if nsBase != zone.Name {
		ui.Blank()
		ui.Warn("Multi-level namespace: %s", ns)
		ui.Dim("   Cloudflare free Universal SSL covers only *.%s", zone.Name)
		ui.Dim("   Hosts like app.%s will show SSL/certificate errors", nsBase)
		ui.Dim("   unless you add an Advanced Certificate for %s", ns)
		ui.Blank()
		ui.Info("Recommended: use *.%s  (e.g. sample.%s)", zone.Name, zone.Name)
		ok, err := ui.Confirm("Continue with this namespace anyway?", false)
		if err != nil || !ok {
			return apperr.New(apperr.ExitInvalidArgs, "setup cancelled; re-run and accept the default *."+zone.Name)
		}
	}
	ui.KeyValue("Namespace", ns)
	if err := st.SetPhase(state.PhaseSelected); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}

	status := ui.StartStatus("Ensuring Cloudflare tunnel")
	cfg := cloneConfig(originalCfg)

	tunnel, tunnelTokenValue, created, previousConfig, err := ensureTunnel(ctx, ensureTunnelOpts{
		cf:          cf,
		store:       store,
		status:      status,
		cfg:         cfg,
		profileName: profileName,
		accountID:   account.ID,
	})
	rollback.tunnelCreated = created
	rollback.createdTunnelID = tunnel.ID
	if !created {
		rollback.reusedTunnelID = tunnel.ID
		rollback.previousConfig = previousConfig
	}
	if err != nil {
		return err
	}
	if err := st.SetPhase(state.PhaseTunnelEnsured); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}
	if err := st.SetPhase(state.PhaseTokenStored); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}
	if err := st.SetPhase(state.PhaseConfigApplied); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}

	dnsID, owned, created, err := ensureWildcardDNS(ctx, ensureDNSOpts{
		cf:     cf,
		status: status,
		zoneID: zone.ID,
		ns:     ns,
		tunnel: tunnel,
	})
	if err != nil {
		return err
	}
	rollback.dnsCreated = created
	rollback.createdDNSID = dnsID
	if err := st.SetPhase(state.PhaseDNSEnsured); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}
	status.Set("Saving local config")
	if err := saveSetupProfile(cfg, profileName, account, zone, ns, tunnel); err != nil {
		status.Fail(err)
		return err
	}
	if err := st.PutProfile(profileName, state.ProfileState{
		TunnelID: tunnel.ID,
		WildcardDNS: &state.DNSRecord{
			RecordID:     dnsID,
			Hostname:     ns,
			OwnedByPortx: owned,
		},
	}); err != nil {
		return fmt.Errorf("save setup state: %w", err)
	}
	status.Set("Verifying end-to-end")
	if err := verifySetup(ctx, cfg, tunnelTokenValue, ns); err != nil {
		status.Stop()
		rollback.keepResources = true
		ui.Warn("setup verification is pending: %v", err)
		ui.Dim("   Resources are provisioned, but setup is not verified yet:")
		ui.Dim("   portx doctor")
		ui.Dim("   portx http --url=test.%s 3000", strings.TrimPrefix(ns, "*."))
		return apperr.Wrap(
			apperr.ExitProvision,
			"setup provisioned resources but end-to-end verification failed",
			err,
		)
	} else {
		status.Stop()
		if err := st.SetPhase(state.PhaseVerified); err != nil {
			return fmt.Errorf("save setup state: %w", err)
		}
		if err := st.SetPhase(state.PhaseReady); err != nil {
			return fmt.Errorf("save setup state: %w", err)
		}
		if previousState.WildcardDNS != nil &&
			previousState.WildcardDNS.OwnedByPortx &&
			previousState.WildcardDNS.RecordID != "" &&
			previousState.WildcardDNS.RecordID != dnsID {
			if err := cf.DeleteDNS(ctx, zone.ID, previousState.WildcardDNS.RecordID); err != nil {
				return fmt.Errorf("remove previous PortX-managed DNS record: %w", err)
			}
			ui.Success("removed previous wildcard DNS")
		}
	}

	ui.PrintSetupReady(ns, tunnel.Name)
	return nil
}

type credentialSnapshot struct {
	value   string
	present bool
}

type setupRollback struct {
	cf              *cloudflare.Client
	store           credentials.Store
	stateStore      *state.Store
	originalCfg     config.Config
	originalState   state.Data
	profileName     string
	apiToken        credentialSnapshot
	tunnelToken     credentialSnapshot
	accountID       string
	zoneID          string
	createdTunnelID string
	createdDNSID    string
	tunnelCreated   bool
	dnsCreated      bool
	keepResources   bool
	reusedTunnelID  string
	previousConfig  map[string]any
}

func (r *setupRollback) run() {
	if r.reusedTunnelID != "" && r.previousConfig != nil && r.accountID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := r.cf.PutTunnelConfigValue(ctx, r.accountID, r.reusedTunnelID, r.previousConfig); err != nil {
			ui.Warn("setup rollback could not restore tunnel configuration: %v", err)
		}
		cancel()
	}
	if r.dnsCreated && r.createdDNSID != "" && r.zoneID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := r.cf.DeleteDNS(ctx, r.zoneID, r.createdDNSID); err != nil {
			ui.Warn("setup rollback could not delete DNS record %s: %v", r.createdDNSID, err)
		}
		cancel()
	}
	if r.tunnelCreated && r.createdTunnelID != "" && r.accountID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := r.cf.DeleteTunnel(ctx, r.accountID, r.createdTunnelID); err != nil {
			ui.Warn("setup rollback could not delete tunnel %s: %v", r.createdTunnelID, err)
		}
		cancel()
	}
	if err := restoreCredential(r.store, credentials.APITokenKey(r.profileName), r.apiToken); err != nil {
		ui.Warn("setup rollback could not restore API token: %v", err)
	}
	if err := restoreCredential(r.store, credentials.TunnelTokenKey(r.profileName), r.tunnelToken); err != nil {
		ui.Warn("setup rollback could not restore tunnel token: %v", err)
	}
	if err := r.stateStore.Replace(r.originalState); err != nil {
		ui.Warn("setup rollback could not restore local state: %v", err)
	}
	if err := config.Save(r.originalCfg); err != nil {
		ui.Warn("setup rollback could not restore local config: %v", err)
	}
}

func snapshotCredential(store credentials.Store, key string) (credentialSnapshot, error) {
	value, err := store.Get(key)
	if err == nil {
		return credentialSnapshot{value: value, present: true}, nil
	}
	var codeErr *apperr.CodeError
	if errors.As(err, &codeErr) && codeErr.Code == apperr.ExitAuth {
		return credentialSnapshot{}, nil
	}
	return credentialSnapshot{}, fmt.Errorf("read credential %q: %w", key, err)
}

func restoreCredential(store credentials.Store, key string, snapshot credentialSnapshot) error {
	if snapshot.present {
		return store.Set(key, snapshot.value)
	}
	return store.Delete(key)
}

func cloneConfig(cfg config.Config) config.Config {
	profiles := make(map[string]config.Profile, len(cfg.Profiles))
	for name, profile := range cfg.Profiles {
		profiles[name] = profile
	}
	cfg.Profiles = profiles
	return cfg
}

type ensureTunnelOpts struct {
	cf          *cloudflare.Client
	store       credentials.Store
	status      *ui.StatusLine
	cfg         config.Config
	profileName string
	accountID   string
}

func ensureTunnel(
	ctx context.Context,
	o ensureTunnelOpts,
) (cloudflare.Tunnel, string, bool, map[string]any, error) {
	host, _ := os.Hostname()
	host = sanitize(host)
	profile := sanitize(o.profileName)
	tunnelName := fmt.Sprintf("portx-%s-%s", profile, host)

	existing := o.cfg.Profiles[o.profileName]
	var tunnel cloudflare.Tunnel
	created := false
	var err error

	if existing.TunnelID != "" {
		tunnel, err = o.cf.GetTunnel(ctx, o.accountID, existing.TunnelID)
		if err != nil {
			if !cloudflare.IsNotFound(err) {
				o.status.Fail(err)
				return cloudflare.Tunnel{}, "", false, nil, fmt.Errorf(
					"look up configured tunnel %q: %w",
					existing.TunnelID,
					err,
				)
			}
			existing.TunnelID = ""
		}
		if err == nil && !tunnelReusable(tunnel) {
			existing.TunnelID = ""
		}
	}
	if existing.TunnelID == "" {
		o.status.Set("Looking up tunnel " + tunnelName)
		list, err := o.cf.ListTunnels(ctx, o.accountID, tunnelName)
		if err != nil {
			o.status.Fail(err)
			return cloudflare.Tunnel{}, "", false, nil, err
		}
		reused := false
		for _, t := range list {
			if t.Name != tunnelName || !tunnelReusable(t) {
				continue
			}
			meta, _ := t.Metadata["owned_by"].(string)
			if meta == "portx" {
				tunnel = t
				reused = true
				break
			}
		}
		if !reused {
			o.status.Set("Creating tunnel " + tunnelName)
			creationID := uuid.NewString()
			tunnel, err = o.cf.CreateTunnel(ctx, o.accountID, tunnelName, map[string]any{
				"owned_by":    "portx",
				"creation_id": creationID,
				"machine":     host,
				"version":     1,
			})
			if err == nil && tunnel.ID != "" {
				created = true
			} else {
				reconciled, reconcileErr := findOwnedTunnel(
					ctx,
					o.cf,
					o.accountID,
					tunnelName,
					creationID,
				)
				if reconcileErr == nil && reconciled.ID != "" {
					tunnel = reconciled
					created = true
				} else if err == nil {
					o.status.Fail(fmt.Errorf("cloudflare returned an empty tunnel ID"))
					if reconcileErr != nil {
						return cloudflare.Tunnel{}, "", false, nil, fmt.Errorf(
							"create tunnel returned no ID; reconcile ambiguous result: %v",
							reconcileErr,
						)
					}
					return cloudflare.Tunnel{}, "", false, nil, fmt.Errorf(
						"create tunnel returned no ID",
					)
				} else {
					o.status.Fail(err)
					if reconcileErr != nil {
						return cloudflare.Tunnel{}, "", false, nil, fmt.Errorf(
							"create tunnel: %w; reconcile ambiguous result: %v",
							err,
							reconcileErr,
						)
					}
					return cloudflare.Tunnel{}, "", false, nil, err
				}
			}
		}
	}
	var previousConfig map[string]any
	if !created {
		o.status.Set("Saving existing tunnel configuration")
		previousConfig, err = o.cf.GetTunnelConfig(ctx, o.accountID, tunnel.ID)
		if err != nil {
			o.status.Fail(err)
			return tunnel, "", false, nil, err
		}
	}

	o.status.Set("Fetching tunnel token")
	tunnelToken, err := o.cf.GetTunnelToken(ctx, o.accountID, tunnel.ID)
	if err != nil {
		o.status.Fail(err)
		return tunnel, "", created, previousConfig, err
	}
	o.status.Set("Storing credentials (" + o.store.Backend() + ")")
	if err := o.store.Set(credentials.TunnelTokenKey(o.profileName), tunnelToken); err != nil {
		o.status.Fail(err)
		return tunnel, "", created, previousConfig, err
	}

	proxyPort := o.cfg.Defaults.ProxyPort
	if proxyPort == 0 {
		proxyPort = 4041
	}
	originURL := fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
	o.status.Set("Configuring tunnel origin")
	if err := putTunnelConfigWithRetry(ctx, o.cf, o.accountID, tunnel.ID, originURL); err != nil {
		o.status.Fail(err)
		return tunnel, "", created, previousConfig, err
	}
	return tunnel, tunnelToken, created, previousConfig, nil
}

func findOwnedTunnel(
	ctx context.Context,
	cf *cloudflare.Client,
	accountID string,
	name string,
	creationID string,
) (cloudflare.Tunnel, error) {
	tunnels, err := cf.ListTunnels(ctx, accountID, name)
	if err != nil {
		return cloudflare.Tunnel{}, err
	}
	for _, tunnel := range tunnels {
		if tunnel.Name != name || !tunnelReusable(tunnel) {
			continue
		}
		ownedBy, _ := tunnel.Metadata["owned_by"].(string)
		if ownedBy != "portx" {
			continue
		}
		if creationID != "" {
			metadataID, _ := tunnel.Metadata["creation_id"].(string)
			if metadataID != creationID {
				continue
			}
		}
		return tunnel, nil
	}
	return cloudflare.Tunnel{}, fmt.Errorf("created tunnel was not found")
}

func putTunnelConfigWithRetry(ctx context.Context, cf *cloudflare.Client, accountID, tunnelID, originURL string) error {
	const attempts = 4
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if err := cf.PutTunnelConfig(ctx, accountID, tunnelID, originURL); err == nil {
			return nil
		} else {
			lastErr = err
			if !strings.Contains(strings.ToLower(err.Error()), "tunnel not found") {
				return err
			}
		}
		if attempt == attempts-1 {
			break
		}
		delay := time.Duration(1<<attempt) * time.Second
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf(
		"configure tunnel %s: %w (Cloudflare may still be propagating the tunnel; retry setup if this persists)",
		tunnelID,
		lastErr,
	)
}

func tunnelReusable(tunnel cloudflare.Tunnel) bool {
	if tunnel.ID == "" {
		return false
	}
	if tunnel.DeletedAt != nil && strings.TrimSpace(*tunnel.DeletedAt) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(tunnel.Status)) {
	case "deleted", "deleting":
		return false
	default:
		// Cloudflare reports a stopped but startable tunnel as inactive.
		return true
	}
}

type ensureDNSOpts struct {
	cf     *cloudflare.Client
	status *ui.StatusLine
	zoneID string
	ns     string
	tunnel cloudflare.Tunnel
}

func ensureWildcardDNS(ctx context.Context, o ensureDNSOpts) (dnsID string, owned bool, created bool, err error) {
	dnsName := strings.TrimPrefix(o.ns, "*.")
	cnameTarget := o.tunnel.ID + ".cfargotunnel.com"
	creationMarker := "portx-managed-" + uuid.NewString()
	o.status.Set("Configuring DNS " + o.ns)
	records, err := o.cf.ListDNS(ctx, o.zoneID, o.ns, "CNAME")
	if err != nil {
		o.status.Fail(err)
		return "", false, false, apperr.Wrap(apperr.ExitProvision, "list DNS for wildcard", err)
	}
	wantNames := map[string]bool{
		o.ns:           true,
		"*." + dnsName: true,
		dnsName:        true,
	}
	for _, r := range records {
		if !wantNames[r.Name] && !wantNames[strings.ToLower(r.Name)] {
			continue
		}
		if r.Content == cnameTarget || r.Content == o.tunnel.ID+".cfargotunnel.com" {
			owned := strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Comment)), "portx-managed")
			return r.ID, owned, false, nil
		}
		o.status.Fail(fmt.Errorf("DNS conflict on %q", r.Name))
		return "", false, false, apperr.New(apperr.ExitProvision, fmt.Sprintf(
			"DNS record %q already exists pointing to %q\n\n"+
				"PortX will not replace it automatically. Remove or rename it, then re-run setup.",
			r.Name, r.Content))
	}

	o.status.Set("Creating DNS " + o.ns)
	rec, err := o.cf.CreateDNS(ctx, o.zoneID, cloudflare.DNSRecord{
		Type:    "CNAME",
		Name:    o.ns,
		Content: cnameTarget,
		Proxied: true,
		Comment: creationMarker,
	})
	if err != nil || rec.ID == "" {
		reconciled, reconcileErr := findMatchingDNS(
			ctx,
			o.cf,
			o.zoneID,
			o.ns,
			cnameTarget,
			creationMarker,
		)
		if reconcileErr == nil && reconciled.ID != "" {
			rec = reconciled
		} else if err == nil {
			o.status.Fail(fmt.Errorf("cloudflare returned an empty DNS record ID"))
			if reconcileErr != nil {
				return "", false, false, fmt.Errorf(
					"create DNS record returned no ID; reconcile ambiguous result: %v",
					reconcileErr,
				)
			}
			return "", false, false, fmt.Errorf("create DNS record returned no ID")
		} else {
			o.status.Fail(err)
			if reconcileErr != nil {
				return "", false, false, fmt.Errorf(
					"create DNS record: %w; reconcile ambiguous result: %v",
					err,
					reconcileErr,
				)
			}
			return "", false, false, err
		}
	}
	return rec.ID, true, true, nil
}

func findMatchingDNS(
	ctx context.Context,
	cf *cloudflare.Client,
	zoneID string,
	name string,
	content string,
	comment string,
) (cloudflare.DNSRecord, error) {
	records, err := cf.ListDNS(ctx, zoneID, name, "CNAME")
	if err != nil {
		return cloudflare.DNSRecord{}, err
	}
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Name), strings.TrimSpace(name)) {
			continue
		}
		if !strings.EqualFold(strings.TrimSuffix(record.Content, "."), strings.TrimSuffix(content, ".")) {
			continue
		}
		if comment != "" && record.Comment != comment {
			continue
		}
		return record, nil
	}
	return cloudflare.DNSRecord{}, fmt.Errorf("created DNS record was not found")
}

func saveSetupProfile(
	cfg config.Config,
	profileName string,
	account cloudflare.Account,
	zone cloudflare.Zone,
	ns string,
	tunnel cloudflare.Tunnel,
) error {
	cfg.DefaultProfile = profileName
	cfg.Profiles[profileName] = config.Profile{
		AccountID:  account.ID,
		ZoneID:     zone.ID,
		Domain:     zone.Name,
		Wildcard:   ns,
		TunnelID:   tunnel.ID,
		TunnelName: tunnel.Name,
	}
	return config.Save(cfg)
}

func verifySetup(ctx context.Context, cfg config.Config, tunnelToken, wildcard string) (err error) {
	st, err := cloudflared.EnsureInstalled()
	if err != nil {
		return err
	}
	bin := st.Path
	nonce := uuid.NewString()
	label := "portx-setup-" + uuid.NewString()[:8]
	host := label + strings.TrimPrefix(wildcard, "*")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nonce))
	})
	proxyPort := cfg.Defaults.ProxyPort
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("proxy port %d busy (is portx daemon already running?): %w", proxyPort, err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()

	runtimeDir, _ := config.RuntimeDir()
	_ = config.EnsureDir(runtimeDir)
	proc, err := cloudflared.StartNamed(ctx, bin, cloudflared.NamedOptions{
		Token:   tunnelToken,
		LogPath: filepath.Join(runtimeDir, "setup-cloudflared.log"),
	})
	if err != nil {
		return err
	}
	defer func() {
		if stopErr := proc.Stop(5 * time.Second); err == nil && stopErr != nil {
			err = fmt.Errorf("stop setup cloudflared: %w", stopErr)
		}
	}()
	rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := proc.WaitReady(rctx); err != nil {
		return err
	}

	// public probe
	client := &http.Client{Timeout: 15 * time.Second}
	var last error
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, "https://"+host, nil)
		resp, err := client.Do(req)
		if err != nil {
			last = err
			time.Sleep(2 * time.Second)
			continue
		}
		body := make([]byte, 64)
		n, _ := resp.Body.Read(body)
		_ = resp.Body.Close()
		if string(body[:n]) == nonce {
			return nil
		}
		last = fmt.Errorf("unexpected body")
		time.Sleep(2 * time.Second)
	}
	return last
}

func authenticateSetup(ctx context.Context, cmd *cli.Command) (token, preferAccount, preferZone string, err error) {
	// Prefer env over interactive paste for non-interactive use (never argv).
	if t := strings.TrimSpace(os.Getenv("CLOUDFLARE_API_TOKEN")); t != "" {
		useEnvToken := cmd.Bool("token") || !isTerminalStdin()
		if useEnvToken {
			return t, "", "", nil
		}
	}
	if cmd.Bool("token") {
		return promptAPIToken()
	}

	_, choice, err := ui.Select("How do you want to authenticate?", []ui.Choice{
		{Label: "Browser login", Desc: "open Cloudflare (recommended; needs cloudflared)", Value: "browser"},
		{Label: "API token", Desc: "paste a token from the dashboard", Value: "token"},
	})
	if err != nil {
		return "", "", "", apperr.New(apperr.ExitInvalidArgs, "setup cancelled")
	}

	if choice.Value == "token" {
		return promptAPIToken()
	}

	// Default: browser OAuth-style login via cloudflared tunnel login
	if _, err := cloudflared.Lookup(); err != nil {
		return "", "", "", apperr.New(apperr.ExitCloudflared, fmt.Sprintf(
			"Browser login needs cloudflared on your PATH.\n\n"+
				"  %s\n\n"+
				"Then re-run:  portx setup\n\n"+
				"Or choose API token and paste one.",
			cloudflared.InstallCommand()))
	}
	ui.Blank()
	ui.Info("Opening Cloudflare in your browser…")
	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	res, err := auth.BrowserLogin(loginCtx)
	if err != nil {
		return "", "", "", err
	}
	ui.Success("Signed in with browser")
	return res.APIToken, res.AccountID, res.ZoneID, nil
}

func promptAPIToken() (token, preferAccount, preferZone string, err error) {
	t, err := ui.Input(ui.InputOpts{
		Title:       "API token",
		Placeholder: "paste token",
		Password:    true,
		Hint: "Create one at  https://dash.cloudflare.com/profile/api-tokens\n" +
			"  Required permissions: https://github.com/jordanliu/portx#custom-hostnames",
	})
	if err != nil {
		return "", "", "", apperr.New(apperr.ExitInvalidArgs, "setup cancelled")
	}
	if t == "" {
		return "", "", "", apperr.New(apperr.ExitAuth, "API token is required")
	}
	return t, "", "", nil
}

func pickAccount(accounts []cloudflare.Account, preferID string) (cloudflare.Account, error) {
	if preferID != "" {
		for _, a := range accounts {
			if a.ID == preferID {
				ui.KeyValue("Account", a.Name+"  (from login)")
				return a, nil
			}
		}
	}
	if len(accounts) == 1 {
		ui.KeyValue("Account", accounts[0].Name)
		return accounts[0], nil
	}
	choices := make([]ui.Choice, len(accounts))
	for i, a := range accounts {
		choices[i] = ui.Choice{Label: a.Name, Desc: a.ID, Value: a.ID}
	}
	idx, _, err := ui.Select("Select an account", choices)
	if err != nil {
		return cloudflare.Account{}, apperr.New(apperr.ExitInvalidArgs, "setup cancelled")
	}
	ui.KeyValue("Account", accounts[idx].Name)
	return accounts[idx], nil
}

func pickZone(zones []cloudflare.Zone, preferID string) (cloudflare.Zone, error) {
	if preferID != "" {
		for _, z := range zones {
			if z.ID == preferID {
				ui.KeyValue("Zone", z.Name+"  (from login)")
				return z, nil
			}
		}
	}
	if len(zones) == 1 {
		ui.KeyValue("Zone", zones[0].Name)
		return zones[0], nil
	}
	choices := make([]ui.Choice, len(zones))
	for i, z := range zones {
		choices[i] = ui.Choice{Label: z.Name, Desc: z.Status, Value: z.ID}
	}
	idx, _, err := ui.Select("Select a zone", choices)
	if err != nil {
		return cloudflare.Zone{}, apperr.New(apperr.ExitInvalidArgs, "setup cancelled")
	}
	ui.KeyValue("Zone", zones[idx].Name)
	return zones[idx], nil
}

func isTerminalStdin() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "host"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
