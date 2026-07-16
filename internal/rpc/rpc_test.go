package rpc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"portx/internal/leases"
	"portx/internal/router"
)

type testHandler struct {
	m *leases.Manager
}

func (h *testHandler) GetStatus() (StatusResult, error) {
	return StatusResult{ProxyAddr: "127.0.0.1:4041", LeaseCount: len(h.m.List())}, nil
}
func (h *testHandler) AcquireLease(p AcquireParams) (leases.Lease, error) {
	return h.m.Acquire(leases.AcquireRequest{
		Hostname: p.Hostname, PathPrefix: p.PathPrefix, Target: p.Target,
		OwnerPID: p.OwnerPID, Ephemeral: true, Reuse: p.Reuse, Replace: p.Replace,
	})
}
func (h *testHandler) RenewLease(id, token string) (leases.Lease, error) {
	return h.m.Renew(id, token, 45*time.Second)
}
func (h *testHandler) ReleaseLease(id, token string) error { return h.m.Release(id, token) }
func (h *testHandler) ForceRelease(id string) error         { return h.m.ForceRelease(id) }
func (h *testHandler) ListLeases() ([]leases.Lease, error)  { return h.m.List(), nil }
func (h *testHandler) StartTunnel() error                   { return nil }
func (h *testHandler) StopTunnel() error                    { return nil }

func TestRPCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "portxd.sock")
	reg := router.NewRegistry()
	m := leases.NewManager(reg, "", 45*time.Second)
	srv := NewServer(&testHandler{m: m})
	go func() { _ = srv.Serve(sock) }()
	defer srv.Close()

	deadline := time.Now().Add(2 * time.Second)
	var c *Client
	var err error
	for time.Now().Before(deadline) {
		c, err = Dial(sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	st, err := c.GetStatus()
	if err != nil || st.ProxyAddr == "" {
		t.Fatalf("%+v %v", st, err)
	}
	l, err := c.AcquireLease(AcquireParams{Hostname: "api.example.dev", PathPrefix: "/", Target: "http://127.0.0.1:3000", OwnerPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.RenewLease(l.ID, l.OwnerToken); err != nil {
		t.Fatal(err)
	}
	list, err := c.ListLeases()
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	if list[0].OwnerToken != "" {
		t.Fatal("ListLeases must not return owner_token")
	}
	if l.OwnerToken == "" {
		t.Fatal("AcquireLease must return owner_token")
	}
	if err := c.ReleaseLease(l.ID, l.OwnerToken); err != nil {
		t.Fatal(err)
	}
}


