package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/cloudflared"
	"portx/internal/config"
	"portx/internal/daemon"
	"portx/internal/httpx"
	"portx/internal/origin"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func httpCommand() *cli.Command {
	return &cli.Command{
		Name:      "http",
		Usage:     "Expose a local HTTP origin",
		ArgsUsage: "<target>",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name: "url",
				Usage: "managed hostname: omit value to use the repo/folder name, " +
					"or pass a label/host (api → api.<namespace>)",
			},
			&cli.StringFlag{Name: "host-header", Usage: "override origin Host header"},
			&cli.StringFlag{Name: "scheme", Usage: "http|https"},
			&cli.BoolFlag{Name: "insecure-skip-verify", Usage: "skip TLS verify for HTTPS origins"},
			&cli.BoolFlag{Name: "reuse", Usage: "reuse identical existing lease"},
			&cli.BoolFlag{Name: "replace", Usage: "replace existing lease"},
			&cli.BoolFlag{Name: "save", Usage: "save route to portx.yaml"},
			&cli.StringFlag{Name: "name", Usage: "route name when using --save"},
			&cli.BoolFlag{Name: "json", Usage: "JSON to stdout; keep session open until Ctrl+C"},
			&cli.BoolFlag{Name: "no-origin-check", Usage: "skip origin preflight"},
		},
		Action: runHTTP,
	}
}

func runHTTP(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() < 1 {
		return apperr.New(apperr.ExitInvalidArgs, "usage: portx http <target>")
	}
	target, err := origin.Normalize(cmd.Args().First())
	if err != nil {
		return err
	}
	if scheme := cmd.String("scheme"); scheme != "" {
		if scheme != "http" && scheme != "https" {
			return apperr.New(apperr.ExitInvalidArgs, "scheme must be http or https")
		}
		target.Scheme = scheme
	}
	if hostHeader := cmd.String("host-header"); hostHeader != "" {
		if err := origin.ValidateHostHeader(hostHeader); err != nil {
			return err
		}
	}
	if err := origin.ValidateTargetSafety(target); err != nil {
		return err
	}
	if !cmd.Bool("no-origin-check") {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := origin.Preflight(pctx, target); err != nil {
			return err
		}
	}

	publicURL, managed, err := resolvePublicURL(cmd)
	if err != nil {
		return err
	}
	if !managed {
		if cmd.Bool("save") {
			return apperr.New(apperr.ExitInvalidArgs, "--save requires --url (managed hostname)")
		}
		return runQuickHTTP(ctx, cmd, target)
	}
	if cmd.Bool("save") {
		if err := saveProjectRoute(cmd, target, publicURL); err != nil {
			return err
		}
	}
	return runManagedHTTP(ctx, cmd, target, publicURL)
}

// resolvePublicURL:
//   - no --url → quick tunnel
//   - --url with no value → infer label from repo/folder
//   - --url=api / --url api → explicit managed hostname
func resolvePublicURL(cmd *cli.Command) (public string, managed bool, err error) {
	if !cmd.IsSet("url") {
		return "", false, nil
	}
	public = strings.TrimSpace(cmd.String("url"))
	if public != "" {
		return public, true, nil
	}
	label, err := origin.InferLabel()
	if err != nil {
		return "", false, err
	}
	return label, true, nil
}

func saveProjectRoute(cmd *cli.Command, target *url.URL, public string) error {
	name := cmd.String("name")
	if name == "" {
		host := public
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		if i := strings.Index(host, "."); i > 0 {
			name = host[:i]
		} else {
			name = host
		}
		if name == "" {
			name = "app"
		}
	}
	pc, err := config.LoadProject("")
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		pc = config.ProjectConfig{Version: 1, Routes: map[string]config.ProjectRoute{}}
	}
	prHost := public
	prPath := ""
	if strings.Contains(public, "://") {
		if u, err := url.Parse(public); err == nil {
			prHost = u.Hostname()
			prPath = u.Path
		}
	} else if i := strings.Index(public, "/"); i >= 0 {
		prHost = public[:i]
		prPath = public[i:]
	}
	pc.UpsertRoute(name, config.ProjectRoute{
		Target:     target.String(),
		Hostname:   prHost,
		Path:       prPath,
		HostHeader: cmd.String("host-header"),
		Insecure:   cmd.Bool("insecure-skip-verify"),
	})
	if pc.Project == "" {
		pc.Project = name
	}
	if err := config.SaveProject("", pc); err != nil {
		return err
	}
	reportSavedRoute(cmd.Bool("json"), name)
	return nil
}

func reportSavedRoute(jsonOutput bool, name string) {
	if jsonOutput {
		fmt.Fprintf(os.Stderr, "saved route %q to %s\n", name, config.ProjectFileName)
		return
	}
	ui.Success("saved route %q to %s", name, config.ProjectFileName)
}

