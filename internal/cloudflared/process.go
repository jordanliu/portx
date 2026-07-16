package cloudflared

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"portx/internal/apperr"
	"portx/internal/logredact"
	"portx/internal/procutil"
)

var (
	trycloudflareRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
	// cloudflared / API failure phrases when tunnel is gone or token invalid
	fatalLogRe = regexp.MustCompile(`(?i)(unauthorized|unauthorised|invalid token|tunnel.*(not found|deleted|disabled)|failed to (authenticate|register)|registration error|connection refused.*edge|403 forbidden|401 unauthorized)`)
)

type Process struct {
	cmd        *exec.Cmd
	bin        string
	metricsURL string
	logPath    string
	mu         sync.Mutex
	stdoutBuf  strings.Builder
	quickURL   string
	exited     atomic.Bool
	exitErr    error
	done       chan struct{}
}

type QuickOptions struct {
	OriginURL   string
	LogPath     string
	MetricsAddr string
}

type NamedOptions struct {
	Token       string
	LogPath     string
	MetricsAddr string
}

func freeLoopbackPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func StartQuick(ctx context.Context, bin string, opts QuickOptions) (*Process, error) {
	metricsAddr := opts.MetricsAddr
	if metricsAddr == "" {
		port, err := freeLoopbackPort()
		if err != nil {
			return nil, err
		}
		metricsAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	args := []string{
		"tunnel",
		"--no-autoupdate",
		"--metrics", metricsAddr,
		"--url", opts.OriginURL,
	}
	return start(ctx, startConfig{
		Bin:         bin,
		Args:        args,
		LogPath:     opts.LogPath,
		MetricsAddr: metricsAddr,
	})
}

func StartNamed(ctx context.Context, bin string, opts NamedOptions) (*Process, error) {
	if opts.Token == "" {
		return nil, apperr.New(apperr.ExitAuth, "tunnel token is empty")
	}
	metricsAddr := opts.MetricsAddr
	if metricsAddr == "" {
		port, err := freeLoopbackPort()
		if err != nil {
			return nil, err
		}
		metricsAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	args := []string{
		"tunnel",
		"--no-autoupdate",
		"--metrics", metricsAddr,
		"run",
	}
	return start(ctx, startConfig{
		Bin:         bin,
		Args:        args,
		Env:         append(os.Environ(), "TUNNEL_TOKEN="+opts.Token),
		LogPath:     opts.LogPath,
		MetricsAddr: metricsAddr,
	})
}

type startConfig struct {
	Bin         string
	Args        []string
	Env         []string
	LogPath     string
	MetricsAddr string
}

func start(ctx context.Context, cfg startConfig) (*Process, error) {
	cmd := exec.CommandContext(ctx, cfg.Bin, cfg.Args...)
	if cfg.Env != nil {
		cmd.Env = cfg.Env
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{
		cmd:        cmd,
		bin:        cfg.Bin,
		metricsURL: "http://" + cfg.MetricsAddr,
		logPath:    cfg.LogPath,
		done:       make(chan struct{}),
	}
	var logFile *os.File
	if cfg.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.LogPath), 0o700); err == nil {
			logFile, _ = os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		}
	}
	go p.consume(stdout, logFile)
	go p.consume(stderr, logFile)
	if err := cmd.Start(); err != nil {
		return nil, apperr.Wrap(apperr.ExitCloudflared, "start cloudflared", err)
	}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		p.exitErr = err
		p.exited.Store(true)
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *Process) consume(r io.Reader, logFile *os.File) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		p.mu.Lock()
		p.stdoutBuf.WriteString(line)
		p.stdoutBuf.WriteByte('\n')
		if p.quickURL == "" {
			if m := trycloudflareRe.FindString(line); m != "" {
				p.quickURL = m
			}
		}
		p.mu.Unlock()
		if logFile != nil {
			_, _ = logFile.WriteString(line + "\n")
		}
	}
}

func (p *Process) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) Alive() bool {
	if p.exited.Load() {
		return false
	}
	if p.cmd.Process == nil {
		return false
	}
	return procutil.Alive(p.cmd.Process.Pid)
}

