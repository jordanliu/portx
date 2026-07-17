package cloudflared

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"portx/internal/apperr"
	"portx/internal/procutil"
)

var (
	trycloudflareRe = regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)
	metricsAddrRe   = regexp.MustCompile(`(?i)(?:starting metrics server on\s+|addr[=:]"?)(127\.0\.0\.1:\d+)`)
	// cloudflared / API failure phrases when tunnel is gone or token invalid
	fatalLogRe = regexp.MustCompile(`(?i)(unauthorized|unauthorised|invalid token|tunnel.*(not found|deleted|disabled)|failed to (authenticate|register)|registration error|connection refused.*edge|403 forbidden|401 unauthorized)`)
)

type Process struct {
	cmd        *exec.Cmd
	bin        string
	metricsURL string
	startTime  int64
	logPath    string
	mu         sync.Mutex
	stdoutBuf  strings.Builder
	logMu      sync.Mutex
	logFile    *os.File
	logClose   sync.Once
	outputErr  error
	quickURL   string
	exited     atomic.Bool
	exitErr    error
	done       chan struct{}
}

const maxMetricsResponseBytes = 64 << 10

const (
	quickDNSPollInterval   = time.Second
	quickDNSPropagationLag = 8 * time.Second
	quickDNSLookupTimeout  = 5 * time.Second
	quickDoHLookupTimeout  = 2 * time.Second
	quickHostLookupTimeout = 2 * time.Second
)

const (
	cloudflareDoHEndpoint = "https://cloudflare-dns.com/dns-query"
	googleDoHEndpoint     = "https://dns.google/resolve"
)

var metricsClient = &http.Client{Timeout: 2 * time.Second}

var quickTunnelResolver hostnameResolver = fallbackHostnameResolver{
	primary: anyHostnameResolver{
		resolvers: []hostnameResolver{
			dohHostnameResolver{
				client:   newDoHClient(),
				endpoint: cloudflareDoHEndpoint,
			},
			dohHostnameResolver{
				client:   newDoHClient(),
				endpoint: googleDoHEndpoint,
			},
		},
	},
	fallback:        net.DefaultResolver,
	primaryTimeout:  quickDoHLookupTimeout,
	fallbackTimeout: quickHostLookupTimeout,
}

func newDoHClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	return &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	}
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

func StartQuick(ctx context.Context, bin string, opts QuickOptions) (*Process, error) {
	args := quickArgs(opts.OriginURL, opts.MetricsAddr)
	return start(ctx, startConfig{
		Bin:         bin,
		Args:        args,
		LogPath:     opts.LogPath,
		MetricsAddr: opts.MetricsAddr,
	})
}

func StartNamed(ctx context.Context, bin string, opts NamedOptions) (*Process, error) {
	if opts.Token == "" {
		return nil, apperr.New(apperr.ExitAuth, "tunnel token is empty")
	}
	args := namedArgs(opts.MetricsAddr)
	return start(ctx, startConfig{
		Bin:         bin,
		Args:        args,
		Env:         append(os.Environ(), "TUNNEL_TOKEN="+opts.Token),
		LogPath:     opts.LogPath,
		MetricsAddr: opts.MetricsAddr,
	})
}

func quickArgs(originURL, metricsAddr string) []string {
	return []string{
		"tunnel",
		"--no-autoupdate",
		"--metrics", metricsArgument(metricsAddr),
		"--url", originURL,
	}
}

func namedArgs(metricsAddr string) []string {
	return []string{
		"tunnel",
		"--no-autoupdate",
		"--metrics", metricsArgument(metricsAddr),
		"run",
	}
}

func metricsArgument(metricsAddr string) string {
	if metricsAddr == "" {
		return "127.0.0.1:0"
	}
	return metricsAddr
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
	logFile, err := openLogFile(cfg.LogPath)
	if err != nil {
		return nil, apperr.Wrap(apperr.ExitCloudflared, "open cloudflared log", err)
	}
	p := &Process{
		cmd:     cmd,
		bin:     cfg.Bin,
		logPath: cfg.LogPath,
		done:    make(chan struct{}),
		logFile: logFile,
	}
	if cfg.MetricsAddr != "" {
		p.metricsURL = "http://" + cfg.MetricsAddr
	}
	stdout := &processWriter{process: p}
	stderr := &processWriter{process: p}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		p.closeLog()
		return nil, apperr.Wrap(apperr.ExitCloudflared, "start cloudflared", err)
	}
	startTime, err := procutil.StartTime(cmd.Process.Pid)
	if err != nil {
		_ = procutil.Kill(cmd.Process.Pid)
		_ = cmd.Wait()
		p.closeLog()
		return nil, fmt.Errorf("record cloudflared process identity: %w", err)
	}
	p.startTime = startTime
	go func() {
		err := cmd.Wait()
		stdout.Flush()
		stderr.Flush()
		p.closeLog()
		p.mu.Lock()
		p.exitErr = err
		p.exited.Store(true)
		p.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

func (p *Process) consume(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		p.consumeLine(sc.Text())
	}
	if err := sc.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		p.recordOutputError(fmt.Errorf("read cloudflared output: %w", err))
	}
}

