package rpc

import (
	"portx/internal/leases"
	"portx/internal/router"
)

const Version = 1

type Request struct {
	Version int            `json:"version"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
	Auth    string         `json:"auth,omitempty"`
}

type Response struct {
	OK     bool           `json:"ok"`
	Error  string         `json:"error,omitempty"`
	Code   int            `json:"code,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

type StreamEvent struct {
	Type  string              `json:"type"`
	Event router.RequestEvent `json:"event"`
}

type StatusResult struct {
	ProxyAddr      string `json:"proxy_addr"`
	TunnelRunning  bool   `json:"tunnel_running"`
	LeaseCount     int    `json:"lease_count"`
	CloudflaredPID int    `json:"cloudflared_pid,omitempty"`
	Profile        string `json:"profile,omitempty"`
	RequestEvents  bool   `json:"request_events"`
}

type AcquireParams struct {
	Hostname       string `json:"hostname"`
	PathPrefix     string `json:"path_prefix"`
	Target         string `json:"target"`
	HostHeader     string `json:"host_header"`
	OwnerPID       int    `json:"owner_pid"`
	OwnerStartTime int64  `json:"owner_start_time,omitempty"`
	Reuse          bool   `json:"reuse"`
	Replace        bool   `json:"replace"`
	Insecure       bool   `json:"insecure"`
}

func leaseToMap(l leases.Lease) map[string]any {
	return map[string]any{
		"id":               l.ID,
		"route_id":         l.RouteID,
		"hostname":         l.Hostname,
		"path_prefix":      l.PathPrefix,
		"target":           l.Target,
		"host_header":      l.HostHeader,
		"owner_pid":        l.OwnerPID,
		"owner_start_time": l.OwnerStartTime,
		"created_at":       l.CreatedAt,
		"renewed_at":       l.RenewedAt,
		"expires_at":       l.ExpiresAt,
		"ephemeral":        l.Ephemeral,
	}
}

// leaseToMapPrivate includes owner_token for the acquiring client only.
func leaseToMapPrivate(l leases.Lease) map[string]any {
	m := leaseToMap(l)
	m["owner_token"] = l.OwnerToken
	return m
}
