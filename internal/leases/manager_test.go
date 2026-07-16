package leases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	if !l2.ExpiresAt.After(before) {
		t.Fatalf("expected renewed expiry: before=%v after=%v", before, l2.ExpiresAt)
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
