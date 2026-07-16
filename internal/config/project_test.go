package config

import (
	"os"
	"path/filepath"
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
	loaded, err := LoadProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Project != "payments" || len(loaded.Routes) != 2 {
		t.Fatalf("%+v", loaded)
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
