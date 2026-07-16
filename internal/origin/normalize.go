package origin

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"portx/internal/apperr"
)

func Normalize(target string) (*url.URL, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, apperr.New(apperr.ExitInvalidArgs, "target is required")
	}

	if port, err := strconv.Atoi(target); err == nil {
		if port <= 0 || port > 65535 {
			return nil, apperr.New(apperr.ExitInvalidArgs, "port must be 1-65535")
		}
		return &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}, nil
	}

	if !strings.Contains(target, "://") {
		isLoopbackHostPort := strings.HasPrefix(target, "localhost:") ||
			strings.HasPrefix(target, "127.0.0.1:") ||
			strings.HasPrefix(target, "[::1]:")
		if isLoopbackHostPort {
			target = "http://" + target
		} else if host, port, err := net.SplitHostPort(target); err == nil && host != "" && port != "" {
			target = "http://" + net.JoinHostPort(host, port)
		} else {
			return nil, apperr.New(apperr.ExitInvalidArgs,
				fmt.Sprintf("invalid target %q; use a port, host:port, or URL", target))
		}
	}

	u, err := url.Parse(target)
	if err != nil {
		return nil, apperr.Wrap(apperr.ExitInvalidArgs, "parse target", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("unsupported scheme %q; use http or https", u.Scheme))
	}
	if u.Host == "" {
		return nil, apperr.New(apperr.ExitInvalidArgs, "target host is required")
	}
	return u, nil
}
