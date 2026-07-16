package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"portx/internal/apperr"
)

func (c Config) Validate() error {
	if c.Version != 1 {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("unsupported config version %d", c.Version))
	}
	if c.Defaults.ProxyPort <= 0 || c.Defaults.ProxyPort > 65535 {
		return apperr.New(apperr.ExitInvalidArgs, "defaults.proxy_port must be 1-65535")
	}
	if c.Defaults.BindAddress == "" {
		return apperr.New(apperr.ExitInvalidArgs, "defaults.bind_address is required")
	}
	if err := validateBindAddress(c.Defaults.BindAddress); err != nil {
		return err
	}
	for name, p := range c.Profiles {
		if err := p.Validate(name); err != nil {
			return err
		}
	}
	return nil
}

func validateBindAddress(addr string) error {
	switch addr {
	case "127.0.0.1", "::1", "localhost":
		return nil
	}

	allowNonLocal := os.Getenv("PORTX_ALLOW_NONLOCAL_BIND") == "1"
	ip := net.ParseIP(addr)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	if allowNonLocal {
		return nil
	}

	return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf(
		"defaults.bind_address %q must be loopback (127.0.0.1 or ::1)\n\nSet PORTX_ALLOW_NONLOCAL_BIND=1 to override (not recommended)",
		addr))
}

func (p Profile) Validate(name string) error {
	if p.Domain == "" && p.Wildcard == "" {
		return nil
	}
	if p.AccountID == "" {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("profile %q: account_id required", name))
	}
	if p.ZoneID == "" {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("profile %q: zone_id required", name))
	}
	if p.Wildcard == "" {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("profile %q: wildcard required", name))
	}
	if !strings.HasPrefix(p.Wildcard, "*.") {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("profile %q: wildcard must start with *.", name))
	}
	return nil
}

func (p Profile) IsConfigured() bool {
	return p.AccountID != "" && p.ZoneID != "" && p.TunnelID != "" && p.Wildcard != ""
}
