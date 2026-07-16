package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"portx/internal/apperr"
)

const baseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	token  string
	http   *http.Client
	base   string
}

func New(token string) *Client {
	return &Client{
		token: token,
		http: &http.Client{Timeout: 30 * time.Second},
		base:  baseURL,
	}
}

type apiResponse struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, result any) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		rdr, err := bodyReader(body)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "portx")
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		shouldRetry := resp.StatusCode == 429 || resp.StatusCode >= 500
		if shouldRetry {
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
			sleep := time.Duration(1<<attempt) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if sec, err := strconv.Atoi(ra); err == nil {
					sleep = time.Duration(sec) * time.Second
				}
			}
			time.Sleep(sleep)
			continue
		}

		var ar apiResponse
		if err := json.Unmarshal(data, &ar); err != nil {
			return apperr.Wrap(apperr.ExitProvision, "decode cloudflare response", err)
		}
		if !ar.Success {
			msg := "cloudflare API error"
			if len(ar.Errors) > 0 {
				msg = ar.Errors[0].Message
			}
			code := apperr.ExitProvision
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				code = apperr.ExitAuth
			}
			return apperr.New(code, msg)
		}
		if result != nil && len(ar.Result) > 0 {
			if err := json.Unmarshal(ar.Result, result); err != nil {
				return err
			}
		}
		return nil
	}
	return apperr.Wrap(apperr.ExitProvision, "cloudflare API retries exhausted", lastErr)
}

func bodyReader(body any) (io.Reader, error) {
	if body == nil {
		return nil, nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type Tunnel struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	ConfigSrc  string         `json:"config_src"`
	Metadata   map[string]any `json:"metadata"`
	DeletedAt  *string        `json:"deleted_at"`
}

type DNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment"`
}

func (c *Client) VerifyToken(ctx context.Context) error {
	var res struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, &res); err != nil {
		return err
	}
	if res.Status != "" && res.Status != "active" {
		return apperr.New(apperr.ExitAuth, "API token is not active")
	}
	return nil
}

func (c *Client) ListAccounts(ctx context.Context) ([]Account, error) {
	var res []Account
	if err := c.do(ctx, http.MethodGet, "/accounts?per_page=50", nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) ListZones(ctx context.Context, accountID string) ([]Zone, error) {
	path := "/zones?per_page=50"
	if accountID != "" {
		path += "&account.id=" + url.QueryEscape(accountID)
	}
	var res []Zone
	if err := c.do(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) ListTunnels(ctx context.Context, accountID, name string) ([]Tunnel, error) {
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel?is_deleted=false&per_page=50", accountID)
	if name != "" {
		path += "&name=" + url.QueryEscape(name)
	}
	var res []Tunnel
	if err := c.do(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (Tunnel, error) {
	var res Tunnel
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", accountID, tunnelID), nil, &res)
	return res, err
}

func (c *Client) CreateTunnel(ctx context.Context, accountID, name string, metadata map[string]any) (Tunnel, error) {
	body := map[string]any{
		"name":       name,
		"config_src": "cloudflare",
		"metadata":   metadata,
	}
	var res Tunnel
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/accounts/%s/cfd_tunnel", accountID), body, &res)
	return res, err
}

func (c *Client) GetTunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/token", accountID, tunnelID), nil, &token)
	return token, err
}

func (c *Client) PutTunnelConfig(ctx context.Context, accountID, tunnelID, origin string) error {
	body := map[string]any{
		"config": map[string]any{
			"ingress": []map[string]any{
				{"service": origin},
			},
		},
	}
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel/%s/configurations", accountID, tunnelID)
	return c.do(
		ctx,
		http.MethodPut,
		path,
		body,
		nil,
	)
}

func (c *Client) ListDNS(ctx context.Context, zoneID, name, recordType string) ([]DNSRecord, error) {
	path := fmt.Sprintf("/zones/%s/dns_records?per_page=100", zoneID)
	if name != "" {
		path += "&name=" + url.QueryEscape(name)
	}
	if recordType != "" {
		path += "&type=" + url.QueryEscape(recordType)
	}
	var res []DNSRecord
	err := c.do(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) CreateDNS(ctx context.Context, zoneID string, rec DNSRecord) (DNSRecord, error) {
	body := map[string]any{
		"type":    rec.Type,
		"name":    rec.Name,
		"content": rec.Content,
		"proxied": rec.Proxied,
		"comment": rec.Comment,
		"ttl":     1,
	}
	var res DNSRecord
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), body, &res)
	return res, err
}

func (c *Client) DeleteDNS(ctx context.Context, zoneID, recordID string) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, recordID), nil, nil)
}

func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/accounts/%s/cfd_tunnel/%s", accountID, tunnelID), nil, nil)
}
