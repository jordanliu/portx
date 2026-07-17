package origin

import (
	"net"
	"net/netip"
	"net/url"
	"testing"
)

func TestValidateTargetSafetyBlocksMetadata(t *testing.T) {
	t.Parallel()
	u, _ := url.Parse("http://169.254.169.254/latest/meta-data")
	if err := ValidateTargetSafety(u); err == nil {
		t.Fatal("expected block")
	}
	u, _ = url.Parse("http://127.0.0.1:3000")
	if err := ValidateTargetSafety(u); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTargetSafetyBlocksPrivateAndMetadataTargets(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://10.0.0.1:3000",
		"http://172.16.0.1:3000",
		"http://192.168.1.1:3000",
		"http://[fd00::1]:3000",
		"http://metadata.google.internal/",
		"http://100.64.0.1:3000",
		"http://198.18.0.1:3000",
		"http://203.0.113.10:3000",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateTargetSafety(u); err == nil {
			t.Errorf("ValidateTargetSafety(%q) succeeded; want error", raw)
		}
	}
}

func TestCheckIPAllowsLoopbackAndRejectsIPv6ZoneAddress(t *testing.T) {
	t.Parallel()
	if err := checkIP(net.ParseIP("127.0.0.1")); err != nil {
		t.Fatalf("loopback rejected: %v", err)
	}
	zoneAddr := &net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 8080, Zone: "en0"}
	if err := validateRemoteAddress(zoneAddr); err == nil {
		t.Fatal("IPv6 link-local address with zone was accepted")
	}
	if err := checkAddr(netip.MustParseAddr("::1")); err != nil {
		t.Fatalf("IPv6 loopback rejected: %v", err)
	}
}
