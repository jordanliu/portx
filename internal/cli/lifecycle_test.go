package cli

import (
	"os"
	"path/filepath"
	"testing"

	"portx/internal/apperr"
	"portx/internal/cloudflare"
	"portx/internal/credentials"
)

func TestReadPIDRequiresProcessIdentity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.pid")
	if err := os.WriteFile(legacyPath, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPID(legacyPath); err == nil {
		t.Fatal("readPID accepted a legacy PID-only file")
	}
	legacyPID, err := readLegacyPID(legacyPath)
	if err != nil {
		t.Fatalf("readLegacyPID rejected a valid legacy PID: %v", err)
	}
	if legacyPID != 1234 {
		t.Fatalf("readLegacyPID returned %d, want 1234", legacyPID)
	}

	validPath := filepath.Join(dir, "process.pid")
	data := []byte(`{"pid":1234,"executable":"/usr/local/bin/portx","kind":"portxd","start_time":123456789}`)
	if err := os.WriteFile(validPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	record, err := readPID(validPath)
	if err != nil {
		t.Fatalf("readPID returned error for structured record: %v", err)
	}
	if record.PID != 1234 || record.Executable != "/usr/local/bin/portx" ||
		record.Kind != "portxd" || record.StartTime != 123456789 {
		t.Fatalf("unexpected process record: %+v", record)
	}
}

func TestValidateLegacyProcessRejectsUnrelatedProcess(t *testing.T) {
	t.Parallel()

	if err := validateLegacyProcess(os.Getpid(), "cloudflared"); err == nil {
		t.Fatal("validateLegacyProcess accepted the test process as cloudflared")
	}
}

func TestProcessStartTime(t *testing.T) {
	t.Parallel()

	startTime, err := processStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("processStartTime returned error: %v", err)
	}
	if startTime <= 0 {
		t.Fatalf("processStartTime returned %d", startTime)
	}
}

func TestTunnelReusable(t *testing.T) {
	t.Parallel()

	deletedAt := "2026-07-16T00:00:00Z"
	cases := []struct {
		name   string
		tunnel cloudflare.Tunnel
		want   bool
	}{
		{name: "missing id", tunnel: cloudflare.Tunnel{}, want: false},
		{name: "deleted timestamp", tunnel: cloudflare.Tunnel{ID: "tunnel", DeletedAt: &deletedAt}, want: false},
		{name: "deleted status", tunnel: cloudflare.Tunnel{ID: "tunnel", Status: "deleted"}, want: false},
		{name: "inactive is startable", tunnel: cloudflare.Tunnel{ID: "tunnel", Status: "inactive"}, want: true},
		{name: "healthy", tunnel: cloudflare.Tunnel{ID: "tunnel", Status: "healthy"}, want: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tunnelReusable(tc.tunnel); got != tc.want {
				t.Fatalf("tunnelReusable(%+v) = %v, want %v", tc.tunnel, got, tc.want)
			}
		})
	}
}

func TestSnapshotCredentialTreatsMissingCredentialAsAbsent(t *testing.T) {
	t.Parallel()

	store := &testCredentialStore{err: apperr.New(apperr.ExitAuth, "credential not found")}
	snapshot, err := snapshotCredential(store, "missing")
	if err != nil {
		t.Fatalf("snapshotCredential returned error: %v", err)
	}
	if snapshot.present || snapshot.value != "" {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestCoordinateRuntimeSucceedsWithoutDaemon(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	if err := coordinateRuntime(false); err != nil {
		t.Fatalf("coordinateRuntime returned error without daemon: %v", err)
	}
}

type testCredentialStore struct {
	value string
	err   error
}

func (s *testCredentialStore) Set(key, value string) error {
	s.value = value
	return nil
}

func (s *testCredentialStore) Get(key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.value, nil
}

func (s *testCredentialStore) Delete(key string) error {
	s.value = ""
	return nil
}

func (s *testCredentialStore) Backend() string {
	return "test"
}

var _ credentials.Store = (*testCredentialStore)(nil)
