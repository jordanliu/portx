package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"portx/internal/apperr"
)

const baseURL = "https://api.cloudflare.com/client/v4"

const maxResponseBytes = 4 << 20

type Client struct {
	token     string
	http      *http.Client
	base      string
	retryWait func(context.Context, time.Duration) error
}

func New(token string) *Client {
	return &Client{
		token:     token,
		http:      &http.Client{Timeout: 30 * time.Second},
		base:      baseURL,
		retryWait: waitRetry,
	}
}

type apiResponse struct {
	Success    bool            `json:"success"`
	Errors     []apiError      `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo resultInfo      `json:"result_info"`
}

type resultInfo struct {
	Page       int `json:"page"`
	TotalPages int `json:"total_pages"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, body any, result any) error {
	_, err := c.doPage(ctx, method, path, body, result)
	return err
}

func (c *Client) doPage(
	ctx context.Context,
	method string,
	path string,
	body any,
	result any,
) (resultInfo, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		rdr, err := bodyReader(body)
		if err != nil {
			return resultInfo{}, err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
		if err != nil {
			return resultInfo{}, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "portx")
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return resultInfo{}, ctx.Err()
			}
			if !isRetryableMethod(method) {
				return resultInfo{}, apperr.Wrap(
					apperr.ExitProvision,
					"cloudflare request failed",
					err,
				)
			}
			lastErr = err
			if err := c.retryWait(ctx, retryDelay(attempt)); err != nil {
				return resultInfo{}, err
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return resultInfo{}, apperr.Wrap(
				apperr.ExitProvision,
				"read cloudflare response",
				readErr,
			)
		}
		if closeErr != nil {
			return resultInfo{}, apperr.Wrap(
				apperr.ExitProvision,
				"close cloudflare response",
				closeErr,
			)
		}
		if len(data) > maxResponseBytes {
			return resultInfo{}, apperr.New(
				apperr.ExitProvision,
				"cloudflare response exceeds the 4 MiB safety limit",
			)
		}

		shouldRetry := resp.StatusCode == 429 || resp.StatusCode >= 500
		if shouldRetry {
			lastErr = fmt.Errorf("HTTP %s", resp.Status)
			if !isRetryableMethod(method) {
				return resultInfo{}, lastErr
			}
			sleep := retryDelay(attempt)
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if sec, err := strconv.Atoi(ra); err == nil {
					sleep = time.Duration(sec) * time.Second
				}
			}
			if err := c.retryWait(ctx, sleep); err != nil {
				return resultInfo{}, err
			}
			continue
		}

		var ar apiResponse
		if err := json.Unmarshal(data, &ar); err != nil {
			return resultInfo{}, apperr.Wrap(
				apperr.ExitProvision,
				"decode cloudflare response",
				err,
			)
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
			return resultInfo{}, apperr.Wrap(code, msg, &APIError{
				StatusCode: resp.StatusCode,
				Message:    msg,
			})
		}
		if result != nil && len(ar.Result) > 0 {
			if err := json.Unmarshal(ar.Result, result); err != nil {
				return resultInfo{}, err
			}
		}
		return ar.ResultInfo, nil
	}
	return resultInfo{}, apperr.Wrap(
		apperr.ExitProvision,
		"cloudflare API retries exhausted",
		lastErr,
	)
}

func isRetryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

func retryDelay(attempt int) time.Duration {
	return time.Duration(1<<attempt) * time.Second
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Status    string         `json:"status"`
	ConfigSrc string         `json:"config_src"`
	Metadata  map[string]any `json:"metadata"`
	DeletedAt *string        `json:"deleted_at"`
}

// APIError preserves the HTTP status for callers that need to distinguish a
// missing resource from a transient or authorization failure.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
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
	res := []Account{}
	return appendPages(ctx, c, "/accounts?per_page=50", &res)
}

func (c *Client) ListZones(ctx context.Context, accountID string) ([]Zone, error) {
	path := "/zones?per_page=50"
	if accountID != "" {
		path += "&account.id=" + url.QueryEscape(accountID)
	}
	res := []Zone{}
	return appendPages(ctx, c, path, &res)
}

