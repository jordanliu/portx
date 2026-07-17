package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProjectSaveLoadValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ProjectFileName)
	pc := ProjectConfig{
		Version: 1,
		Project: "payments",
		Routes: map[string]ProjectRoute{
			"api": {Target: "http://127.0.0.1:3000", Hostname: "api.example.dev"},
			"web": {Target: "5173", Hostname: "web.example.dev"},
		},
	}
	if err := pc.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := SaveProject(path, pc); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("project config mode = %o, want 600", mode)
		}
	}
	loaded, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Project != "payments" || len(loaded.Routes) != 2 {
		t.Fatalf("%+v", loaded)
	}
}

func TestWriteFileAtomicRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	err := writeFileAtomic(path, []byte("replacement"), 0o600)
	if err == nil {
		t.Fatal("expected symlink replacement to fail")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "original" {
		t.Fatalf("symlink target changed: %q", b)
	}
}

func TestWriteFileAtomicLeavesNoPredictableTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := writeFileAtomic(path, []byte("config"), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.yaml" {
		t.Fatalf("unexpected files after atomic write: %+v", entries)
	}
}

func TestFindProjectFileWalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ProjectFileName)
	if err := os.WriteFile(path, []byte("version: 1\nproject: x\nroutes:\n  a:\n    target: \"3000\"\n    hostname: a.example.dev\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := FindProjectFile(sub)
	if err != nil {
		t.Fatal(err)
	}
	if found != path {
		t.Fatalf("got %s want %s", found, path)
	}
}

func TestLoadProjectRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), ProjectFileName)
	if err := os.WriteFile(path, []byte("version: 1\nproject: x\nunknown: true\nroutes: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProject(path); err == nil {
		t.Fatal("expected unknown project field to be rejected")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	home := t.TempDir()
	setTestUserDirectories(t, home)
	configHome, err := ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configHome, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configHome, "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected unknown config field to be rejected")
	}
}

func setTestUserDirectories(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("AppData", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("LocalAppData", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
}
