package origin

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"portx/internal/apperr"
)

// SafeDialContext verifies the address selected by the OS resolver at the
// moment of connection, closing the DNS-rebinding gap between validation and
// the actual origin request.
func SafeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if err := validateRemoteAddress(conn.RemoteAddr()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

// ValidateTargetSafety allows loopback origins for local tunneling and rejects
// private, metadata, link-local, multicast, and unspecified destinations.
func ValidateTargetSafety(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return apperr.New(apperr.ExitInvalidArgs, "target host is required")
	}
	// block obvious metadata hostnames
	lower := strings.ToLower(host)
	if isMetadataHostname(lower) {
		return apperr.New(apperr.ExitInvalidArgs, "refusing to proxy to cloud metadata endpoints")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		// if host is already an IP
		if ip := net.ParseIP(host); ip != nil {
			return checkIP(ip)
		}
		// unresolved hostnames (e.g. offline) allowed; dial will fail later
		return nil
	}
	for _, ip := range ips {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

func checkIP(ip net.IP) error {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("invalid origin address %q", ip))
	}
	return checkAddr(addr)
}

func validateRemoteAddress(addr net.Addr) error {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		return checkIP(tcpAddr.IP)
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return fmt.Errorf("inspect origin address: %w", err)
	}
	if percent := strings.LastIndexByte(host, '%'); percent >= 0 {
		host = host[:percent]
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("parse origin address %q: %w", host, err)
	}
	return checkAddr(ip)
}

func checkAddr(addr netip.Addr) error {
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return nil
	}
	if addr.IsPrivate() {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing private origin address %q", addr))
	}
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing link-local target %q", addr))
	}
	if addr.IsUnspecified() || addr.IsMulticast() {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing non-routable origin address %q", addr))
	}
	if isSpecialUseAddress(addr) {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing special-use origin address %q", addr))
	}
	if isMetadataAddress(addr) {
		return apperr.New(apperr.ExitInvalidArgs, "refusing to proxy to cloud metadata endpoints")
	}
	return nil
}

func isSpecialUseAddress(addr netip.Addr) bool {
	specialUsePrefixes := []netip.Prefix{
		netip.MustParsePrefix("100.64.0.0/10"),   // shared address space
		netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
		netip.MustParsePrefix("192.0.2.0/24"),    // documentation
		netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 anycast
		netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
		netip.MustParsePrefix("198.51.100.0/24"), // documentation
		netip.MustParsePrefix("203.0.113.0/24"),  // documentation
	}
	for _, prefix := range specialUsePrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func isMetadataHostname(host string) bool {
	metadataNames := []string{
		"metadata.google.internal",
		"metadata.google.com",
		"instance-data.ec2.internal",
	}
	for _, name := range metadataNames {
		if host == name || strings.HasSuffix(host, "."+name) {
			return true
		}
	}
	return false
}

func isMetadataAddress(addr netip.Addr) bool {
	metadataAddresses := []string{
		"100.100.100.200", // Alibaba
		"169.254.169.254", // AWS/GCP/Azure
		"169.254.169.253", // AWS Route 53 resolver metadata
		"169.254.170.2",   // AWS ECS
	}
	for _, raw := range metadataAddresses {
		if addr == netip.MustParseAddr(raw) {
			return true
		}
	}
	return false
}
