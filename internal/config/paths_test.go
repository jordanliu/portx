package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	t.Parallel()
	dir, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("empty config dir")
	}
}

func TestRuntimeDirFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	dir, err := RuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(dir)) != "portx" && filepath.Base(dir) != "runtime" {
		// either .../portx/runtime or under xdg
		if _, err := os.Stat(filepath.Dir(dir)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if dir == "" {
		t.Fatal("empty runtime dir")
	}
}
