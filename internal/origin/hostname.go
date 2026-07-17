package origin

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode"

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
		if u.Scheme != "http" && u.Scheme != "https" {
			return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "public URL scheme must be http or https")
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs,
				"public URLs must not include credentials, query parameters, or fragments")
		}
		host = u.Hostname()
		if u.Port() != "" {
			return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "public hostname must not include a port")
		}
		if u.Path != "" {
			path = u.Path
		}
	} else if i := strings.Index(raw, "/"); i >= 0 {
		host = raw[:i]
		path = raw[i:]
	}

	if _, _, err := net.SplitHostPort(host); err == nil {
		return PublicRoute{}, apperr.New(apperr.ExitInvalidArgs, "public hostname must not include a port")
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
	if err := ValidatePathPrefix(path); err != nil {
		return PublicRoute{}, err
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
	if err := ValidateDNSHostname(strings.TrimPrefix(suffix, ".")); err != nil {
		return apperr.New(apperr.ExitInvalidArgs, "wildcard must contain a valid DNS suffix")
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if err := ValidateDNSHostname(host); err != nil {
		return err
	}
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
	return nil
}

// ValidateDNSHostname validates a hostname without requiring a particular DNS namespace.
func ValidateDNSHostname(host string) error {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if host == "" || len(host) > 253 {
		return apperr.New(apperr.ExitInvalidArgs, "hostname must be 1-253 characters")
	}
	for _, label := range strings.Split(host, ".") {
		if err := validateDNSLabel(label); err != nil {
			return err
		}
	}
	return nil
}

// ValidateHostHeader validates an origin Host override, including an optional port.
func ValidateHostHeader(host string) error {
	if host == "" || strings.IndexFunc(host, unicode.IsControl) >= 0 || strings.ContainsAny(host, " \t/\\?#@") {
		return apperr.New(apperr.ExitInvalidArgs, "invalid host header")
	}

	hostname := host
	port := ""
	if strings.HasPrefix(host, "[") {
		end := strings.IndexByte(host, ']')
		if end < 0 {
			return apperr.New(apperr.ExitInvalidArgs, "invalid bracketed host header")
		}
		hostname = host[1:end]
		rest := host[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return apperr.New(apperr.ExitInvalidArgs, "invalid host header")
			}
			port = rest[1:]
		}
		if !isIPLiteral(hostname) || !strings.Contains(hostname, ":") {
			return apperr.New(apperr.ExitInvalidArgs, "bracketed host header must contain an IPv6 address")
		}
	} else if strings.Count(host, ":") == 1 {
		var err error
		hostname, port, err = net.SplitHostPort(host)
		if err != nil {
			return apperr.New(apperr.ExitInvalidArgs, "invalid host header port")
		}
	} else if strings.Contains(host, ":") {
		return apperr.New(apperr.ExitInvalidArgs, "IPv6 host headers must use brackets")
	}

	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return apperr.New(apperr.ExitInvalidArgs, "host header port must be 1-65535")
		}
	}
	if isIPLiteral(hostname) {
		return nil
	}
	return ValidateDNSHostname(hostname)
}

// ValidatePathPrefix validates the path used to select a managed route.
func ValidatePathPrefix(path string) error {
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return apperr.New(apperr.ExitInvalidArgs, "path prefix must start with /")
	}
	if strings.IndexFunc(path, unicode.IsControl) >= 0 || strings.ContainsAny(path, "\\?#") {
		return apperr.New(apperr.ExitInvalidArgs, "path prefix contains invalid characters")
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return apperr.New(apperr.ExitInvalidArgs, "path prefix must not contain . or .. segments")
		}
	}
	return nil
}

func validateDNSLabel(label string) error {
	if label == "" || len(label) > 63 {
		return apperr.New(apperr.ExitInvalidArgs, "hostname labels must be 1-63 characters")
	}
	for i, r := range label {
		isLetter := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLetter && !isDigit && r != '-' {
			return apperr.New(apperr.ExitInvalidArgs, fmt.Sprintf("invalid hostname label %q", label))
		}
		if (i == 0 || i == len(label)-1) && r == '-' {
			return apperr.New(apperr.ExitInvalidArgs, "hostname labels cannot start or end with a hyphen")
		}
	}
	return nil
}

func isIPLiteral(host string) bool {
	if strings.Contains(host, "%") {
		host = host[:strings.LastIndexByte(host, '%')]
	}
	_, err := netip.ParseAddr(host)
	return err == nil
}
