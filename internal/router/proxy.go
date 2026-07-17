package router

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"portx/internal/httpx"
	"portx/internal/origin"
)

const (
	upstreamResponseHeaderTimeout = 30 * time.Second
)

var forwardingHeaders = []string{
	"Forwarded",
	"X-Forwarded-For",
	"X-Forwarded-Host",
	"X-Forwarded-Port",
	"X-Forwarded-Proto",
	"X-Real-IP",
	"CF-Connecting-IP",
}

type Proxy struct {
	registry  *Registry
	transport *http.Transport
	observer  RequestObserver
}

func NewProxy(reg *Registry, observers ...RequestObserver) *Proxy {
	var observer RequestObserver
	if len(observers) > 0 {
		observer = observers[0]
	}
	return &Proxy{
		registry: reg,
		observer: observer,
		transport: &http.Transport{
			// Never route origin traffic through HTTP_PROXY (local dev tool).
			Proxy:                 nil,
			DialContext:           origin.SafeDialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ResponseHeaderTimeout: upstreamResponseHeaderTimeout,
		},
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.observer != nil {
		ObserveHandler(http.HandlerFunc(p.serveHTTP), p.observer).ServeHTTP(w, r)
		return
	}
	p.serveHTTP(w, r)
}

func (p *Proxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
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
	setObservedRouteID(r, route.ID)

	reqID := uuid.NewString()
	rp := &httputil.ReverseProxy{}
	tr := p.transport.Clone()
	if route.Target.Scheme == "https" {
		tr.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: route.TLS.InsecureSkipVerify, //nolint:gosec
			MinVersion:         tls.VersionTLS12,
		}
	}
	rp.Transport = tr
	rp.FlushInterval = 100 * time.Millisecond
	rp.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode == http.StatusSwitchingProtocols {
			return nil
		}
		resp.Body = httpx.NewIdleTimeoutBody(
			resp.Body,
			httpx.ResponseBodyIdleLimit,
		)
		return nil
	}
	rp.Rewrite = func(proxyReq *httputil.ProxyRequest) {
		proxyReq.SetURL(route.Target)
		proxyReq.Out.Host = route.Target.Host
		if route.HostHeader != "" {
			proxyReq.Out.Host = route.HostHeader
		}
		stripForwardingHeaders(proxyReq.Out.Header)
		clientIP := requestClientIP(proxyReq.In)
		proxyReq.Out.Header.Set("X-Forwarded-For", clientIP)
		proxyReq.Out.Header.Set("X-Forwarded-Host", host)
		proxyReq.Out.Header.Set("X-Forwarded-Proto", "https")
		proxyReq.Out.Header.Set("X-PortX-Request-ID", reqID)
		proxyReq.Out.Header.Set(
			"Forwarded",
			"for="+formatForwardedFor(clientIP)+";host="+host+";proto=https",
		)
	}
	rp.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		http.Error(rw, "bad gateway", http.StatusBadGateway)
	}
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = httpx.NewIdleTimeoutBody(r.Body, httpx.RequestBodyIdleLimit)
		defer r.Body.Close()
	}
	rp.ServeHTTP(w, r)
}

func stripForwardingHeaders(header http.Header) {
	for _, name := range forwardingHeaders {
		header.Del(name)
	}
}

func requestClientIP(r *http.Request) string {
	peerIP := normalizeIP(remoteIP(r.RemoteAddr))
	cfIP := validPublicClientIP(r.Header.Get("CF-Connecting-IP"))
	if cfIP != "" && isLoopbackIP(peerIP) {
		return cfIP
	}
	if peerIP != "" {
		return peerIP
	}
	return "unknown"
}

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func isLoopbackIP(raw string) bool {
	addr, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	return err == nil && addr.IsLoopback()
}

func normalizeIP(raw string) string {
	addr, err := netip.ParseAddr(strings.Trim(raw, "[]"))
	if err != nil {
		return ""
	}
	return addr.String()
}

func validPublicClientIP(raw string) string {
	raw = strings.TrimSpace(raw)
	addr, err := netip.ParseAddr(raw)
	if err != nil || !addr.IsGlobalUnicast() {
		return ""
	}
	if addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return ""
	}
	if isClientIPSpecialUse(addr) {
		return ""
	}
	return addr.String()
}

func isClientIPSpecialUse(addr netip.Addr) bool {
	specialUsePrefixes := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func formatForwardedFor(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err == nil && addr.Is6() {
		return "[" + addr.String() + "]"
	}
	return ip
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
	srv := httpx.NewServer(handler)
	go func() { _ = srv.Serve(ln) }()
	return srv, ln, nil
}
