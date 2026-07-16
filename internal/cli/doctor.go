package cli

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/cloudflare"
	"portx/internal/cloudflared"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func doctorCommand() *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Diagnose common PortX problems",
		Action: runDoctor,
	}
}

type doctorReport struct {
	passed, warned, failed int
}

func (r *doctorReport) ok(format string, args ...any) {
	ui.CheckOK(format, args...)
	r.passed++
}

func (r *doctorReport) warn(format string, args ...any) {
	ui.CheckWarn(format, args...)
	r.warned++
}

func (r *doctorReport) check(name string, err error) bool {
	if err != nil {
		ui.CheckFail("%s: %v", name, err)
		r.failed++
		return false
	}
	ui.CheckOK("%s", name)
	r.passed++
	return true
}

func runDoctor(ctx context.Context, cmd *cli.Command) error {
	var r doctorReport
	ui.Title("portx doctor")

	_, err := config.ConfigDir()
	r.check("config directory", err)
	runtimeDir, err := config.RuntimeDir()
	r.check("runtime directory", err)

	checkCloudflared(&r)
	cfg, ok := checkConfig(&r)
	if ok {
		checkProfile(ctx, &r, cmd, cfg)
	}
	checkProject(&r)
	checkDaemon(&r, runtimeDir)

	ui.Summary(r.passed, r.warned, r.failed)
	if r.failed > 0 {
		return ui.ShownError{Err: fmt.Errorf("doctor found issues")}
	}
	return nil
}

func checkCloudflared(r *doctorReport) {
	st, err := cloudflared.Lookup()
	if err != nil {
		r.warn("cloudflared not on PATH: %s", cloudflared.InstallCommand())
		return
	}
	if err := cloudflared.CheckSupported(st.Version); err != nil {
		r.check("cloudflared support window", err)
		return
	}
	r.ok("cloudflared %s (%s)", st.Version, st.Path)
}

func checkConfig(r *doctorReport) (config.Config, bool) {
	cfg, err := config.Load()
	if !r.check("load config", err) {
		return config.Config{}, false
	}
	if !r.check("validate config", cfg.Validate()) {
		return cfg, false
	}
	return cfg, true
}

func checkProfile(ctx context.Context, r *doctorReport, cmd *cli.Command, cfg config.Config) {
	profile := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)
	p, err := cfg.Profile(profile)
	if err != nil {
		r.warn("profile %q not configured (managed mode unavailable)", profile)
		return
	}
	if !p.IsConfigured() {
		r.warn("profile %q not configured (managed mode unavailable)", profile)
		return
	}
	r.ok("profile %q configured (tunnel %s, %s)", profile, p.TunnelName, p.Wildcard)

	store, err := credentials.Open()
	if !r.check("credential store", err) {
		return
	}
	r.ok("credential store (%s)", store.Backend())
	_, err = store.Get(credentials.TunnelTokenKey(profile))
	r.check("tunnel token", err)
	apiTok, err := store.Get(credentials.APITokenKey(profile))
	if !r.check("api token", err) || apiTok == "" {
		return
	}

	checkCloudflareAPI(doctorAPIOpts{
		ctx:    ctx,
		report: r,
		cfg:    cfg,
		prof:   p,
		token:  apiTok,
	})
}

type doctorAPIOpts struct {
	ctx    context.Context
	report *doctorReport
	cfg    config.Config
	prof   config.Profile
	token  string
}

func checkCloudflareAPI(o doctorAPIOpts) {
	r := o.report
	cf := cloudflare.New(o.token)
	cctx, cancel := context.WithTimeout(o.ctx, 15*time.Second)
	defer cancel()

	if err := cf.VerifyToken(cctx); err != nil {
		r.check("cloudflare API access", err)
		return
	}
	r.ok("cloudflare API access")

	if o.prof.TunnelID != "" {
		t, err := cf.GetTunnel(cctx, o.prof.AccountID, o.prof.TunnelID)
		if err != nil {
			r.check("tunnel exists", err)
		} else {
			r.ok("tunnel status %s", t.Status)
		}
	}
	if o.prof.ZoneID != "" && o.prof.Wildcard != "" {
		recs, err := cf.ListDNS(cctx, o.prof.ZoneID, o.prof.Wildcard, "CNAME")
		if err != nil {
			r.check("wildcard DNS lookup", err)
		} else if len(recs) == 0 {
			r.warn("no CNAME found for %s", o.prof.Wildcard)
		} else {
			r.ok("wildcard DNS → %s", recs[0].Content)
		}
	}

	addr := net.JoinHostPort(o.cfg.Defaults.BindAddress, strconv.Itoa(o.cfg.Defaults.ProxyPort))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		r.warn("proxy port %s not free (daemon may own it): %v", addr, err)
		return
	}
	_ = ln.Close()
	r.ok("proxy port %s available", addr)
}

func checkProject(r *doctorReport) {
	pc, err := config.LoadProject("")
	if err != nil {
		return
	}
	if err := pc.Validate(); err != nil {
		r.check("project config", err)
		return
	}
	r.ok("project config (%d routes)", len(pc.Routes))
}

func checkDaemon(r *doctorReport, runtimeDir string) {
	c, err := rpc.Dial(filepath.Join(runtimeDir, "portxd.sock"))
	if err != nil {
		r.warn("daemon not running")
		return
	}
	st, err := c.GetStatus()
	_ = c.Close()
	if err != nil {
		r.check("daemon status", err)
		return
	}
	r.ok("daemon (proxy %s, leases %d, tunnel %v)", st.ProxyAddr, st.LeaseCount, st.TunnelRunning)
}
