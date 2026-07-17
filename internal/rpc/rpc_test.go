package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"portx/internal/leases"
	"portx/internal/router"
)

type testHandler struct {
	m *leases.Manager
}

type blockingContextHandler struct {
	*testHandler
	started        chan struct{}
	acquireStarted chan struct{}
}

func (h *blockingContextHandler) StartTunnelContext(ctx context.Context) error {
	close(h.started)
	<-ctx.Done()
	return ctx.Err()
}

func (h *blockingContextHandler) AcquireLeaseContext(ctx context.Context, _ AcquireParams) (leases.Lease, error) {
	close(h.acquireStarted)
	<-ctx.Done()
	return leases.Lease{}, ctx.Err()
}

func (h *blockingContextHandler) StopTunnelContext(ctx context.Context) error {
	return ctx.Err()
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
func (h *testHandler) ForceRelease(id string) error        { return h.m.ForceRelease(id) }
func (h *testHandler) ListLeases() ([]leases.Lease, error) { return h.m.List(), nil }
func (h *testHandler) StartTunnel() error                  { return nil }
func (h *testHandler) StopTunnel() error                   { return nil }

func TestRPCRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
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
	l, err := c.AcquireLease(AcquireParams{
		Hostname:   "api.example.dev",
		PathPrefix: "/",
		Target:     "http://127.0.0.1:3000",
		OwnerPID:   os.Getpid(),
	})
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

func TestRPCRejectsUnsafeAcquireParams(t *testing.T) {
	t.Parallel()
	m := leases.NewManager(router.NewRegistry(), "", 45*time.Second)
	srv := NewServer(&testHandler{m: m})
	for _, params := range []map[string]any{
		{"hostname": "api.example.dev", "path_prefix": "/../admin"},
		{"hostname": "api.example.dev", "host_header": "origin.example/evil"},
		{"hostname": "bad host.example"},
	} {
		resp := srv.dispatch(Request{Version: Version, Method: "AcquireLease", Params: params})
		if resp.OK || resp.Code == 0 {
			t.Errorf("dispatch(%v) = %+v; want validation error", params, resp)
		}
	}
}

func TestServerCloseCancelsAndDrainsContextHandler(t *testing.T) {
	h := &blockingContextHandler{
		testHandler:    &testHandler{m: leases.NewManager(router.NewRegistry(), "", 45*time.Second)},
		started:        make(chan struct{}),
		acquireStarted: make(chan struct{}),
	}
	srv := NewServer(h)
	responses := make(chan Response, 1)
	srv.handlers.Add(1)
	go func() {
		defer srv.handlers.Done()
		responses <- srv.dispatchContext(srv.ctx, Request{Version: Version, Method: "StartTunnel"})
	}()

	select {
	case <-h.started:
	case <-time.After(time.Second):
		t.Fatal("context handler did not start")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case resp := <-responses:
		if resp.OK {
			t.Fatal("canceled handler returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("Close returned before handler drained")
	}
}

func TestServerCloseCancelsAndDrainsAcquireContextHandler(t *testing.T) {
	h := &blockingContextHandler{
		testHandler:    &testHandler{m: leases.NewManager(router.NewRegistry(), "", 45*time.Second)},
		started:        make(chan struct{}),
		acquireStarted: make(chan struct{}),
	}
	srv := NewServer(h)
	srv.handlers.Add(1)
	responses := make(chan Response, 1)
	go func() {
		defer srv.handlers.Done()
		responses <- srv.dispatchContext(srv.ctx, Request{
			Version: Version,
			Method:  "AcquireLease",
			Params: map[string]any{
				"hostname":  "api.example.dev",
				"target":    "http://127.0.0.1:3000",
				"owner_pid": os.Getpid(),
			},
		})
	}()

	select {
	case <-h.acquireStarted:
	case <-time.After(time.Second):
		t.Fatal("context acquire handler did not start")
	}
	if err := srv.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	select {
	case resp := <-responses:
		if resp.OK {
			t.Fatal("canceled acquire handler returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("Close returned before acquire handler drained")
	}
}

func TestRPCRejectsOversizedRequest(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "x.sock")
	m := leases.NewManager(router.NewRegistry(), "", 45*time.Second)
	srv := NewServer(&testHandler{m: m})
	go func() { _ = srv.Serve(sock) }()
	defer srv.Close()

	deadline := time.Now().Add(2 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	req := Request{
		Version: Version,
		Method:  "GetStatus",
		Params:  map[string]any{"padding": strings.Repeat("x", maxRPCRequestBytes)},
	}
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	// The server may close the socket as soon as the request budget is exceeded.
	_, _ = conn.Write(payload)
	if _, err := bufio.NewReader(conn).ReadByte(); err == nil {
		t.Fatal("oversized RPC request received a response")
	}
}

func TestRPCRejectsUnauthenticatedRequest(t *testing.T) {
	sock := shortSocketPath(t)
	srv := NewServer(&testHandler{m: leases.NewManager(router.NewRegistry(), "", 45*time.Second)})
	go func() { _ = srv.Serve(sock) }()
	defer srv.Close()

	deadline := time.Now().Add(2 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(Request{
		Version: Version,
		Method:  "GetStatus",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(conn).ReadByte(); err == nil {
		t.Fatal("unauthenticated RPC request received a response")
	}
}

func TestRPCRequestBudgetResetsPerRequest(t *testing.T) {
	sock := shortSocketPath(t)
	srv := NewServer(&testHandler{m: leases.NewManager(router.NewRegistry(), "", 45*time.Second)})
	go func() { _ = srv.Serve(sock) }()
	defer srv.Close()

	deadline := time.Now().Add(2 * time.Second)
	var conn net.Conn
	var err error
	for time.Now().Before(deadline) {
		conn, err = net.Dial("unix", sock)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	auth, err := readAuthToken(authPath(sock))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := encoder.Encode(Request{Version: Version, Method: "GetStatus", Auth: auth}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("authenticated request failed: %+v", response)
	}

	if err := encoder.Encode(Request{
		Version: Version,
		Method:  "GetStatus",
		Auth:    auth,
		Params:  map[string]any{"padding": strings.Repeat("x", maxRPCRequestBytes)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&response); err == nil {
		t.Fatal("oversized second request received a response")
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("portx-rpc-%d.sock", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = os.Remove(path)
		_ = os.Remove(authPath(path))
	})
	return path
}
