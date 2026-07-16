package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/user"
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

func runSetup(ctx context.Context, cmd *cli.Command) error {
	cfg0, _ := config.Load()
	profileName := config.ResolveProfile(cmd.String("profile"), "", cfg0.DefaultProfile)
	token, preferAccount, preferZone, err := authenticateSetup(ctx, cmd)
	if err != nil {
		return err
	}

	cf := cloudflare.New(token)
	if err := cf.VerifyToken(ctx); err != nil {
		return err
	}

	store, err := credentials.Open()
	if err != nil {
		return err
	}
	if err := store.Set(credentials.APITokenKey(profileName), token); err != nil {
		return err
	}
	st, err := state.Open()
	if err != nil {
		return err
	}
	_ = st.SetPhase(state.PhaseAuthenticated)

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
	if !strings.HasSuffix(nsBase, zone.Name) && nsBase != zone.Name {
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
	_ = st.SetPhase(state.PhaseSelected)

	status := ui.StartStatus("Ensuring Cloudflare tunnel")
	cfg, err := config.Load()
	if err != nil {
		status.Fail(err)
		return err
	}

	tunnel, tunnelToken, err := ensureTunnel(ctx, ensureTunnelOpts{
		cf:          cf,
		store:       store,
		status:      status,
		cfg:         cfg,
		profileName: profileName,
		accountID:   account.ID,
	})
	if err != nil {
		return err
	}
	_ = st.SetPhase(state.PhaseTunnelEnsured)
	_ = st.SetPhase(state.PhaseTokenStored)
	_ = st.SetPhase(state.PhaseConfigApplied)

	dnsID, owned, err := ensureWildcardDNS(ctx, ensureDNSOpts{
		cf:     cf,
		status: status,
		zoneID: zone.ID,
		ns:     ns,
		tunnel: tunnel,
	})
	if err != nil {
		return err
	}
	_ = st.SetPhase(state.PhaseDNSEnsured)
	_ = st.PutProfile(profileName, state.ProfileState{
		TunnelID: tunnel.ID,
		WildcardDNS: &state.DNSRecord{
			RecordID:     dnsID,
			Hostname:     ns,
			OwnedByPortx: owned,
		},
	})

	status.Set("Saving local config")
	if err := saveSetupProfile(cfg, profileName, account, zone, ns, tunnel); err != nil {
		status.Fail(err)
		return err
	}

	status.Set("Verifying end-to-end")
	if err := verifySetup(ctx, cfg, tunnelToken, ns); err != nil {
		status.Stop()
		ui.Warn("End-to-end check failed: %v", err)
		ui.Dim("   Resources are provisioned; try after DNS propagates:")
		ui.Dim("   portx doctor")
		ui.Dim("   portx http --url=test.%s 3000", strings.TrimPrefix(ns, "*."))
	} else {
		status.Stop()
		_ = st.SetPhase(state.PhaseVerified)
		_ = st.SetPhase(state.PhaseReady)
	}

	ui.PrintSetupReady(ns, tunnel.Name)
	return nil
}

type ensureTunnelOpts struct {
	cf          *cloudflare.Client
	store       credentials.Store
	status      *ui.StatusLine
	cfg         config.Config
	profileName string
	accountID   string
}

func ensureTunnel(ctx context.Context, o ensureTunnelOpts) (cloudflare.Tunnel, string, error) {
	u, _ := user.Current()
	username := "user"
	if u != nil && u.Username != "" {
		username = sanitize(u.Username)
	}
	host, _ := os.Hostname()
	host = sanitize(host)
	tunnelName := fmt.Sprintf("portx-%s-%s", username, host)

	existing := o.cfg.Profiles[o.profileName]
	var tunnel cloudflare.Tunnel
	var err error

	if existing.TunnelID != "" {
		tunnel, err = o.cf.GetTunnel(ctx, o.accountID, existing.TunnelID)
		if err != nil {
			existing.TunnelID = ""
		}
	}
	if existing.TunnelID == "" {
		o.status.Set("Looking up tunnel " + tunnelName)
		list, err := o.cf.ListTunnels(ctx, o.accountID, tunnelName)
		if err != nil {
			o.status.Fail(err)
			return cloudflare.Tunnel{}, "", err
		}
		reused := false
		for _, t := range list {
			if t.Name != tunnelName {
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
			tunnel, err = o.cf.CreateTunnel(ctx, o.accountID, tunnelName, map[string]any{
				"owned_by": "portx",
				"machine":  host,
				"version":  1,
			})
			if err != nil {
				o.status.Fail(err)
				return cloudflare.Tunnel{}, "", err
			}
		}
	}

	o.status.Set("Fetching tunnel token")
	tunnelToken, err := o.cf.GetTunnelToken(ctx, o.accountID, tunnel.ID)
	if err != nil {
		o.status.Fail(err)
		return cloudflare.Tunnel{}, "", err
	}
	o.status.Set("Storing credentials (" + o.store.Backend() + ")")
	if err := o.store.Set(credentials.TunnelTokenKey(o.profileName), tunnelToken); err != nil {
		o.status.Fail(err)
		return cloudflare.Tunnel{}, "", err
	}

	proxyPort := o.cfg.Defaults.ProxyPort
	if proxyPort == 0 {
		proxyPort = 4041
	}
	originURL := fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
	o.status.Set("Configuring tunnel origin")
	if err := o.cf.PutTunnelConfig(ctx, o.accountID, tunnel.ID, originURL); err != nil {
		o.status.Fail(err)
		return cloudflare.Tunnel{}, "", err
	}
	return tunnel, tunnelToken, nil
}

type ensureDNSOpts struct {
	cf     *cloudflare.Client
	status *ui.StatusLine
	zoneID string
	ns     string
	tunnel cloudflare.Tunnel
}

func ensureWildcardDNS(ctx context.Context, o ensureDNSOpts) (dnsID string, owned bool, err error) {
	dnsName := strings.TrimPrefix(o.ns, "*.")
	cnameTarget := o.tunnel.ID + ".cfargotunnel.com"
	o.status.Set("Configuring DNS " + o.ns)
	records, err := o.cf.ListDNS(ctx, o.zoneID, o.ns, "CNAME")
	if err != nil {
		o.status.Fail(err)
		return "", false, apperr.Wrap(apperr.ExitProvision, "list DNS for wildcard", err)
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
			return r.ID, true, nil
		}
		o.status.Fail(fmt.Errorf("DNS conflict on %q", r.Name))
		return "", false, apperr.New(apperr.ExitProvision, fmt.Sprintf(
			"DNS record %q already exists pointing to %q\n\nPortX will not replace it automatically. Remove or rename it, then re-run setup.",
			r.Name, r.Content))
	}

	o.status.Set("Creating DNS " + o.ns)
	rec, err := o.cf.CreateDNS(ctx, o.zoneID, cloudflare.DNSRecord{
		Type:    "CNAME",
		Name:    o.ns,
		Content: cnameTarget,
		Proxied: true,
		Comment: "portx-managed",
	})
	if err != nil {
		o.status.Fail(err)
		return "", false, err
	}
	return rec.ID, true, nil
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

func verifySetup(ctx context.Context, cfg config.Config, tunnelToken, wildcard string) error {
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
	defer func() { _ = proc.Stop(5 * time.Second) }()
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
		Hint:        "Create one at  https://dash.cloudflare.com/profile/api-tokens\n  Permissions: Account → Cloudflare Tunnel Edit, Zone → DNS Edit",
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
