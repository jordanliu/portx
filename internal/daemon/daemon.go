package daemon

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"portx/internal/apperr"
	"portx/internal/cloudflare"
	"portx/internal/cloudflared"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/leases"
	"portx/internal/origin"
	"portx/internal/router"
	"portx/internal/rpc"
)

type Daemon struct {
	cfg        config.Config
	profile    string
	reg        *router.Registry
	leases     *leases.Manager
	runtimeDir string
	proxyAddr  string

	mu       sync.Mutex
	runCtx   context.Context
	cfProc   *cloudflared.Process
	idleStop *time.Timer
	idleGen  uint64
	stopCh   chan struct{}
}

func New(cfg config.Config, profile string) (*Daemon, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	runtimeDir, err := config.RuntimeDir()
	if err != nil {
		return nil, err
	}
	if err := config.EnsureDir(runtimeDir); err != nil {
		return nil, err
	}
	if err := config.EnsureDir(filepath.Join(runtimeDir, "leases")); err != nil {
		return nil, err
	}
	reg := router.NewRegistry()
	m := leases.NewManager(reg, filepath.Join(runtimeDir, "leases"), cfg.Defaults.LeaseTTL)
	proxyAddr := net.JoinHostPort(cfg.Defaults.BindAddress, strconv.Itoa(cfg.Defaults.ProxyPort))
	return &Daemon{
		cfg:        cfg,
		profile:    profile,
		reg:        reg,
		leases:     m,
		runtimeDir: runtimeDir,
		proxyAddr:  proxyAddr,
		stopCh:     make(chan struct{}),
	}, nil
}

func (d *Daemon) SocketPath() string {
	return filepath.Join(d.runtimeDir, "portxd.sock")
}

func (d *Daemon) PidPath() string {
	return filepath.Join(d.runtimeDir, "portxd.pid")
}

func (d *Daemon) LockPath() string {
	return filepath.Join(d.runtimeDir, "portxd.lock")
}