func (p *Process) consumeLine(line string) {
	p.mu.Lock()
	p.stdoutBuf.WriteString(line)
	p.stdoutBuf.WriteByte('\n')
	if p.stdoutBuf.Len() > 64*1024 {
		s := p.stdoutBuf.String()
		p.stdoutBuf.Reset()
		p.stdoutBuf.WriteString(s[len(s)-64*1024:])
	}
	if p.quickURL == "" {
		if m := trycloudflareRe.FindString(line); m != "" {
			p.quickURL = m
		}
	}
	if p.metricsURL == "" {
		if match := metricsAddrRe.FindStringSubmatch(line); len(match) == 2 {
			p.metricsURL = "http://" + match[1]
		}
	}
	p.mu.Unlock()
	p.writeLog(line)
}

type processWriter struct {
	process *Process
	mu      sync.Mutex
	buffer  bytes.Buffer
}

func (w *processWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	_, _ = w.buffer.Write(data)
	for {
		line, err := w.buffer.ReadString('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return 0, err
			}
			w.buffer.WriteString(line)
			break
		}
		w.process.consumeLine(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	}
	return len(data), nil
}

func (w *processWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buffer.Len() == 0 {
		return
	}
	w.process.consumeLine(strings.TrimSuffix(strings.TrimSuffix(w.buffer.String(), "\n"), "\r"))
	w.buffer.Reset()
}

