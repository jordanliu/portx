package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/config"
	"portx/internal/daemon"
	"portx/internal/origin"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func startCommand() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Start all routes from portx.yaml",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "file", Usage: "path to project config (default: walk up for portx.yaml)"},
			&cli.StringSliceFlag{Name: "only", Usage: "start only these named routes"},
			&cli.BoolFlag{Name: "json", Usage: "JSON to stdout; keep session open until Ctrl+C"},
			&cli.BoolFlag{Name: "no-origin-check", Usage: "skip origin preflight"},
			&cli.BoolFlag{Name: "replace", Usage: "replace conflicting leases"},
		},
		Action: runStart,
	}
}

type heldRoute struct {
	name      string
	id        string
	routeID   string
	token     string
	target    string
	publicURL string
}

type startOpts struct {
	ctx         context.Context
	cmd         *cli.Command
	pc          config.ProjectConfig
	cfg         config.Config
	profileName string
	prof        config.Profile
	only        map[string]bool
}

func runStart(ctx context.Context, cmd *cli.Command) error {
	path := cmd.String("file")
	pc, err := config.LoadProject(path)
	if err != nil {
		if os.IsNotExist(err) {
			return apperr.New(apperr.ExitInvalidArgs, "no portx.yaml found; create one or use portx http --save")
		}
		return err
	}
	if err := pc.Validate(); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	profileName := config.ResolveProfile(cmd.String("profile"), pc.Profile, cfg.DefaultProfile)
	prof, err := cfg.Profile(profileName)
	if err != nil || !prof.IsConfigured() {
		return apperr.New(apperr.ExitInvalidArgs, "Custom hostnames require one-time setup.\n\nRun:\n  portx setup")
	}

	only := map[string]bool{}
	for _, n := range cmd.StringSlice("only") {
		only[n] = true
	}

	opts := startOpts{
		ctx:         ctx,
		cmd:         cmd,
		pc:          pc,
		cfg:         cfg,
		profileName: profileName,
		prof:        prof,
		only:        only,
	}
	if cmd.Bool("json") {
		return runStartJSON(opts)
	}
	return runStartTUI(opts)
}

func runStartTUI(opts startOpts) error {
	var held []heldRoute
	var client *rpc.Client

	err := ui.RunSession(opts.ctx, func(sessionCtx context.Context, p *tea.Program) error {
		ui.SetPhase(p, "Starting local daemon")
		var err error
		client, err = daemon.EnsureRunning(sessionCtx, opts.profileName)
		if err != nil {
			return apperr.Wrap(apperr.ExitDaemon, "start daemon", err)
		}
		if err := requireRequestEvents(client); err != nil {
			return err
		}
		ui.SetPhase(p, "Connecting Cloudflare tunnel")
		if err := client.StartTunnelContext(sessionCtx); err != nil {
			return err
		}

		held, err = registerProjectRoutes(opts, client, func(name string) {
			ui.SetPhase(p, "Registering "+name)
		})
		if err != nil {
			return err
		}

		routes := make([]ui.Route, len(held))
		for i, h := range held {
			routes[i] = ui.Route{Name: h.name, URL: h.publicURL, Target: h.target}
		}
		ui.SetReady(p, ui.ReadyInfo{
			Mode:    "managed",
			Profile: opts.profileName,
			Routes:  routes,
		})
		for _, route := range held {
			go streamRequestEvents(sessionCtx, client, p, route.routeID)
		}
		return nil
	}, func() error {
		for _, h := range held {
			if _, err := client.RenewLease(h.id, h.token); err != nil {
				return err
			}
		}
		return nil
	}, opts.cfg.Defaults.HeartbeatInterval)

	for _, h := range held {
		if client != nil {
			_ = client.ReleaseLease(h.id, h.token)
		}
	}
	if client != nil {
		_ = client.Close()
	}
	return err
}

func runStartJSON(opts startOpts) error {
	client, err := daemon.EnsureRunning(opts.ctx, opts.profileName)
	if err != nil {
		return apperr.Wrap(apperr.ExitDaemon, "start daemon", err)
	}
	defer client.Close()
	if err := client.StartTunnelContext(opts.ctx); err != nil {
		return err
	}

	held, err := registerProjectRoutes(opts, client, nil)
	if err != nil {
		return err
	}
	defer func() {
		for _, h := range held {
			_ = client.ReleaseLease(h.id, h.token)
		}
	}()

	type out struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Target string `json:"target"`
		Mode   string `json:"mode"`
		Status string `json:"status"`
	}
	results := make([]out, 0, len(held))
	for _, h := range held {
		results = append(results, out{
			Name:   h.name,
			URL:    h.publicURL,
			Target: h.target,
			Mode:   "managed",
			Status: "online",
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return err
	}

	hbEvery := opts.cfg.Defaults.HeartbeatInterval
	if hbEvery <= 0 {
		hbEvery = 15 * time.Second
	}
	return holdOpen(opts.ctx, func() error {
		for _, h := range held {
			if _, err := client.RenewLease(h.id, h.token); err != nil {
				return err
			}
		}
		return nil
	}, hbEvery)
}

func registerProjectRoutes(opts startOpts, client *rpc.Client, onRoute func(string)) ([]heldRoute, error) {
	held := make([]heldRoute, 0)
	names := make([]string, 0, len(opts.pc.Routes))
	for name := range opts.pc.Routes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		route := opts.pc.Routes[name]
		if len(opts.only) > 0 && !opts.only[name] {
			continue
		}
		if onRoute != nil {
			onRoute(name)
		}
		h, err := acquireProjectRoute(opts, client, name, route)
		if err != nil {
			for _, prev := range held {
				_ = client.ReleaseLease(prev.id, prev.token)
			}
			return nil, err
		}
		held = append(held, h)
	}
	if len(held) == 0 {
		return nil, apperr.New(apperr.ExitInvalidArgs, "no routes to start")
	}
	return held, nil
}

func acquireProjectRoute(opts startOpts, client *rpc.Client, name string, route config.ProjectRoute) (heldRoute, error) {
	target, err := origin.Normalize(route.Target)
	if err != nil {
		return heldRoute{}, apperr.Wrap(apperr.ExitInvalidArgs, fmt.Sprintf("route %q target", name), err)
	}
	if err := origin.ValidateTargetSafety(target); err != nil {
		return heldRoute{}, err
	}
	if !opts.cmd.Bool("no-origin-check") {
		pctx, cancel := context.WithTimeout(opts.ctx, 5*time.Second)
		err := origin.Preflight(pctx, target)
		cancel()
		if err != nil {
			return heldRoute{}, err
		}
	}
	public := route.Hostname
	if route.Path != "" {
		path := route.Path
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		public = route.Hostname + path
	}
	pr, err := origin.ParsePublicURL(public, opts.prof.Wildcard)
	if err != nil {
		return heldRoute{}, err
	}
	lease, err := client.AcquireLease(rpc.AcquireParams{
		Hostname:   pr.Hostname,
		PathPrefix: pr.PathPrefix,
		Target:     target.String(),
		HostHeader: route.HostHeader,
		OwnerPID:   os.Getpid(),
		Replace:    opts.cmd.Bool("replace"),
		Insecure:   route.Insecure,
	})
	if err != nil {
		return heldRoute{}, err
	}
	publicURL := "https://" + pr.Hostname
	if pr.PathPrefix != "/" {
		publicURL += pr.PathPrefix
	}
	return heldRoute{
		name:      name,
		id:        lease.ID,
		routeID:   lease.RouteID,
		token:     lease.OwnerToken,
		target:    target.String(),
		publicURL: publicURL,
	}, nil
}
