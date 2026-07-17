package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenDaemonLogEnforcesPermissions(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "portxd.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := openDaemonLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("log permissions = %o, want 600", got)
		}
	}
}

func TestOpenDaemonLogRejectsSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	path := filepath.Join(dir, "portxd.log")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := openDaemonLog(path); err == nil {
		t.Fatal("openDaemonLog accepted a symlink")
	}
}