func (c *Client) ListTunnels(ctx context.Context, accountID, name string) ([]Tunnel, error) {
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel?is_deleted=false&per_page=50",
		pathSegment(accountID),
	)
	if name != "" {
		path += "&name=" + url.QueryEscape(name)
	}
	res := []Tunnel{}
	return appendPages(ctx, c, path, &res)
}

func (c *Client) GetTunnel(ctx context.Context, accountID, tunnelID string) (Tunnel, error) {
	var res Tunnel
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel/%s",
		pathSegment(accountID),
		pathSegment(tunnelID),
	)
	err := c.do(ctx, http.MethodGet, path, nil, &res)
	return res, err
}

func (c *Client) CreateTunnel(ctx context.Context, accountID, name string, metadata map[string]any) (Tunnel, error) {
	body := map[string]any{
		"name":       name,
		"config_src": "cloudflare",
		"metadata":   metadata,
	}
	var res Tunnel
	path := fmt.Sprintf("/accounts/%s/cfd_tunnel", pathSegment(accountID))
	err := c.do(ctx, http.MethodPost, path, body, &res)
	return res, err
}

func (c *Client) GetTunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	var token string
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel/%s/token",
		pathSegment(accountID),
		pathSegment(tunnelID),
	)
	err := c.do(ctx, http.MethodGet, path, nil, &token)
	return token, err
}

func (c *Client) PutTunnelConfig(ctx context.Context, accountID, tunnelID, origin string) error {
	config := map[string]any{
		"ingress": []map[string]any{
			{"service": origin},
		},
	}
	return c.PutTunnelConfigValue(ctx, accountID, tunnelID, config)
}

func (c *Client) GetTunnelConfig(
	ctx context.Context,
	accountID string,
	tunnelID string,
) (map[string]any, error) {
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel/%s/configurations",
		pathSegment(accountID),
		pathSegment(tunnelID),
	)
	var result struct {
		Config map[string]any `json:"config"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	if result.Config == nil {
		return map[string]any{}, nil
	}
	return result.Config, nil
}

func (c *Client) PutTunnelConfigValue(
	ctx context.Context,
	accountID string,
	tunnelID string,
	config map[string]any,
) error {
	body := map[string]any{"config": config}
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel/%s/configurations",
		pathSegment(accountID),
		pathSegment(tunnelID),
	)
	return c.do(
		ctx,
		http.MethodPut,
		path,
		body,
		nil,
	)
}

func (c *Client) ListDNS(ctx context.Context, zoneID, name, recordType string) ([]DNSRecord, error) {
	path := fmt.Sprintf("/zones/%s/dns_records?per_page=100", pathSegment(zoneID))
	if name != "" {
		path += "&name=" + url.QueryEscape(name)
	}
	if recordType != "" {
		path += "&type=" + url.QueryEscape(recordType)
	}
	res := []DNSRecord{}
	return appendPages(ctx, c, path, &res)
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
	path := fmt.Sprintf("/zones/%s/dns_records", pathSegment(zoneID))
	err := c.do(ctx, http.MethodPost, path, body, &res)
	return res, err
}

func (c *Client) DeleteDNS(ctx context.Context, zoneID, recordID string) error {
	path := fmt.Sprintf(
		"/zones/%s/dns_records/%s",
		pathSegment(zoneID),
		pathSegment(recordID),
	)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) DeleteTunnel(ctx context.Context, accountID, tunnelID string) error {
	path := fmt.Sprintf(
		"/accounts/%s/cfd_tunnel/%s",
		pathSegment(accountID),
		pathSegment(tunnelID),
	)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func appendPages[T any](
	ctx context.Context,
	c *Client,
	path string,
	result *[]T,
) ([]T, error) {
	page := 1
	for {
		current := []T{}
		info, err := c.doPage(
			ctx,
			http.MethodGet,
			pagePath(path, page),
			nil,
			&current,
		)
		if err != nil {
			return nil, err
		}
		*result = append(*result, current...)
		if info.TotalPages <= page || info.TotalPages == 0 {
			return *result, nil
		}
		page++
	}
}

func pagePath(path string, page int) string {
	parsed, err := url.Parse(path)
	if err != nil {
		return path
	}
	query := parsed.Query()
	query.Set("page", strconv.Itoa(page))
	parsed.RawQuery = query.Encode()
	return parsed.RequestURI()
}

func pathSegment(value string) string {
	return url.PathEscape(value)
}
