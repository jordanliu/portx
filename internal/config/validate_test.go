package config

import (
	"testing"
)

func TestValidateBindAddressRequiresLoopback(t *testing.T) {
	t.Setenv("PORTX_ALLOW_NONLOCAL_BIND", "")

	cfg := Default()
	cfg.Defaults.BindAddress = "0.0.0.0"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected non-loopback bind to fail")
	}

	cfg.Defaults.BindAddress = "127.0.0.1"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg.Defaults.BindAddress = "::1"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PORTX_ALLOW_NONLOCAL_BIND", "1")
	cfg.Defaults.BindAddress = "0.0.0.0"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("override should allow non-loopback: %v", err)
	}
}

func TestValidateProxyPort(t *testing.T) {
	cfg := Default()
	cfg.Defaults.ProxyPort = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid proxy port")
	}
	cfg.Defaults.ProxyPort = 4041
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}
