package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	"portx/internal/procutil"
	"portx/internal/router"
	"portx/internal/rpc"
)

type Daemon struct {
	cfg        config.Config
	profile    string
	reg        *router.Registry
	leases     *leases.Manager
	requests   *requestBroker
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
		requests:   newRequestBroker(),
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

func (d *Daemon) SubscribeRequestEvents(routeID string) (<-chan router.RequestEvent, func()) {
	return d.requests.Subscribe(routeID)
}

func (d *Daemon) Run(ctx context.Context) (runErr error) {
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	d.mu.Lock()
	d.runCtx = runCtx
	d.mu.Unlock()
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	lock, err := os.OpenFile(d.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := flockExclusive(lock); err != nil {
		return apperr.New(apperr.ExitDaemon, "another portx daemon holds the lock")
	}
	if err := d.recoverCloudflared(); err != nil {
		return fmt.Errorf("recover cloudflared from previous daemon: %w", err)
	}

	// bind proxy
	proxy := router.NewProxy(d.reg, d.requests.Publish)
	srv, ln, err := router.ListenAndServe(d.proxyAddr, proxy)
	if err != nil {
		return apperr.Wrap(apperr.ExitDaemon, fmt.Sprintf("bind proxy on %s", d.proxyAddr), err)
	}
	defer func() {
		_ = srv.Close()
		_ = ln.Close()
	}()

	pidRecord, err := newPIDRecord(os.Getpid(), executable, "portxd")
	if err != nil {
		return err
	}
	if err := writePIDRecord(d.PidPath(), pidRecord); err != nil {
		return fmt.Errorf("write daemon pid file: %w", err)
	}

	rpcServer := rpc.NewServer(d)
	defer func() {
		cancelRun()
		close(d.stopCh)
		cleanupErr := errors.Join(
			rpcServer.Close(),
			removeIfPresent(d.SocketPath()),
			d.StopTunnel(),
			removeIfPresent(d.PidPath()),
		)
		if cleanupErr != nil {
			runErr = errors.Join(runErr, cleanupErr)
		}
	}()

	if _, err := d.leases.ReconcileWithError(); err != nil {
		log.Printf("portx: lease reconciliation failed: %v", err)
	}
	go d.leases.ExpireLoopWithError(d.stopCh, 5*time.Second, func(changed bool, err error) {
		if err != nil {
			log.Printf("portx: lease expiry cleanup failed: %v", err)
		}
		if changed && len(d.leases.List()) == 0 {
			d.scheduleIdle()
		}
	})

	errCh := make(chan error, 1)
	go func() { errCh <- rpcServer.Serve(d.SocketPath()) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		return err
	}
}

func (d *Daemon) GetStatus() (rpc.StatusResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := rpc.StatusResult{
		ProxyAddr:     d.proxyAddr,
		LeaseCount:    len(d.leases.List()),
		Profile:       d.profile,
		RequestEvents: true,
	}
	if d.cfProc != nil && d.cfProc.Alive() {
		st.TunnelRunning = true
		st.CloudflaredPID = d.cfProc.PID()
	}
	return st, nil
}

func (d *Daemon) AcquireLease(p rpc.AcquireParams) (leases.Lease, error) {
	return d.AcquireLeaseContext(context.Background(), p)
}

func (d *Daemon) AcquireLeaseContext(ctx context.Context, p rpc.AcquireParams) (leases.Lease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return leases.Lease{}, err
	}
	// Security boundary: re-validate everything the CLI may have skipped.
	prof, err := d.cfg.Profile(d.profile)
	if err != nil {
		if len(d.leases.List()) == 0 {
			d.scheduleIdle()
		}
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
	tunnelWasRunning := d.tunnelRunning()
	d.cancelIdle()

	if err := d.StartTunnelContext(ctx); err != nil {
		d.scheduleIdleIfUnused()
		return leases.Lease{}, err
	}
	l, err := d.leases.Acquire(leases.AcquireRequest{
		Hostname:       p.Hostname,
		PathPrefix:     p.PathPrefix,
		Target:         target.String(),
		HostHeader:     p.HostHeader,
		OwnerPID:       p.OwnerPID,
		OwnerStartTime: ownerStartTime(p),
		Ephemeral:      true,
		Insecure:       p.Insecure,
		Reuse:          p.Reuse,
		Replace:        p.Replace,
		TTL:            d.cfg.Defaults.LeaseTTL,
	})
	if err != nil {
		if !tunnelWasRunning {
			cleanupErr := d.StopTunnel()
			d.scheduleIdleIfUnused()
			return leases.Lease{}, errors.Join(err, cleanupErr)
		}
		d.scheduleIdleIfUnused()
		return leases.Lease{}, err
	}
	d.cancelIdle()
	return l, nil
}

func ownerStartTime(p rpc.AcquireParams) int64 {
	if p.OwnerStartTime > 0 {
		return p.OwnerStartTime
	}
	startTime, err := procutil.StartTime(p.OwnerPID)
	if err != nil {
		return 0
	}
	return startTime
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
	ctx := d.runCtx
	d.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	return d.startTunnel(ctx)
}

// StartTunnelContext lets the RPC server cancel an in-flight tunnel startup
// while it drains requests during daemon shutdown.
func (d *Daemon) StartTunnelContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return d.startTunnel(ctx)
}

func (d *Daemon) startTunnel(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
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
		if err := d.stopTunnelLocked(3 * time.Second); err != nil {
			return err
		}
	}

	prof, err := d.cfg.Profile(d.profile)
	if err != nil {
		return err
	}
	if !prof.IsConfigured() {
		return apperr.New(
			apperr.ExitInvalidArgs,
			"Custom hostnames require one-time setup.\n\nRun:\n  portx setup\n\n"+
				"For an immediate random URL:\n  portx http 3000",
		)
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
				"Cloudflare tunnel %s is missing or inaccessible.\n\n"+
					"It may have been deleted in the dashboard.\n\nFix:\n  portx setup",
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
	d.cfProc = proc
	pidPath := filepath.Join(d.runtimeDir, "cloudflared.pid")
	if err := persistPIDRecord(pidPath, proc.PID(), st.Path, "cloudflared"); err != nil {
		stopErr := d.stopTunnelLocked(3 * time.Second)
		return errors.Join(fmt.Errorf("write cloudflared pid file: %w", err), stopErr)
	}
	// Fail faster than before so the CLI spinner doesn't spin forever.
	readyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if err := proc.WaitReady(readyCtx); err != nil {
		return errors.Join(err, d.stopTunnelLocked(3*time.Second))
	}
	return nil
}

func (d *Daemon) tunnelRunning() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfProc != nil && d.cfProc.Alive()
}