func (p *Process) LogTail(max int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stdoutBuf.String()
	if max > 0 && len(s) > max {
		s = s[len(s)-max:]
	}
	return logredact.String(strings.TrimSpace(s))
}

func (p *Process) fatalFromLogs() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m := fatalLogRe.FindString(p.stdoutBuf.String()); m != "" {
		return m
	}
	return ""
}

func (p *Process) WaitQuickURL(ctx context.Context) (string, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if u := p.QuickURL(); u != "" {
			return u, nil
		}
		if u, err := p.fetchQuickTunnel(ctx); err == nil && u != "" {
			p.mu.Lock()
			p.quickURL = u
			p.mu.Unlock()
			return u, nil
		}
		if p.exited.Load() {
			return "", p.exitError("exited before publishing a URL")
		}
		select {
		case <-ctx.Done():
			return "", p.timeoutError("timeout waiting for Quick Tunnel URL")
		case <-p.done:
			return "", p.exitError("exited before publishing a URL")
		case <-ticker.C:
		}
	}
}

func (p *Process) QuickURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.quickURL
}

func (p *Process) fetchQuickTunnel(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.metricsURL+"/quicktunnel", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if i := strings.Index(s, `"hostname":"`); i >= 0 {
		rest := s[i+len(`"hostname":"`):]
		if j := strings.Index(rest, `"`); j >= 0 {
			h := rest[:j]
			if h == "" {
				return "", fmt.Errorf("empty hostname")
			}
			if !strings.HasPrefix(h, "https://") {
				h = "https://" + h
			}
			return h, nil
		}
	}
	return "", fmt.Errorf("no hostname")
}

func (p *Process) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := p.Ready(ctx); err == nil {
			return nil
		}
		if p.exited.Load() {
			return p.exitError("exited before becoming ready")
		}
		if fatal := p.fatalFromLogs(); fatal != "" {
			_ = p.Stop(3 * time.Second)
			return p.authError(fatal)
		}
		select {
		case <-ctx.Done():
			_ = p.Stop(3 * time.Second)
			return p.timeoutError("tunnel did not become ready")
		case <-p.done:
			return p.exitError("exited before becoming ready")
		case <-ticker.C:
		}
	}
}

func (p *Process) Ready(ctx context.Context) error {
	// Short request timeout so WaitReady loop stays responsive
	cctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, p.metricsURL+"/ready", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}

func (p *Process) Stop(timeout time.Duration) error {
	if p.cmd.Process == nil {
		return nil
	}
	if p.exited.Load() {
		return nil
	}
	pid := p.cmd.Process.Pid
	_ = procutil.Interrupt(pid)
	select {
	case <-p.done:
		return nil
	case <-time.After(timeout):
		_ = procutil.Kill(pid)
		<-p.done
		return nil
	}
}

func (p *Process) exitError(prefix string) error {
	tail := p.LogTail(800)
	msg := "cloudflared " + prefix
	if fatal := p.fatalFromLogs(); fatal != "" {
		return p.authError(fatal)
	}
	if tail != "" {
		msg += "\n\nLast logs:\n" + tail
	}
	msg += "\n\nIf you deleted the tunnel in Cloudflare, re-run:\n  portx setup"
	return apperr.New(apperr.ExitCloudflared, msg)
}

func (p *Process) timeoutError(prefix string) error {
	tail := p.LogTail(800)
	msg := "cloudflared " + prefix + "\n\n" +
		"The tunnel may have been deleted, or the token revoked.\n\n" +
		"Fix:\n  portx setup\n"
	if tail != "" {
		msg += "\nLast logs:\n" + tail
	}
	return apperr.New(apperr.ExitCloudflared, msg)
}

func (p *Process) authError(hint string) error {
	tail := p.LogTail(600)
	msg := "cloudflared could not connect to Cloudflare"
	if hint != "" {
		msg += " (" + hint + ")"
	}
	msg += "\n\nOften means the tunnel was deleted or the token is invalid.\n\n" +
		"Fix:\n  portx setup\n"
	if tail != "" {
		msg += "\nLast logs:\n" + tail
	}
	return apperr.New(apperr.ExitCloudflared, msg)
}
