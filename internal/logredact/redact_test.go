package logredact

import "testing"

func TestRedact(t *testing.T) {
	t.Parallel()
	in := `Authorization: Bearer supersecrettoken123 TUNNEL_TOKEN=abc.def.ghi eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.sig`
	out := String(in)
	if contains(out, "supersecrettoken123") || contains(out, "abc.def.ghi") {
		t.Fatalf("not redacted: %s", out)
	}
	if !contains(out, "[REDACTED]") {
		t.Fatalf("expected redaction markers: %s", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
