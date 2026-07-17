package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveBrowserCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cloudflared")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	files := []string{
		"cert.pem",
		"cert.pem.bak.20260716-120000",
		"config.yml",
	}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveBrowserCredentials(); err != nil {
		t.Fatal(err)
	}
	for _, name := range files[:2] {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("credential remnant %q was not removed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, files[2])); err != nil {
		t.Fatalf("unrelated cloudflared file was removed: %v", err)
	}
}

func TestRemoveBrowserCredentialsRejectsSymlinkedDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, ".cloudflared")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := RemoveBrowserCredentials(); err == nil {
		t.Fatal("expected symlinked cloudflared directory to be rejected")
	}
}
