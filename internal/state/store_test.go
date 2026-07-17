package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDataClonesProfilesMap(t *testing.T) {
	s, err := Open()
	if err != nil {
		// Open uses real config paths; use Replace on a fresh store via temp if needed.
		// Prefer in-memory construction:
		s = &Store{data: Data{Version: 1, Profiles: map[string]ProfileState{
			"personal": {TunnelID: "t1"},
		}}}
	}
	_ = s
	s = &Store{data: Data{
		Version: 1,
		Profiles: map[string]ProfileState{
			"personal": {TunnelID: "t1"},
		},
	}}

	snap := s.Data()
	snap.Profiles["personal"] = ProfileState{TunnelID: "mutated"}
	snap.Profiles["other"] = ProfileState{TunnelID: "x"}

	again := s.Data()
	if again.Profiles["personal"].TunnelID != "t1" {
		t.Fatalf("internal map mutated: %+v", again.Profiles["personal"])
	}
	if _, ok := again.Profiles["other"]; ok {
		t.Fatal("caller should not insert into store map")
	}
}

func TestPersistUsesSecureAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	s := &Store{
		path: path,
		data: Data{Version: 1, Profiles: map[string]ProfileState{}},
	}
	if err := s.Replace(Data{Version: 1, Profiles: map[string]ProfileState{
		"personal": {TunnelID: "t1"},
	}}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state mode = %o, want 600", mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("unexpected files after atomic write: %+v", entries)
	}
}

func TestPersistRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}

	s := &Store{
		path: path,
		data: Data{Version: 1, Profiles: map[string]ProfileState{}},
	}
	if err := s.Replace(Data{Version: 1, Profiles: map[string]ProfileState{}}); err == nil {
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
