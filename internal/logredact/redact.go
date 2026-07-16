package logredact

import (
	"regexp"
	"strings"
)

var (
	bearerRe = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9\-\._~\+\/]+=*`)
	tokenRe  = regexp.MustCompile(`(?i)(tunnel[_-]?token|api[_-]?token|authorization)(["\s:=]+)([^\s"',}]+)`)
	jwtish   = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)
)

func String(s string) string {
	s = bearerRe.ReplaceAllString(s, "${1}[REDACTED]")
	s = tokenRe.ReplaceAllString(s, "${1}${2}[REDACTED]")
	s = jwtish.ReplaceAllString(s, "[REDACTED_JWT]")
	// TUNNEL_TOKEN=...
	if i := strings.Index(strings.ToUpper(s), "TUNNEL_TOKEN="); i >= 0 {
		rest := s[i+len("TUNNEL_TOKEN="):]
		end := len(rest)
		for j, r := range rest {
			if r == ' ' || r == '\n' || r == '"' || r == '\'' {
				end = j
				break
			}
		}
		s = s[:i] + "TUNNEL_TOKEN=[REDACTED]" + rest[end:]
	}
	return s
}
