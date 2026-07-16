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
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Forwarded-Host", "evil.example")
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Forwarded", "for=1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "198.51.100.20")
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status %d", rr.Code)
	}
	xff := got.Get("X-Forwarded-For")
	if xff == "" || xff == "1.2.3.4" {
		t.Fatalf("X-Forwarded-For = %q (spoofed inbound should be stripped)", xff)
	}
	if !containsIP(xff, "198.51.100.20") {
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
