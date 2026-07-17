package credentials

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileStoreSetIsAtomicAndSecure(t *testing.T) {
	dir := t.TempDir()
	store := &fileStore{dir: dir}
	key := "portx/test"

	if err := store.Set(key, "secret-value"); err != nil {
		t.Fatal(err)
	}

	path := store.path(key)
	assertPrivateCredentialPath(t, path, false)
	got, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-value" {
		t.Fatalf("credential = %q, want secret-value", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("temporary credential files remain: %v", entries)
	}
}

func TestFileStoreTightensExistingPermissions(t *testing.T) {
	dir := t.TempDir()
	store := &fileStore{dir: dir}
	path := store.path("portx/test")
	if err := os.WriteFile(path, []byte("secret-value"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Get("portx/test"); err != nil {
		t.Fatal(err)
	}
	assertPrivateCredentialPath(t, path, false)
}

func TestEnsureSecretDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := ensureSecretDir(dir); err != nil {
		t.Fatal(err)
	}
	assertPrivateCredentialPath(t, dir, true)
}

func TestFileStoreRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	store := &fileStore{dir: dir}
	path := store.path("portx/test")
	outside := filepath.Join(dir, "outside")
	if err := os.WriteFile(outside, []byte("not-a-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := store.Get("portx/test"); err == nil {
		t.Fatal("expected symlink credential to be rejected")
	}
}

func TestFileStoreBackendWarnsPlaintext(t *testing.T) {
	store := &fileStore{}
	if got := store.Backend(); got != "file (plaintext)" {
		t.Fatalf("backend = %q, want plaintext warning", got)
	}
}

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
