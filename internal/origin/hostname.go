package origin

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"portx/internal/apperr"
)

type PublicRoute struct {
	Hostname   string
	PathPrefix string
}

// ParsePublicURL parses --url values.
// Full:  api.example.dev, https://api.example.dev/webhooks
// Short: api  →  api.<namespace> when wildcard is *.example.dev
func ParsePublicURL(raw, wildcard string) (PublicRoute, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "--url is required for managed mode")
	}

	host := raw
	path := "/"
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return PublicRoute{}, apperr.Wrap(apperr.ExitInvalidArgs, "parse --url", err)
		}
		host = u.Host
		if u.Path != "" {
			path = u.Path
		}
	} else if i := strings.Index(raw, "/"); i >= 0 {
		host = raw[:i]
		path = raw[i:]
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "hostname is required")
	}
	if strings.Contains(host, ":") {
		return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "hostname must not include a port")
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}

	// Short form: single label → expand with configured wildcard suffix.
	// my-app + *.jx0.dev → my-app.jx0.dev
	if !strings.Contains(host, ".") {
		expanded, err := ExpandShortHostname(host, wildcard)
		if err != nil {
			return PublicRoute{}, err
		}
		host = expanded
	}

	if err := ValidateHostname(host, wildcard); err != nil {
		return PublicRoute{}, err
	}
	return PublicRoute{Hostname: host, PathPrefix: path}, nil
}

// ExpandShortHostname turns "my-app" into "my-app.example.dev" given wildcard "*.example.dev".
func ExpandShortHostname(label, wildcard string) (string, error) {
	wildcard = strings.ToLower(strings.TrimSpace(wildcard))
	if wildcard == "" {
		return "", apperr.New(apperr.ExitInvalidArgs, "managed hostnames require setup; run portx setup")
	}
	if !strings.HasPrefix(wildcard, "*.") {
		return "", apperr.New(apperr.ExitInvalidArgs, "wildcard must look like *.example.dev")
	}
	label = strings.ToLower(strings.TrimSpace(label))
	if label == "" || strings.Contains(label, ".") {
		return "", apperr.New(apperr.ExitInvalidArgs, "short --url must be a single DNS label (e.g. my-app)")
	}
	if strings.ContainsAny(label, " /\\:*?\"<>|") {
		return "", apperr.New(apperr.ExitInvalidArgs, "invalid hostname label")
	}
	// basic DNS label rules: alnum and hyphen, not start/end with hyphen
	for i, r := range label {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			return "", apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("invalid character in hostname label %q", label))
		}
		if (i == 0 || i == len(label)-1) && r == '-' {
			return "", apperr.New(apperr.ExitInvalidArgs, "hostname label cannot start or end with a hyphen")
		}
	}
	suffix := strings.TrimPrefix(wildcard, "*") // ".example.dev"
	return label + suffix, nil
}

func ValidateHostname(host, wildcard string) error {
	wildcard = strings.ToLower(strings.TrimSpace(wildcard))
	if wildcard == "" {
		return apperr.New(apperr.ExitInvalidArgs, "managed hostnames require setup; run portx setup")
	}
	if !strings.HasPrefix(wildcard, "*.") {
		return apperr.New(apperr.ExitInvalidArgs, "wildcard must look like *.example.dev")
	}
	suffix := strings.TrimPrefix(wildcard, "*")
	if !strings.HasSuffix(host, suffix) {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf(
			"hostname %q is outside namespace %s\n\nUse a full name (api%s) or short label (api).",
			host, wildcard, suffix))
	}
	label := strings.TrimSuffix(host, suffix)
	if label == "" || strings.Contains(label, ".") {
		return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf(
			"hostname %q must be a single label under %s\n\nExamples:  --url=api   or   --url=api%s",
			host, wildcard, suffix))
	}
	if strings.ContainsAny(label, " /\\") {
		return apperr.New(apperr.ExitInvalidArgs, "invalid hostname label")
	}
	return nil
}
