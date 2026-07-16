package origin

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"portx/internal/apperr"
)

// ValidateTargetSafety rejects metadata and link-local destinations.
func ValidateTargetSafety(u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return apperr.New(apperr.ExitInvalidArgs, "target host is required")
	}
	// block obvious metadata hostnames
	lower := strings.ToLower(host)
	if lower == "metadata.google.internal" || strings.HasSuffix(lower, ".metadata.google.internal") {
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
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing link-local target %q", ip))
	}
	// AWS/GCP/Azure metadata
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("169.254.169.253")) {
		return apperr.New(apperr.ExitInvalidArgs, "refusing to proxy to cloud metadata endpoints")
	}
	if ip4 := ip.To4(); ip4 != nil {
		// 169.254.0.0/16
		if ip4[0] == 169 && ip4[1] == 254 {
			return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("refusing link-local target %q", ip))
		}
	}
	return nil
}
