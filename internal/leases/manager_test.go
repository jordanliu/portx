package leases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"portx/internal/procutil"
	"portx/internal/router"
)

func TestAcquireConflictAndReplace(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	l1, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3001",
		OwnerPID: 2, Ephemeral: true,
	})
	if err == nil {
		t.Fatal("expected conflict")
	}
	l2, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3001",
		OwnerPID: 2, Ephemeral: true, Replace: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if l2.ID == l1.ID {
		t.Fatal("expected new lease")
	}
	if _, ok := reg.Match("api.example.dev", "/"); !ok {
		t.Fatal("route missing")
	}
}

func TestRenewRelease(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	l, err := m.Acquire(AcquireRequest{
		Hostname: "web.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:5173",
		OwnerPID: os.Getpid(), Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Renew(l.ID, "bad", 0); err == nil {
		t.Fatal("expected bad token")
	}
	if _, err := m.Renew(l.ID, l.OwnerToken, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(l.ID, l.OwnerToken); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Match("web.example.dev", "/"); ok {
		t.Fatal("route should be gone")
	}
}

func TestAcquireReplacesExpiredLease(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", time.Hour)
	l1, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true, TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	l2, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3001",
		OwnerPID: 2, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if l2.ID == l1.ID {
		t.Fatal("expected new lease after expiry")
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("want 1 lease, got %d", got)
	}
	r, ok := reg.Match("api.example.dev", "/")
	if !ok {
		t.Fatal("route missing")
	}
	if r.Target.String() != "http://127.0.0.1:3001" {
		t.Fatalf("stale target: %s", r.Target)
	}
}

func TestAcquireReuseSameTargetRenews(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	l1, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := l1.ExpiresAt
	time.Sleep(5 * time.Millisecond)
	l2, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true, Reuse: true, TTL: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if l2.ID != l1.ID {
		t.Fatalf("want same lease id, got %s vs %s", l1.ID, l2.ID)
	}
	if l2.OwnerToken == l1.OwnerToken {
		t.Fatal("reuse should issue a distinct owner token")
	}
	if !l2.ExpiresAt.After(before) {
		t.Fatalf("expected renewed expiry: before=%v after=%v", before, l2.ExpiresAt)
	}
	if err := m.Release(l2.ID, l2.OwnerToken); err != nil {
		t.Fatal(err)
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("releasing one reused owner should retain lease, got %d", got)
	}
	if err := m.Release(l1.ID, l1.OwnerToken); err != nil {
		t.Fatal(err)
	}
	if got := len(m.List()); got != 0 {
		t.Fatalf("releasing all reused owners should remove lease, got %d", got)
	}
}

func TestAcquireReuseDifferentTargetConflicts(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	_, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3001",
		OwnerPID: 2, Ephemeral: true, Reuse: true,
	})
	if err == nil {
		t.Fatal("expected conflict on different target with Reuse")
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("want 1 lease, got %d", got)
	}
}

func TestAcquireReuseDifferentInsecureSettingConflicts(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	_, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "https://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "https://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true, Insecure: true, Reuse: true,
	})
	if err == nil {
		t.Fatal("expected conflict when Insecure changes")
	}
}

func TestReconcileReleasesDeadOwnerPID(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", time.Hour)
	_, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		// Unlikely to be a live process; Reconcile should drop the lease.
		OwnerPID: 999_999_999, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Reconcile()
	if got := len(m.List()); got != 0 {
		t.Fatalf("want 0 leases after dead-PID reconcile, got %d", got)
	}
	if _, ok := reg.Match("api.example.dev", "/"); ok {
		t.Fatal("route should be gone")
	}
}

func TestReconcileReportsCleanupErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := router.NewRegistry()
	m := NewManager(reg, dir, time.Hour)
	l, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 999_999_999, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	leasePath := filepath.Join(dir, l.ID+".json")
	if err := os.Remove(leasePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(leasePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leasePath, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := m.ReconcileWithError()
	if !changed {
		t.Fatal("expected reconciliation to remove the stale lease")
	}
	if err == nil {
		t.Fatal("expected persisted lease cleanup error")
	}
	if m.LastError() == nil {
		t.Fatal("expected LastError to expose reconciliation failure")
	}
}

func TestPersistOmitsOwnerToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := router.NewRegistry()
	m := NewManager(reg, dir, 45*time.Second)
	l, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: os.Getpid(), Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if l.OwnerToken == "" {
		t.Fatal("in-memory lease should have owner token")
	}
	path := filepath.Join(dir, l.ID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var disk Lease
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.OwnerToken != "" {
		t.Fatalf("disk lease must not store owner_token, got %q", disk.OwnerToken)
	}
	if disk.ID != l.ID || disk.Hostname != l.Hostname {
		t.Fatalf("disk lease mismatch: %+v", disk)
	}
}

func TestAcquirePersistenceFailureRollsBackRoute(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	storePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(storePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.storeDir = storePath

	_, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	})
	if err == nil {
		t.Fatal("expected persistence error")
	}
	if got := len(m.List()); got != 0 {
		t.Fatalf("failed acquire left %d leases in memory", got)
	}
	if _, ok := reg.Match("api.example.dev", "/"); ok {
		t.Fatal("failed acquire left a route in the registry")
	}
}

func TestRenewPersistenceFailureRestoresLease(t *testing.T) {
	t.Parallel()
	reg := router.NewRegistry()
	m := NewManager(reg, "", 45*time.Second)
	lease, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(storePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.storeDir = storePath

	if _, err := m.Renew(lease.ID, lease.OwnerToken, time.Hour); err == nil {
		t.Fatal("expected persistence error")
	}
	got := m.List()[0]
	if !got.ExpiresAt.Equal(lease.ExpiresAt) {
		t.Fatalf("renewal changed expiry after persistence failure: got %v, want %v", got.ExpiresAt, lease.ExpiresAt)
	}
}

func TestReleaseCleanupFailureIsReturned(t *testing.T) {
	t.Parallel()
	storeDir := t.TempDir()
	reg := router.NewRegistry()
	m := NewManager(reg, storeDir, 45*time.Second)
	lease, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(storeDir, lease.ID+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "in-use"), []byte("lease"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := m.Release(lease.ID, lease.OwnerToken); err == nil {
		t.Fatal("expected persisted lease cleanup error")
	}
	if got := len(m.List()); got != 1 {
		t.Fatalf("cleanup failure should preserve lease for retry, got %d leases", got)
	}
}

func TestLeasePersistenceLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(router.NewRegistry(), dir, time.Minute)
	if _, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev",
		Target:   "http://127.0.0.1:3000",
	}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			t.Fatalf("unexpected temporary lease file %q", entry.Name())
		}
	}
}

func TestReconcileChecksOwnerStartTime(t *testing.T) {
	startTime, err := procutil.StartTime(os.Getpid())
	if err != nil {
		t.Skipf("process start time unavailable: %v", err)
	}
	m := NewManager(router.NewRegistry(), "", time.Hour)
	if _, err := m.Acquire(AcquireRequest{
		Hostname:       "api.example.dev",
		Target:         "http://127.0.0.1:3000",
		OwnerPID:       os.Getpid(),
		OwnerStartTime: startTime + 1,
	}); err != nil {
		t.Fatal(err)
	}
	changed, err := m.ReconcileWithError()
	if err != nil || !changed || len(m.List()) != 0 {
		t.Fatalf("owner identity was not reconciled: changed=%v err=%v leases=%d", changed, err, len(m.List()))
	}
}

func TestManagerReportsLeasePurgeFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not prevent deletion on Windows")
	}
	t.Parallel()
	storeDir := t.TempDir()
	path := filepath.Join(storeDir, "stale.json")
	if err := os.WriteFile(path, []byte("lease"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(storeDir, 0o700)

	m := NewManager(router.NewRegistry(), storeDir, 45*time.Second)
	if _, err := m.Acquire(AcquireRequest{
		Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000",
		OwnerPID: 1, Ephemeral: true,
	}); err == nil {
		t.Fatal("expected lease store initialization error")
	}
}
