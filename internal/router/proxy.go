package router

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Proxy struct {
	registry  *Registry
	transport *http.Transport
}

func NewProxy(reg *Registry) *Proxy {
	return &Proxy{
		registry: reg,
		transport: &http.Transport{
			// Never route origin traffic through HTTP_PROXY (local dev tool).
			Proxy: nil,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: 0,
		},
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := normalizeHost(r.Host)
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	route, ok := p.registry.Match(host, path)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":    "route_not_active",
			"hostname": host,
		})
		return
	}

	reqID := uuid.NewString()
	rp := httputil.NewSingleHostReverseProxy(route.Target)
	tr := p.transport.Clone()
	if route.Target.Scheme == "https" {
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: route.TLS.InsecureSkipVerify, //nolint:gosec
			MinVersion:         tls.VersionTLS12,
		}
	}
	rp.Transport = tr
	rp.FlushInterval = 100 * time.Millisecond
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = route.Target.Host
		if route.HostHeader != "" {
			req.Host = route.HostHeader
		}
		// Strip spoofable inbound forwarding headers; set our own.
		req.Header.Del("X-Forwarded-For")
		req.Header.Del("X-Forwarded-Host")
		req.Header.Del("X-Forwarded-Proto")
		req.Header.Del("Forwarded")
		// Prefer Cloudflare client IP when present (edge → cloudflared → us).
		clientIP := r.Header.Get("CF-Connecting-IP")
		if clientIP == "" {
			clientIP, _, _ = net.SplitHostPort(r.RemoteAddr)
			if clientIP == "" {
				clientIP = r.RemoteAddr
			}
		}
		req.Header.Set("X-Forwarded-For", clientIP)
		req.Header.Set("X-Forwarded-Host", host)
		req.Header.Set("X-Forwarded-Proto", "https") // public managed routes are always HTTPS at edge
		req.Header.Set("X-PortX-Request-ID", reqID)
		req.Header.Set("Forwarded", "for="+clientIP+";host="+host+";proto=https")
	}
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		http.Error(rw, "bad gateway", http.StatusBadGateway)
	}
	rp.ServeHTTP(w, r)
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.TrimSuffix(host, ".")
}

func ListenAndServe(addr string, handler http.Handler) (*http.Server, net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	return srv, ln, nil
}