func openLogFile(path string) (*os.File, error) {
	if path == "" {
		return nil, nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("log path is not a regular file: %q", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (p *Process) writeLog(line string) {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	if p.logFile == nil {
		return
	}
	if _, err := p.logFile.WriteString(line + "\n"); err != nil {
		p.recordOutputErrorLocked(fmt.Errorf("write cloudflared log: %w", err))
		_ = p.logFile.Close()
		p.logFile = nil
	}
}

func (p *Process) recordOutputError(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outputErr == nil {
		p.outputErr = err
	}
}

func (p *Process) recordOutputErrorLocked(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outputErr == nil {
		p.outputErr = err
	}
}

func (p *Process) outputError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.outputErr
}

func (p *Process) closeLog() {
	p.logClose.Do(func() {
		p.logMu.Lock()
		defer p.logMu.Unlock()
		if p.logFile != nil {
			if err := p.logFile.Close(); err != nil {
				p.recordOutputErrorLocked(fmt.Errorf("close cloudflared log: %w", err))
			}
			p.logFile = nil
		}
	})
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
	return strings.TrimSpace(s)
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
	p.mu.Lock()
	metricsURL := p.metricsURL
	p.mu.Unlock()
	if metricsURL == "" {
		return "", errors.New("metrics endpoint is not ready")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metricsURL+"/quicktunnel", nil)
	if err != nil {
		return "", err
	}
	resp, err := metricsClient.Do(req)
	if err != nil {
		return "", err
	}
	body, bodyErr := readMetricsBody(resp)
	if resp.StatusCode != http.StatusOK {
		if bodyErr != nil {
			return "", errors.Join(fmt.Errorf("quick tunnel endpoint returned %s", resp.Status), bodyErr)
		}
		return "", fmt.Errorf("quick tunnel endpoint returned %s", resp.Status)
	}
	if bodyErr != nil {
		return "", bodyErr
	}
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

// WaitQuickReady waits until both the cloudflared connector and its generated
// public hostname are ready. cloudflared can publish a Quick Tunnel URL before
// the corresponding DNS record is resolvable.
func (p *Process) WaitQuickReady(ctx context.Context, publicURL string) error {
	if err := p.WaitReady(ctx); err != nil {
		return err
	}
	hostname, err := quickTunnelHostname(publicURL)
	if err != nil {
		return err
	}
	if err := p.waitQuickDNSGrace(ctx); err != nil {
		return err
	}
	return p.waitQuickDNS(ctx, quickTunnelResolver, hostname)
}

func (p *Process) waitQuickDNSGrace(ctx context.Context) error {
	timer := time.NewTimer(quickDNSPropagationLag)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for quick tunnel DNS propagation: %w", ctx.Err())
	case <-p.done:
		return p.exitError("exited before its hostname could propagate")
	}
}

type hostnameResolver interface {
	LookupHost(context.Context, string) ([]string, error)
}

type fallbackHostnameResolver struct {
	primary         hostnameResolver
	fallback        hostnameResolver
	primaryTimeout  time.Duration
	fallbackTimeout time.Duration
}

type anyHostnameResolver struct {
	resolvers []hostnameResolver
}

type hostnameResult struct {
	addresses []string
	err       error
}

func (r anyHostnameResolver) LookupHost(
	ctx context.Context,
	hostname string,
) ([]string, error) {
	if len(r.resolvers) == 0 {
		return nil, errors.New("no hostname resolvers configured")
	}
	lookupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan hostnameResult, len(r.resolvers))
	for _, resolver := range r.resolvers {
		go func() {
			addresses, err := resolver.LookupHost(lookupCtx, hostname)
			results <- hostnameResult{addresses: addresses, err: err}
		}()
	}

	errorsSeen := make([]error, 0, len(r.resolvers))
	allNotFound := true
	for range r.resolvers {
		result := <-results
		if result.err == nil && len(result.addresses) > 0 {
			return result.addresses, nil
		}
		errorsSeen = append(errorsSeen, result.err)
		if !dnsNameNotFound(result.err) {
			allNotFound = false
		}
	}
	if allNotFound {
		return nil, &net.DNSError{
			Err:        "all resolvers returned no such host",
			Name:       hostname,
			IsNotFound: true,
		}
	}
	return nil, fmt.Errorf("hostname resolvers failed: %v", errorsSeen)
}

func (r fallbackHostnameResolver) LookupHost(
	ctx context.Context,
	hostname string,
) ([]string, error) {
	primaryTimeout := r.primaryTimeout
	if primaryTimeout <= 0 {
		primaryTimeout = quickDoHLookupTimeout
	}
	fallbackTimeout := r.fallbackTimeout
	if fallbackTimeout <= 0 {
		fallbackTimeout = quickHostLookupTimeout
	}

	primaryCtx, cancelPrimary := context.WithTimeout(ctx, primaryTimeout)
	addresses, err := r.primary.LookupHost(primaryCtx, hostname)
	cancelPrimary()
	if err == nil || dnsNameNotFound(err) {
		return addresses, err
	}

	fallbackCtx, cancelFallback := context.WithTimeout(ctx, fallbackTimeout)
	defer cancelFallback()
	return r.fallback.LookupHost(fallbackCtx, hostname)
}

func dnsNameNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}

type dohHostnameResolver struct {
	client   *http.Client
	endpoint string
}

type dohResponse struct {
	Status int         `json:"Status"`
	Answer []dohAnswer `json:"Answer"`
}

type dohAnswer struct {
	Type int    `json:"type"`
	Data string `json:"data"`
}

func (r dohHostnameResolver) LookupHost(
	ctx context.Context,
	hostname string,
) ([]string, error) {
	endpoint, err := url.Parse(r.endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse dns-over-HTTPS endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("name", hostname)
	query.Set("type", "A")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	req.Close = true
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	body, bodyErr := readMetricsBody(resp)
	if resp.StatusCode != http.StatusOK {
		if bodyErr != nil {
			return nil, errors.Join(
				fmt.Errorf("dns-over-HTTPS returned %s", resp.Status),
				bodyErr,
			)
		}
		return nil, fmt.Errorf("dns-over-HTTPS returned %s", resp.Status)
	}
	if bodyErr != nil {
		return nil, bodyErr
	}

	var result dohResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode dns-over-HTTPS response: %w", err)
	}
	if result.Status != 0 {
		return nil, &net.DNSError{
			Err:        fmt.Sprintf("DNS response status %d", result.Status),
			Name:       hostname,
			IsNotFound: result.Status == 3,
		}
	}
	addresses := make([]string, 0, len(result.Answer))
	for _, answer := range result.Answer {
		if answer.Type == 1 && net.ParseIP(answer.Data) != nil {
			addresses = append(addresses, answer.Data)
		}
	}
	if len(addresses) == 0 {
		return nil, &net.DNSError{
			Err:        "no address records",
			Name:       hostname,
			IsNotFound: true,
		}
	}
	return addresses, nil
}

