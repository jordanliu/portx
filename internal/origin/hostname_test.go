package origin

import "testing"

func TestParsePublicURL(t *testing.T) {
	t.Parallel()
	r, err := ParsePublicURL("api.example.dev", "*.example.dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "api.example.dev" || r.PathPrefix != "/" {
		t.Fatalf("%+v", r)
	}
	r, err = ParsePublicURL("https://api.example.dev/webhooks", "*.example.dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.PathPrefix != "/webhooks" {
		t.Fatalf("path %q", r.PathPrefix)
	}
}

func TestParsePublicURLShort(t *testing.T) {
	t.Parallel()
	r, err := ParsePublicURL("my-app", "*.jx0.dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "my-app.jx0.dev" {
		t.Fatalf("got %q", r.Hostname)
	}
	r, err = ParsePublicURL("api/webhooks", "*.example.dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "api.example.dev" || r.PathPrefix != "/webhooks" {
		t.Fatalf("%+v", r)
	}
}

func TestExpandShortHostname(t *testing.T) {
	t.Parallel()
	got, err := ExpandShortHostname("sample", "*.jx0.dev")
	if err != nil || got != "sample.jx0.dev" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ExpandShortHostname("-bad", "*.jx0.dev"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ExpandShortHostname("a.b", "*.jx0.dev"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateHostname(t *testing.T) {
	t.Parallel()
	if err := ValidateHostname("api.example.dev", "*.example.dev"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHostname("a.b.example.dev", "*.example.dev"); err == nil {
		t.Fatal("expected multi-level reject")
	}
	if err := ValidateHostname("example.dev", "*.example.dev"); err == nil {
		t.Fatal("expected apex reject")
	}
}
