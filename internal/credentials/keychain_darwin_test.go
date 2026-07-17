//go:build darwin

package credentials

import "testing"

func TestShellQuote(t *testing.T) {
	if shellQuote("a'b") != `'a'\''b'` {
		t.Fatalf("%q", shellQuote("a'b"))
	}
}
