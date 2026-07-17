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
	if err := ValidateTargetSafety(target); err != nil {
		return err
	}
	host := target.Host
	if target.Port() == "" {
		port := "80"
		if target.Scheme == "https" {
			port = "443"
		}
		host = net.JoinHostPort(target.Hostname(), port)
	}

	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := SafeDialContext(dialCtx, "tcp", host)
	if err != nil {
		return apperr.Wrap(apperr.ExitOrigin, fmt.Sprintf(
			"Could not connect to %q.\n\nStart the local service or pass --no-origin-check",
			target.String()), err)
	}
	_ = conn.Close()
	return nil
}
