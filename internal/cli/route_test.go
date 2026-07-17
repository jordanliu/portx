package cli

import (
	"strings"
	"testing"

	"portx/internal/leases"
)

func TestLeaseSelectorIncludesPathPrefix(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "root", path: "/", want: "api.example.dev/"},
		{name: "path", path: "/hooks", want: "api.example.dev/hooks"},
		{name: "empty path", want: "api.example.dev/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := leaseSelector(leases.Lease{
				Hostname:   "api.example.dev",
				PathPrefix: tc.path,
			})
			if got != tc.want {
				t.Fatalf("leaseSelector() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchingLeasesRejectsAmbiguousHostname(t *testing.T) {
	list := []leases.Lease{
		{ID: "root-id", Hostname: "api.example.dev", PathPrefix: "/"},
		{ID: "hooks-id", Hostname: "api.example.dev", PathPrefix: "/hooks"},
	}
	got := matchingLeases(list, "api.example.dev")
	if len(got) != 2 {
		t.Fatalf("matchingLeases() returned %d leases, want 2", len(got))
	}

	got = matchingLeases(list, "api.example.dev/hooks")
	if len(got) != 1 || got[0].ID != "hooks-id" {
		t.Fatalf("path selector matched %#v, want hooks-id", got)
	}
}

func TestMatchingLeasesAcceptsUniqueIDPrefix(t *testing.T) {
	list := []leases.Lease{
		{ID: "abcdef01", Hostname: "api.example.dev", PathPrefix: "/"},
		{ID: "12345678", Hostname: "web.example.dev", PathPrefix: "/"},
	}
	got := matchingLeases(list, "abc")
	if len(got) != 1 || got[0].ID != "abcdef01" {
		t.Fatalf("ID prefix matched %#v, want abcdef01", got)
	}
}

func TestAmbiguousLeaseErrorListsPathSelectors(t *testing.T) {
	err := ambiguousLeaseError("api.example.dev", []leases.Lease{
		{ID: "root-id", Hostname: "api.example.dev", PathPrefix: "/"},
		{ID: "hooks-id", Hostname: "api.example.dev", PathPrefix: "/hooks"},
	})
	message := err.Error()
	for _, want := range []string{"api.example.dev/", "api.example.dev/hooks", "root-id", "hooks-id"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not contain %q", message, want)
		}
	}
}
