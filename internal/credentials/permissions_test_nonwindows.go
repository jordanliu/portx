//go:build !windows

package credentials

import (
	"os"
	"testing"
)

func assertPrivateCredentialPath(t *testing.T, path string, wantDir bool) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.IsDir() != wantDir {
		t.Fatalf("credential path directory = %t, want %t", info.IsDir(), wantDir)
	}
	wantMode := os.FileMode(0o600)
	if wantDir {
		wantMode = 0o700
	}
	if got := info.Mode().Perm(); got != wantMode {
		t.Fatalf("credential path mode = %o, want %o", got, wantMode)
	}
}