func runQuickHTTP(ctx context.Context, cmd *cli.Command, target *url.URL) error {
	if cmd.Bool("json") {
		return runQuickHTTPJSON(ctx, cmd, target)
	}

	var (
		proc *cloudflared.Process
		srv  *http.Server
		ln   net.Listener
	)
	err := ui.RunSession(ctx, func(sessionCtx context.Context, p *tea.Program) error {
		ui.SetPhase(p, "Checking cloudflared")
		st, err := cloudflared.EnsureInstalled()
		if err != nil {
			return err
		}

		handler := newOriginProxy(target, cmd.String("host-header"), cmd.Bool("insecure-skip-verify"))
		var lerr error
		ln, lerr = net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			return lerr
		}
		srv = newOriginHTTPServer(handler)
		go func() { _ = srv.Serve(ln) }()

		ui.SetPhase(p, "Starting Quick Tunnel")
		proc, err = cloudflared.StartQuick(sessionCtx, st.Path, cloudflared.QuickOptions{
			OriginURL: "http://" + ln.Addr().String(),
		})
		if err != nil {
			return err
		}

		ui.SetPhase(p, "Waiting for public URL")
		waitCtx, cancel := context.WithTimeout(sessionCtx, 45*time.Second)
		defer cancel()
		public, err := proc.WaitQuickURL(waitCtx)
		if err != nil {
			return err
		}

		ui.SetReady(p, ui.ReadyInfo{
			URL:    public,
			Target: target.String(),
			Mode:   "quick",
			Note:   "Note: Quick Tunnels do not support SSE. Use managed mode for streaming.",
		})
		return nil
	}, nil, 0)

	if proc != nil {
		_ = proc.Stop(5 * time.Second)
	}
	if srv != nil {
		shctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = srv.Shutdown(shctx)
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	return err
}

func runQuickHTTPJSON(ctx context.Context, cmd *cli.Command, target *url.URL) error {
	handler := newOriginProxy(target, cmd.String("host-header"), cmd.Bool("insecure-skip-verify"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := newOriginHTTPServer(handler)
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()

	st, err := cloudflared.EnsureInstalled()
	if err != nil {
		return err
	}
	cfCtx, cfCancel := context.WithCancel(ctx)
	defer cfCancel()
	proc, err := cloudflared.StartQuick(cfCtx, st.Path, cloudflared.QuickOptions{
		OriginURL: "http://" + ln.Addr().String(),
	})
	if err != nil {
		return err
	}
	defer func() { _ = proc.Stop(10 * time.Second) }()

	waitCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	public, err := proc.WaitQuickURL(waitCtx)
	cancel()
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]string{
		"url":    public,
		"target": target.String(),
		"mode":   "quick",
		"status": "online",
	}); err != nil {
		return err
	}
	// Keep tunnel up until signal (same lifecycle as TUI).
	return holdOpen(ctx, nil, 0)
}

func runManagedHTTP(ctx context.Context, cmd *cli.Command, target *url.URL, public string) error {
	if cmd.Bool("json") {
		return runManagedHTTPJSON(ctx, cmd, target, public)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profileName := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)
	prof, err := cfg.Profile(profileName)
	if err != nil || !prof.IsConfigured() {
		return apperr.New(
			apperr.ExitInvalidArgs,
			"Custom hostnames require one-time setup.\n\nRun:\n  portx setup\n\nFor an immediate random URL:\n  portx http 3000",
		)
	}
	route, err := origin.ParsePublicURL(public, prof.Wildcard)
	if err != nil {
		return err
	}

	var client *rpc.Client
	var leaseID, leaseToken string

	hbEvery := cfg.Defaults.HeartbeatInterval
	if hbEvery <= 0 {
		hbEvery = 15 * time.Second
	}

	err = ui.RunSession(ctx, func(sessionCtx context.Context, p *tea.Program) error {
		ui.SetPhase(p, "Starting local daemon")
		var err error
		client, err = daemon.EnsureRunning(sessionCtx, profileName)
		if err != nil {
			return apperr.Wrap(apperr.ExitDaemon, "start daemon", err)
		}

		ui.SetPhase(p, "Connecting Cloudflare tunnel")
		if err := client.StartTunnelContext(sessionCtx); err != nil {
			return err
		}

		ui.SetPhase(p, fmt.Sprintf("Registering %s", route.Hostname))
		l, err := client.AcquireLease(rpc.AcquireParams{
			Hostname:   route.Hostname,
			PathPrefix: route.PathPrefix,
			Target:     target.String(),
			HostHeader: cmd.String("host-header"),
			OwnerPID:   os.Getpid(),
			Reuse:      cmd.Bool("reuse"),
			Replace:    cmd.Bool("replace"),
			Insecure:   cmd.Bool("insecure-skip-verify"),
		})
		if err != nil {
			return err
		}
		leaseID, leaseToken = l.ID, l.OwnerToken

		publicURL := "https://" + route.Hostname
		if route.PathPrefix != "/" {
			publicURL += route.PathPrefix
		}
		ui.SetReady(p, ui.ReadyInfo{
			URL:     publicURL,
			Target:  target.String(),
			Mode:    "managed",
			Profile: profileName,
		})
		return nil
	}, func() error {
		if client == nil || leaseID == "" {
			return nil
		}
		_, err := client.RenewLease(leaseID, leaseToken)
		return err
	}, hbEvery)

	if client != nil {
		if leaseID != "" {
			_ = client.ReleaseLease(leaseID, leaseToken)
		}
		_ = client.Close()
	}
	return err
}

