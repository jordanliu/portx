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

func TestParsePublicURLRejectsPortInShortForm(t *testing.T) {
	if _, err := ParsePublicURL("api:8080", "*.example.dev"); err == nil {
		t.Fatal("expected short-form hostname with a port to be rejected")
	}
}

func TestParsePublicURLRejectsUnsafeComponents(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"https://user@example.dev",
		"https://api.example.dev:443",
		"https://api.example.dev/?token=secret",
		"https://api.example.dev/#fragment",
		"ftp://api.example.dev",
		"api.example.dev/../admin",
	} {
		if _, err := ParsePublicURL(raw, "*.example.dev"); err == nil {
			t.Errorf("ParsePublicURL(%q) succeeded; want error", raw)
		}
	}
}

func TestValidateHostHeader(t *testing.T) {
	t.Parallel()
	for _, host := range []string{
		"localhost",
		"localhost:3000",
		"origin.internal",
		"127.0.0.1:8080",
		"[::1]:8080",
	} {
		if err := ValidateHostHeader(host); err != nil {
			t.Errorf("ValidateHostHeader(%q): %v", host, err)
		}
	}
	for _, host := range []string{
		"evil.example/route",
		"evil.example\r\nX-Injected: true",
		"-bad.example",
		"[::1",
		"example.com:0",
		"2001:db8::1",
	} {
		if err := ValidateHostHeader(host); err == nil {
			t.Errorf("ValidateHostHeader(%q) succeeded; want error", host)
		}
	}
}

func TestValidatePathPrefix(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"", "/", "/webhooks", "/api/v1"} {
		if err := ValidatePathPrefix(path); err != nil {
			t.Errorf("ValidatePathPrefix(%q): %v", path, err)
		}
	}
	for _, path := range []string{"webhooks", "/../admin", "/api\\v1", "/api?token=secret", "/api\x00"} {
		if err := ValidatePathPrefix(path); err == nil {
			t.Errorf("ValidatePathPrefix(%q) succeeded; want error", path)
		}
	}
}
