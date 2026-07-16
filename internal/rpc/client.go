package rpc

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"portx/internal/apperr"
	"portx/internal/leases"
)

type Client struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

func Dial(sockPath string) (*Client, error) {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, apperr.Wrap(apperr.ExitDaemon, "connect to daemon", err)
	}
	return &Client{conn: conn, enc: json.NewEncoder(conn), dec: json.NewDecoder(conn)}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) call(method string, params map[string]any) (map[string]any, error) {
	req := Request{Version: Version, Method: method, Params: params}
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

func (c *Client) GetStatus() (StatusResult, error) {
	res, err := c.call("GetStatus", nil)
	if err != nil {
		return StatusResult{}, err
	}
	b, _ := json.Marshal(res)
	var st StatusResult
	_ = json.Unmarshal(b, &st)
	return st, nil
}

func (c *Client) AcquireLease(p AcquireParams) (leases.Lease, error) {
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
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
	raw, _ := res["leases"].([]any)
	out := make([]leases.Lease, 0, len(raw))
	for _, item := range raw {
		b, _ := json.Marshal(item)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		l, err := mapToLease(m)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

func (c *Client) StartTunnel() error {
	_, err := c.call("StartTunnel", nil)
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
