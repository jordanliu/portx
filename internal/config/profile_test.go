package config

import "testing"

func TestResolveProfile(t *testing.T) {
	t.Parallel()
	if got := ResolveProfile("", "", ""); got != "personal" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveProfile("", "proj", "def"); got != "proj" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveProfile("flag", "proj", "def"); got != "flag" {
		t.Fatalf("got %q", got)
	}
	if got := ResolveProfile("", "", "work"); got != "work" {
		t.Fatalf("got %q", got)
	}
}
