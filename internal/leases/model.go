package leases

import (
	"time"
)

type Lease struct {
	ID         string    `json:"id"`
	RouteID    string    `json:"route_id"`
	Hostname   string    `json:"hostname"`
	PathPrefix string    `json:"path_prefix"`
	Target     string    `json:"target"`
	HostHeader string    `json:"host_header,omitempty"`
	OwnerPID   int       `json:"owner_pid"`
	OwnerToken string    `json:"owner_token"`
	CreatedAt  time.Time `json:"created_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Ephemeral  bool      `json:"ephemeral"`
	Insecure   bool      `json:"insecure,omitempty"`
}

type AcquireRequest struct {
	Hostname   string
	PathPrefix string
	Target     string
	HostHeader string
	OwnerPID   int
	Ephemeral  bool
	Insecure   bool
	Reuse      bool
	Replace    bool
	TTL        time.Duration
}