func runManagedHTTPJSON(ctx context.Context, cmd *cli.Command, target *url.URL, public string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profileName := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)
	prof, err := cfg.Profile(profileName)
	if err != nil || !prof.IsConfigured() {
		return apperr.New(
			apperr.ExitInvalidArgs,
			"Custom hostnames require one-time setup.\n\nRun:\n  portx setup\n\nFor an immediate random URL:\n  portx http 3000",
		)
	}
	route, err := origin.ParsePublicURL(public, prof.Wildcard)
	if err != nil {
		return err
	}
	client, err := daemon.EnsureRunning(ctx, profileName)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.StartTunnelContext(ctx); err != nil {
		return err
	}
	l, err := client.AcquireLease(rpc.AcquireParams{
		Hostname:   route.Hostname,
		PathPrefix: route.PathPrefix,
		Target:     target.String(),
		HostHeader: cmd.String("host-header"),
		OwnerPID:   os.Getpid(),
		Reuse:      cmd.Bool("reuse"),
		Replace:    cmd.Bool("replace"),
		Insecure:   cmd.Bool("insecure-skip-verify"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.ReleaseLease(l.ID, l.OwnerToken) }()

	publicURL := "https://" + route.Hostname
	if route.PathPrefix != "/" {
		publicURL += route.PathPrefix
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]string{
		"url":    publicURL,
		"target": target.String(),
		"mode":   "managed",
		"status": "online",
	}); err != nil {
		return err
	}

	hbEvery := cfg.Defaults.HeartbeatInterval
	if hbEvery <= 0 {
		hbEvery = 15 * time.Second
	}
	return holdOpen(ctx, func() error {
		_, err := client.RenewLease(l.ID, l.OwnerToken)
		return err
	}, hbEvery)
}

func newOriginProxy(target *url.URL, hostHeader string, insecure bool) http.Handler {
	rp := &httputil.ReverseProxy{}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = origin.SafeDialContext
	tr.ResponseHeaderTimeout = 30 * time.Second
	if target.Scheme == "https" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: insecure, MinVersion: tls.VersionTLS12} //nolint:gosec
	}
	rp.Transport = tr
	rp.FlushInterval = 100 * time.Millisecond
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return nil
		}
		resp.Body = httpx.NewIdleTimeoutBody(
			resp.Body,
			httpx.ResponseBodyIdleLimit,
		)
		return nil
	}
	rp.Rewrite = func(proxyReq *httputil.ProxyRequest) {
		proxyReq.SetURL(target)
		proxyReq.Out.Host = target.Host
		if hostHeader != "" {
			proxyReq.Out.Host = hostHeader
		}
		stripForwardingHeaders(proxyReq.Out.Header)
		proxyReq.Out.Header.Set("X-PortX-Request-ID", uuid.NewString())
		proxyReq.Out.Header.Set("X-Forwarded-Proto", "https")
	}
	return withRequestBodyIdleTimeout(rp)
}

func withRequestBodyIdleTimeout(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			handler.ServeHTTP(w, r)
			return
		}
		r.Body = httpx.NewIdleTimeoutBody(r.Body, httpx.RequestBodyIdleLimit)
		defer r.Body.Close()
		handler.ServeHTTP(w, r)
	})
}

func stripForwardingHeaders(header http.Header) {
	for _, name := range []string{
		"Forwarded",
		"X-Forwarded-For",
		"X-Forwarded-Host",
		"X-Forwarded-Port",
		"X-Forwarded-Proto",
		"X-Real-IP",
		"CF-Connecting-IP",
	} {
		header.Del(name)
	}
}

func newOriginHTTPServer(handler http.Handler) *http.Server {
	return httpx.NewServer(handler)
}
