package origin

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"time"

	"portx/internal/apperr"
)

func Preflight(ctx context.Context, target *url.URL) error {
	host := target.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		port := "80"
		if target.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(target.Hostname(), port)
	}

	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return apperr.Wrap(apperr.ExitOrigin, fmt.Sprintf(
			"Could not connect to %q.\n\nStart the local service or pass --no-origin-check",
			target.String()), err)
	}
	_ = conn.Close()
	return nil
}
