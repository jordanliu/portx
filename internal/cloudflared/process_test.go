//go:build !windows

package cloudflared

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartRedactsPersistedLogs(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cloudflared.log")
	secret := "super-secret-token"
	p, err := start(context.Background(), startConfig{
		Bin:     "/bin/sh",
		Args:    []string{"-c", "printf 'TUNNEL_TOKEN=" + secret + "\\n'"},
		LogPath: logPath,
	})
	if err != nil {
		t.Fatal(err)
	}

	waitProcess(t, p)
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("log mode = %o, want 600", got)
	}
	if !strings.Contains(string(data), secret) {
		t.Fatalf("log did not preserve developer output: %s", data)
	}
	if !strings.Contains(p.LogTail(0), secret) {
		t.Fatalf("log tail did not preserve developer output: %s", p.LogTail(0))
	}
}

func TestStartRejectsInvalidLogPath(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := start(context.Background(), startConfig{
		Bin:     "/bin/true",
		LogPath: filepath.Join(parent, "cloudflared.log"),
	})
	if err == nil {
		t.Fatal("expected invalid log path error")
	}
}

func TestStopForceKillsAfterInterruptTimeout(t *testing.T) {
	p, err := start(context.Background(), startConfig{
		Bin:  "/bin/sleep",
		Args: []string{"30"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := p.Stop(0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("process did not stop")
	}
}

func TestVerifyIdentityRejectsDifferentProcess(t *testing.T) {
	p := &Process{startTime: 1, done: make(chan struct{})}
	if err := p.verifyIdentity(os.Getpid()); err == nil {
		t.Fatal("verifyIdentity accepted a mismatched process start time")
	}
}

func TestConsumeDiscoversDefaultMetricsAddress(t *testing.T) {
	p := &Process{done: make(chan struct{})}
	p.consume(strings.NewReader("INF Starting metrics server on 127.0.0.1:20241/metrics\n"))

	p.mu.Lock()
	got := p.metricsURL
	p.mu.Unlock()
	if got != "http://127.0.0.1:20241" {
		t.Fatalf("metrics URL = %q, want default cloudflared address", got)
	}
}

func TestReadMetricsBodyRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxMetricsResponseBytes+1))
	}))
	defer server.Close()

	p := &Process{
		metricsURL: server.URL,
		done:       make(chan struct{}),
	}
	_, err := p.fetchQuickTunnel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("fetchQuickTunnel error = %v, want bounded-response error", err)
	}
}

func TestReadMetricsBodyChecksCloseError(t *testing.T) {
	resp := &http.Response{
		Body: &errorCloseReader{Reader: strings.NewReader("{}")},
	}
	_, err := readMetricsBody(resp)
	if err == nil || !strings.Contains(err.Error(), "close metrics response") {
		t.Fatalf("readMetricsBody error = %v, want close error", err)
	}
}

func TestStartUsesEphemeralMetricsPort(t *testing.T) {
	quick := quickArgs("http://127.0.0.1:1234", "")
	named := namedArgs("")
	for _, args := range [][]string{quick, named} {
		if !containsArgument(args, "127.0.0.1:0") {
			t.Fatalf("start arguments = %q, want ephemeral metrics address", args)
		}
	}
}

func containsArgument(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

type errorCloseReader struct {
	*strings.Reader
}

func (r *errorCloseReader) Close() error {
	return io.ErrClosedPipe
}

func waitProcess(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit")
	}
}