func (d *Daemon) scheduleIdleIfUnused() {
	if len(d.leases.List()) == 0 {
		d.scheduleIdle()
	}
}

func (d *Daemon) StopTunnel() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopTunnelLocked(10 * time.Second)
}

func (d *Daemon) StopTunnelContext(ctx context.Context) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return d.StopTunnel()
}

func (d *Daemon) stopTunnelLocked(timeout time.Duration) error {
	if d.cfProc == nil {
		pidPath := filepath.Join(d.runtimeDir, "cloudflared.pid")
		if _, err := os.Stat(pidPath); err == nil {
			return fmt.Errorf("cloudflared pid file exists without a tracked process")
		} else if !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	proc := d.cfProc
	stopErr := proc.Stop(timeout)
	if proc.Alive() {
		if stopErr == nil {
			return fmt.Errorf("cloudflared is still running")
		}
		return fmt.Errorf("cloudflared is still running: %w", stopErr)
	}
	d.cfProc = nil
	return errors.Join(stopErr, removeIfPresent(filepath.Join(d.runtimeDir, "cloudflared.pid")))
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
			if err := d.stopTunnelLocked(10 * time.Second); err != nil {
				log.Printf("portx: idle cloudflared shutdown failed: %v", err)
			}
		}
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

type pidRecord struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Kind       string `json:"kind"`
	StartTime  int64  `json:"start_time"`
}

func newPIDRecord(pid int, executable, kind string) (pidRecord, error) {
	if pid <= 0 || executable == "" || kind == "" {
		return pidRecord{}, fmt.Errorf("invalid pid record")
	}
	startTime, err := processStartTime(pid)
	if err != nil {
		return pidRecord{}, fmt.Errorf("get process start time: %w", err)
	}
	return pidRecord{
		PID:        pid,
		Executable: executable,
		Kind:       kind,
		StartTime:  startTime,
	}, nil
}

func persistPIDRecord(path string, pid int, executable, kind string) error {
	record, err := newPIDRecord(pid, executable, kind)
	if err != nil {
		return err
	}
	return writePIDRecord(path, record)
}

func writePIDRecord(path string, record pidRecord) error {
	if record.PID <= 0 || record.Executable == "" || record.Kind == "" || record.StartTime <= 0 {
		return fmt.Errorf("invalid pid record")
	}
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	defer os.Remove(tmp)
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func processStartTime(pid int) (int64, error) {
	if pid <= 0 {
		return 0, os.ErrInvalid
	}
	if runtime.GOOS == "windows" {
		command := fmt.Sprintf(
			"$p=Get-Process -Id %d -ErrorAction Stop; $p.StartTime.ToUniversalTime().ToString('o')",
			pid,
		)
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command).Output()
		if err != nil {
			return 0, err
		}
		started, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
		if err != nil {
			return 0, err
		}
		return started.UnixNano(), nil
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return 0, err
	}
	started, err := time.ParseInLocation(
		"Mon Jan _2 15:04:05 2006",
		strings.TrimSpace(string(out)),
		time.Local,
	)
	if err != nil {
		return 0, err
	}
	return started.UnixNano(), nil
}