func quickTunnelHostname(publicURL string) (string, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil {
		return "", fmt.Errorf("parse Quick Tunnel URL: %w", err)
	}
	hostname := parsed.Hostname()
	if hostname == "" {
		return "", fmt.Errorf("quick tunnel URL %q has no hostname", publicURL)
	}
	return hostname, nil
}

func (p *Process) waitQuickDNS(
	ctx context.Context,
	resolver hostnameResolver,
	hostname string,
) error {
	ticker := time.NewTicker(quickDNSPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		lookupCtx, cancel := context.WithTimeout(ctx, quickDNSLookupTimeout)
		addresses, err := resolver.LookupHost(lookupCtx, hostname)
		cancel()
		if err == nil && len(addresses) > 0 {
			return nil
		}
		if err == nil {
			err = errors.New("dns lookup returned no addresses")
		}
		lastErr = err
		if p.exited.Load() {
			return p.exitError("exited before its hostname became resolvable")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"quick tunnel hostname %q did not become resolvable: %w",
				hostname,
				errors.Join(ctx.Err(), lastErr),
			)
		case <-p.done:
			return p.exitError("exited before its hostname became resolvable")
		case <-ticker.C:
		}
	}
}

func (p *Process) Ready(ctx context.Context) error {
	p.mu.Lock()
	metricsURL := p.metricsURL
	p.mu.Unlock()
	if metricsURL == "" {
		return errors.New("metrics endpoint is not ready")
	}
	// Short request timeout so WaitReady loop stays responsive
	cctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, metricsURL+"/ready", nil)
	if err != nil {
		return err
	}
	resp, err := metricsClient.Do(req)
	if err != nil {
		return err
	}
	_, bodyErr := readMetricsBody(resp)
	if resp.StatusCode != http.StatusOK {
		if bodyErr != nil {
			return errors.Join(fmt.Errorf("not ready: %s", resp.Status), bodyErr)
		}
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	if bodyErr != nil {
		return bodyErr
	}
	return nil
}

func readMetricsBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, errors.New("metrics response has no body")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxMetricsResponseBytes+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read metrics response: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close metrics response: %w", closeErr)
	}
	if len(body) > maxMetricsResponseBytes {
		return nil, fmt.Errorf("metrics response exceeds %d bytes", maxMetricsResponseBytes)
	}
	return body, nil
}

func (p *Process) Stop(timeout time.Duration) error {
	if p.cmd.Process == nil {
		return p.outputError()
	}
	if p.exited.Load() {
		return p.waitForExit()
	}
	pid := p.cmd.Process.Pid
	if err := p.verifyIdentity(pid); err != nil {
		return errors.Join(err, p.waitForExit())
	}
	interruptErr := procutil.Interrupt(pid)
	if interruptErr != nil && !procutil.Alive(pid) {
		return p.waitForExit()
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-p.done:
		return p.outputError()
	case <-timer.C:
		if err := p.verifyIdentity(pid); err != nil {
			return errors.Join(err, p.waitForExit())
		}
		if err := procutil.Kill(pid); err != nil && procutil.Alive(pid) {
			return fmt.Errorf("stop cloudflared: %w", err)
		}
	}

	killTimer := time.NewTimer(2 * time.Second)
	defer killTimer.Stop()
	select {
	case <-p.done:
		return p.outputError()
	case <-killTimer.C:
		return fmt.Errorf("cloudflared process %d did not exit after kill", pid)
	}
}

func (p *Process) verifyIdentity(pid int) error {
	if !procutil.Alive(pid) {
		return nil
	}
	startTime, err := procutil.StartTime(pid)
	if err != nil {
		select {
		case <-p.done:
			return nil
		default:
		}
		if !procutil.Alive(pid) {
			return nil
		}
		return fmt.Errorf("verify cloudflared process identity: %w", err)
	}
	if startTime != p.startTime {
		return fmt.Errorf("cloudflared PID %d no longer belongs to the started process", pid)
	}
	return nil
}

func (p *Process) waitForExit() error {
	select {
	case <-p.done:
		return p.outputError()
	case <-time.After(2 * time.Second):
		return fmt.Errorf("cloudflared process did not exit")
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
