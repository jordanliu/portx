package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestNewOriginProxyStripsForwardingHeaders(t *testing.T) {
	t.Parallel()
	var received http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer origin.Close()

	target, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := newOriginProxy(target, "", false)
	req := httptest.NewRequest(http.MethodGet, "http://quick.example.dev/", nil)
	req.Header.Set("Forwarded", "for=198.51.100.10")
	req.Header.Set("X-Forwarded-For", "198.51.100.10")
	req.Header.Set("X-Real-IP", "198.51.100.10")
	req.Header.Set("CF-Connecting-IP", "198.51.100.10")
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	for _, name := range []string{
		"Forwarded",
		"X-Forwarded-For",
		"X-Real-IP",
		"CF-Connecting-IP",
	} {
		if value := received.Get(name); value != "" {
			t.Fatalf("%s = %q, want empty", name, value)
		}
	}
	if received.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("X-Forwarded-Proto = %q, want https", received.Get("X-Forwarded-Proto"))
	}
	if received.Get("X-PortX-Request-ID") == "" {
		t.Fatal("missing X-PortX-Request-ID")
	}
}

func TestReportSavedRouteJSONUsesStderr(t *testing.T) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	defer func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
	}()

	reportSavedRoute(true, "api")
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 {
		t.Fatalf("JSON save status wrote to stdout: %q", stdout)
	}
	if !strings.Contains(string(stderr), "saved route") {
		t.Fatalf("stderr = %q, want save status", stderr)
	}
}
