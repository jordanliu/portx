package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func containsIP(header, ip string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.TrimSpace(part) == ip {
			return true
		}
	}
	return false
}

func TestProxyForwards(t *testing.T) {
	t.Parallel()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PortX-Request-ID") == "" {
			t.Error("missing request id")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer origin.Close()

	u, _ := url.Parse(origin.URL)
	reg := NewRegistry()
	_ = reg.Add(Route{
		ID: "1", Hostname: "api.example.dev", PathPrefix: "/",
		Target: u, CreatedAt: time.Now(),
	})
	p := NewProxy(reg)
	req := httptest.NewRequest(http.MethodGet, "http://api.example.dev/hello", nil)
	req.Host = "api.example.dev"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	if string(body) != "ok" {
		t.Fatalf("body %q", body)
	}
}

func TestProxyInactive(t *testing.T) {
	t.Parallel()
	p := NewProxy(NewRegistry())
	req := httptest.NewRequest(http.MethodGet, "http://missing.example.dev/", nil)
	req.Host = "missing.example.dev"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Fatalf("status %d", rr.Code)
	}
}

func TestProxySetsAndStripsForwardingHeaders(t *testing.T) {
	t.Parallel()
	var got http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		if r.Host != "origin.internal" {
			t.Errorf("Host = %q, want origin.internal", r.Host)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer origin.Close()

	u, _ := url.Parse(origin.URL)
	reg := NewRegistry()
	_ = reg.Add(Route{
		ID:         "1",
		Hostname:   "api.example.dev",
		PathPrefix: "/",
		Target:     u,
		HostHeader: "origin.internal",
		CreatedAt:  time.Now(),
	})
	p := NewProxy(reg)
	req := httptest.NewRequest(http.MethodGet, "http://api.example.dev/", nil)
	req.Host = "api.example.dev"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Forwarded", "for=1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "8.8.8.8")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	xff := got.Get("X-Forwarded-For")
	if xff == "" || xff == "1.2.3.4" {
		t.Fatalf("X-Forwarded-For = %q (spoofed inbound should be stripped)", xff)
	}
	if !containsIP(xff, "8.8.8.8") {
		t.Fatalf("X-Forwarded-For = %q, want CF-Connecting-IP present", xff)
	}
	if got.Get("X-Forwarded-Host") != "api.example.dev" {
		t.Fatalf("X-Forwarded-Host = %q", got.Get("X-Forwarded-Host"))
	}
	if got.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("X-Forwarded-Proto = %q", got.Get("X-Forwarded-Proto"))
	}
	if got.Get("X-PortX-Request-ID") == "" {
		t.Fatal("missing X-PortX-Request-ID")
	}
}

func TestProxyIgnoresUntrustedOrInvalidForwardingIP(t *testing.T) {
	t.Parallel()
	var got http.Header
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer origin.Close()

	u, _ := url.Parse(origin.URL)
	reg := NewRegistry()
	_ = reg.Add(Route{
		ID:         "1",
		Hostname:   "api.example.dev",
		PathPrefix: "/",
		Target:     u,
		CreatedAt:  time.Now(),
	})
	p := NewProxy(reg)
	req := httptest.NewRequest(http.MethodGet, "http://api.example.dev/", nil)
	req.Host = "api.example.dev"
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("CF-Connecting-IP", "not-an-ip")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if got.Get("X-Forwarded-For") != "127.0.0.1" {
		t.Fatalf("X-Forwarded-For = %q, want local peer", got.Get("X-Forwarded-For"))
	}

	req = httptest.NewRequest(http.MethodGet, "http://api.example.dev/", nil)
	req.Host = "api.example.dev"
	req.RemoteAddr = "192.0.2.10:12345"
	req.Header.Set("CF-Connecting-IP", "8.8.8.8")
	rr = httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if got.Get("X-Forwarded-For") != "192.0.2.10" {
		t.Fatalf("X-Forwarded-For = %q, want untrusted peer", got.Get("X-Forwarded-For"))
	}
}

func TestListenAndServeSetsSafeHTTPDefaults(t *testing.T) {
	t.Parallel()
	srv, ln, err := ListenAndServe("127.0.0.1:0", http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer ln.Close()
	if srv.MaxHeaderBytes != 32<<10 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", srv.MaxHeaderBytes, 32<<10)
	}
	if srv.ReadHeaderTimeout != 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s, want 30s", srv.ReadHeaderTimeout)
	}
	if srv.IdleTimeout != 90*time.Second {
		t.Fatalf("IdleTimeout = %s, want 90s", srv.IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 for streaming responses", srv.WriteTimeout)
	}
}
