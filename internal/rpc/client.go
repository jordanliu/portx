package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"portx/internal/apperr"
	"portx/internal/leases"
)

type Client struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
	auth string
	mu   sync.Mutex
}

const (
	rpcClientWriteTimeout = 10 * time.Second
	rpcClientReadTimeout  = 60 * time.Second
)

func Dial(sockPath string) (*Client, error) {
	auth, err := readAuthToken(authPath(sockPath))
	if err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "read daemon authentication token", err)
	}
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "connect to daemon", err)
	}
	return &Client{
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(conn),
		auth: auth,
	}, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.Close()
}

func (c *Client) call(method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(rpcClientWriteTimeout)); err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "set rpc write deadline", err)
	}
	defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	if err := c.conn.SetReadDeadline(time.Now().Add(rpcClientReadTimeout)); err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "set rpc read deadline", err)
	}
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()
	req := Request{
		Version: Version,
		Method:  method,
		Params:  params,
		Auth:    c.auth,
	}
	if err := c.enc.Encode(req); err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "rpc write", err)
	}
	var resp Response
	if err := c.dec.Decode(&resp); err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "rpc read", err)
	}
	if !resp.OK {
		code := resp.Code
		if code == 0 {
			code = apperr.ExitDaemon
		}
		return nil, apperr.New(code, resp.Error)
	}
	return resp.Result, nil
}

func authPath(sockPath string) string {
	return sockPath + ".auth"
}

func readAuthToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("authentication token is not a regular file")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(b))
	if token == "" {
		return "", fmt.Errorf("authentication token is empty")
	}
	return token, nil
}

func (c *Client) callContext(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = c.conn.Close()
		case <-done:
		}
	}()
	res, err := c.call(method, params)
	close(done)
	return res, err
}

func (c *Client) GetStatus() (StatusResult, error) {
	res, err := c.call("GetStatus", nil)
	if err != nil {
		return StatusResult{}, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return StatusResult{}, fmt.Errorf("encode status: %w", err)
	}
	var st StatusResult
	if err := json.Unmarshal(b, &st); err != nil {
		return StatusResult{}, fmt.Errorf("decode status: %w", err)
	}
	return st, nil
}

func (c *Client) AcquireLease(p AcquireParams) (leases.Lease, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return leases.Lease{}, fmt.Errorf("encode acquire parameters: %w", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return leases.Lease{}, fmt.Errorf("decode acquire parameters: %w", err)
	}
	res, err := c.call("AcquireLease", m)
	if err != nil {
		return leases.Lease{}, err
	}
	return mapToLease(res)
}

func (c *Client) RenewLease(id, token string) (leases.Lease, error) {
	res, err := c.call("RenewLease", map[string]any{"id": id, "owner_token": token})
	if err != nil {
		return leases.Lease{}, err
	}
	return mapToLease(res)
}

func (c *Client) ReleaseLease(id, token string) error {
	_, err := c.call("ReleaseLease", map[string]any{"id": id, "owner_token": token})
	return err
}

func (c *Client) ForceRelease(id string) error {
	_, err := c.call("ForceRelease", map[string]any{"id": id})
	return err
}

func (c *Client) ListLeases() ([]leases.Lease, error) {
	res, err := c.call("ListLeases", nil)
	if err != nil {
		return nil, err
	}
	rawValue, ok := res["leases"]
	if !ok {
		return nil, fmt.Errorf("rpc response is missing leases")
	}
	raw, ok := rawValue.([]any)
	if !ok {
		return nil, fmt.Errorf("rpc response leases has unexpected type %T", rawValue)
	}
	out := make([]leases.Lease, 0, len(raw))
	for _, item := range raw {
		b, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("encode lease: %w", err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("decode lease response: %w", err)
		}
		l, err := mapToLease(m)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (c *Client) StartTunnel() error {
	return c.StartTunnelContext(context.Background())
}

func (c *Client) StartTunnelContext(ctx context.Context) error {
	_, err := c.callContext(ctx, "StartTunnel", nil)
	return err
}

func mapToLease(m map[string]any) (leases.Lease, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return leases.Lease{}, err
	}
	var l leases.Lease
	if err := json.Unmarshal(b, &l); err != nil {
		return leases.Lease{}, fmt.Errorf("decode lease: %w", err)
	}
	return l, nil
}