func (d *Daemon) Run(ctx context.Context) error {
	d.runCtx = ctx

	lock, err := os.OpenFile(d.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := flockExclusive(lock); err != nil {
		return apperr.New(apperr.ExitDaemon, "another portx daemon holds the lock")
	}

	// bind proxy
	proxy := router.NewProxy(d.reg)
	srv, ln, err := router.ListenAndServe(d.proxyAddr, proxy)
	if err != nil {
		return apperr.Wrap(apperr.ExitDaemon, fmt.Sprintf("bind proxy on %s", d.proxyAddr), err)
	}
	defer func() {
		_ = srv.Close()
		_ = ln.Close()
	}()

	_ = os.WriteFile(d.PidPath(), []byte(strconv.Itoa(os.Getpid())), 0o600)
	defer os.Remove(d.PidPath())

	d.leases.Reconcile()
	go d.leases.ExpireLoop(d.stopCh, 5*time.Second)

	rpcServer := rpc.NewServer(d)
	errCh := make(chan error, 1)
	go func() { errCh <- rpcServer.Serve(d.SocketPath()) }()

	select {
	case <-ctx.Done():
		close(d.stopCh)
		_ = rpcServer.Close()
		_ = os.Remove(d.SocketPath())
		d.mu.Lock()
		if d.cfProc != nil {
			_ = d.cfProc.Stop(10 * time.Second)
		}
		d.mu.Unlock()
		return nil
	case err := <-errCh:
		close(d.stopCh)
		return err
	}
}

func (d *Daemon) GetStatus() (rpc.StatusResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := rpc.StatusResult{
		ProxyAddr:  d.proxyAddr,
		LeaseCount: len(d.leases.List()),
		Profile:    d.profile,
	}
	if d.cfProc != nil && d.cfProc.Alive() {
		st.TunnelRunning = true
		st.CloudflaredPID = d.cfProc.PID()
	}
	return st, nil
}

func (d *Daemon) AcquireLease(p rpc.AcquireParams) (leases.Lease, error) {
	// Security boundary: re-validate everything the CLI may have skipped.
	prof, err := d.cfg.Profile(d.profile)
	if err != nil {
		return leases.Lease{}, err
	}
	if !prof.IsConfigured() {
		return leases.Lease{}, apperr.New(apperr.ExitInvalidArgs, "profile not configured; run portx setup")
	}
	if err := origin.ValidateHostname(p.Hostname, prof.Wildcard); err != nil {
		return leases.Lease{}, err
	}
	if strings.ContainsAny(p.Hostname, "\r\n\x00") || strings.ContainsAny(p.HostHeader, "\r\n\x00") {
		return leases.Lease{}, apperr.New(apperr.ExitInvalidArgs, "invalid characters in hostname or host header")
	}
	target, err := origin.Normalize(p.Target)
	if err != nil {
		return leases.Lease{}, err
	}
	if err := origin.ValidateTargetSafety(target); err != nil {
		return leases.Lease{}, err
	}

	// Cancel idle before tunnel/lease work so a firing timer cannot stop a new session.
	d.cancelIdle()

	if err := d.StartTunnel(); err != nil {
		return leases.Lease{}, err
	}
	l, err := d.leases.Acquire(leases.AcquireRequest{
		Hostname:   p.Hostname,
		PathPrefix: p.PathPrefix,
		Target:     target.String(),
		HostHeader: p.HostHeader,
		OwnerPID:   p.OwnerPID,
		Ephemeral:  true,
		Insecure:   p.Insecure,
		Reuse:      p.Reuse,
		Replace:    p.Replace,
		TTL:        d.cfg.Defaults.LeaseTTL,
	})
	if err != nil {
		return leases.Lease{}, err
	}
	d.cancelIdle()
	return l, nil
}

func (d *Daemon) RenewLease(id, token string) (leases.Lease, error) {
	return d.leases.Renew(id, token, d.cfg.Defaults.LeaseTTL)
}

func (d *Daemon) ReleaseLease(id, token string) error {
	if err := d.leases.Release(id, token); err != nil {
		return err
	}
	if len(d.leases.List()) == 0 {
		d.scheduleIdle()
	}
	return nil
}

func (d *Daemon) ForceRelease(id string) error {
	if err := d.leases.ForceRelease(id); err != nil {
		return err
	}
	if len(d.leases.List()) == 0 {
		d.scheduleIdle()
	}
	return nil
}

func (d *Daemon) ListLeases() ([]leases.Lease, error) {
	return d.leases.List(), nil
}

func (d *Daemon) Profile() string {
	return d.profile
}

func (d *Daemon) StartTunnel() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	ctx := d.runCtx
	if ctx == nil {
		ctx = context.Background()
	}

	// Reuse only if still alive AND edge-ready (deleted tunnels can leave a living but broken process).
	if d.cfProc != nil {
		aliveAndReady := false
		if d.cfProc.Alive() {
			rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			aliveAndReady = d.cfProc.Ready(rctx) == nil
			cancel()
		}
		if aliveAndReady {
			return nil
		}
		_ = d.cfProc.Stop(3 * time.Second)
		d.cfProc = nil
	}

	prof, err := d.cfg.Profile(d.profile)
	if err != nil {
		return err
	}
	if !prof.IsConfigured() {
		return apperr.New(apperr.ExitInvalidArgs, "Custom hostnames require one-time setup.\n\nRun:\n  portx setup\n\nFor an immediate random URL:\n  portx http 3000")
	}
	store, err := credentials.Open()
	if err != nil {
		return err
	}
	token, err := store.Get(credentials.TunnelTokenKey(d.profile))
	if err != nil {
		return apperr.New(apperr.ExitAuth, "tunnel token missing; run portx setup")
	}

	// Best-effort: detect deleted tunnel via API before waiting on cloudflared.
	if apiTok, err := store.Get(credentials.APITokenKey(d.profile)); err == nil && apiTok != "" {
		cf := cloudflare.New(apiTok)
		apiCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		t, gerr := cf.GetTunnel(apiCtx, prof.AccountID, prof.TunnelID)
		cancel()
		if gerr != nil {
			return apperr.New(apperr.ExitCloudflared, fmt.Sprintf(
				"Cloudflare tunnel %s is missing or inaccessible.\n\nIt may have been deleted in the dashboard.\n\nFix:\n  portx setup",
				prof.TunnelID))
		}
		if t.DeletedAt != nil && *t.DeletedAt != "" {
			return apperr.New(apperr.ExitCloudflared,
				"Cloudflare tunnel was deleted.\n\nFix:\n  portx setup")
		}
	}

	st, err := cloudflared.EnsureInstalled()
	if err != nil {
		return err
	}
	logPath := filepath.Join(d.runtimeDir, "cloudflared.log")
	proc, err := cloudflared.StartNamed(ctx, st.Path, cloudflared.NamedOptions{
		Token:   token,
		LogPath: logPath,
	})
	if err != nil {
		return err
	}
	// Fail faster than before so the CLI spinner doesn't spin forever.
	readyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := proc.WaitReady(readyCtx); err != nil {
		_ = proc.Stop(3 * time.Second)
		return err
	}
	d.cfProc = proc
	_ = os.WriteFile(filepath.Join(d.runtimeDir, "cloudflared.pid"), []byte(strconv.Itoa(proc.PID())), 0o600)
	return nil
}

func (d *Daemon) StopTunnel() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfProc != nil {
		_ = d.cfProc.Stop(10 * time.Second)
		d.cfProc = nil
	}
	_ = os.Remove(filepath.Join(d.runtimeDir, "cloudflared.pid"))
	return nil
}

func (d *Daemon) scheduleIdle() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.idleStop != nil {
		d.idleStop.Stop()
	}
	d.idleGen++
	gen := d.idleGen
	timeout := d.cfg.Defaults.IdleTunnelTimeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	d.idleStop = time.AfterFunc(timeout, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if gen != d.idleGen {
			return
		}
		// Re-check under the same lock as tunnel stop (no StopTunnel re-lock).
		if len(d.leases.List()) > 0 {
			return
		}
		if d.cfProc != nil {
			_ = d.cfProc.Stop(10 * time.Second)
			d.cfProc = nil
		}
		_ = os.Remove(filepath.Join(d.runtimeDir, "cloudflared.pid"))
	})
}

func (d *Daemon) cancelIdle() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.idleGen++
	if d.idleStop != nil {
		d.idleStop.Stop()
		d.idleStop = nil
	}
}
