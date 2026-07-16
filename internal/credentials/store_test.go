package credentials

import (
	"runtime"
	"testing"
)

func TestOSStoreRoundTrip(t *testing.T) {
	if useFileStore() {
		t.Skip("PORTX_CREDENTIALS_FILE set")
	}
	s, err := Open()
	if err != nil {
		t.Skipf("credential store unavailable: %v", err)
	}
	t.Logf("backend=%s goos=%s", s.Backend(), runtime.GOOS)

	key := APITokenKey("_test_portx")
	secret := "test-secret-value-xyz-not-real"
	if err := s.Set(key, secret); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(key) })

	got, err := s.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != secret {
		t.Fatalf("got %q", got)
	}
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(key); err == nil {
		t.Fatal("expected missing after delete")
	}
}

func TestShellQuote(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	if shellQuote("a'b") != `'a'\''b'` {
		t.Fatalf("%q", shellQuote("a'b"))
	}
}
