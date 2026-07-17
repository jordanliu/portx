package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(server *httptest.Server) *Client {
	client := New("test-token")
	client.base = server.URL
	client.http = server.Client()
	client.retryWait = func(context.Context, time.Duration) error {
		return nil
	}
	return client
}

func TestClientDoesNotRetryPostAfterServerError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.CreateTunnel(context.Background(), "account", "portx", nil)
	if err == nil {
		t.Fatal("expected create tunnel to fail")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("expected one POST request, got %d", got)
	}
}

func TestClientRetriesGetAfterServerError(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		writeAPIResponse(t, w, map[string]string{"status": "active"}, resultInfo{})
	}))
	defer server.Close()

	client := testClient(server)
	if err := client.VerifyToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("expected two GET requests, got %d", got)
	}
}

func TestListTunnelsPaginatesAndEscapesAccountID(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.Query().Get("page"))
		if !strings.Contains(r.URL.EscapedPath(), "/accounts/account%2Fone/") {
			t.Fatalf("account ID was not escaped: %s", r.URL.EscapedPath())
		}
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			writeAPIResponse(t, w, []Tunnel{{ID: "first"}}, resultInfo{Page: 1, TotalPages: 2})
		case "2":
			writeAPIResponse(t, w, []Tunnel{{ID: "second"}}, resultInfo{Page: 2, TotalPages: 2})
		default:
			t.Fatalf("unexpected page %q", page)
		}
	}))
	defer server.Close()

	client := testClient(server)
	tunnels, err := client.ListTunnels(context.Background(), "account/one", "portx")
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{tunnels[0].ID, tunnels[1].ID}; got[0] != "first" || got[1] != "second" {
		t.Fatalf("unexpected tunnels: %v", got)
	}
	if got := strings.Join(pages, ","); got != "1,2" {
		t.Fatalf("expected pages 1 and 2, got %s", got)
	}
}

func TestDeleteDNSEscapesPathSegments(t *testing.T) {
	var escapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escapedPath = r.URL.EscapedPath()
		writeAPIResponse(t, w, nil, resultInfo{})
	}))
	defer server.Close()

	client := testClient(server)
	if err := client.DeleteDNS(context.Background(), "zone/one", "record/two"); err != nil {
		t.Fatal(err)
	}
	want := "/zones/zone%2Fone/dns_records/record%2Ftwo"
	if escapedPath != want {
		t.Fatalf("unexpected escaped path: got %q, want %q", escapedPath, want)
	}
}

func TestTunnelConfigRoundTripUsesRawConfiguration(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeAPIResponse(t, w, map[string]any{
				"config": map[string]any{
					"ingress": []any{
						map[string]any{"hostname": "app.example.com", "service": "http://127.0.0.1:3000"},
					},
				},
			}, resultInfo{})
			return
		}
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeAPIResponse(t, w, nil, resultInfo{})
	}))
	defer server.Close()

	client := testClient(server)
	config, err := client.GetTunnelConfig(context.Background(), "account", "tunnel")
	if err != nil {
		t.Fatal(err)
	}
	if len(config) != 1 {
		t.Fatalf("unexpected tunnel config: %#v", config)
	}
	if err := client.PutTunnelConfigValue(context.Background(), "account", "tunnel", config); err != nil {
		t.Fatal(err)
	}
	if _, ok := received["config"]; !ok {
		t.Fatalf("request did not contain config: %#v", received)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer server.Close()

	client := testClient(server)
	if err := client.VerifyToken(context.Background()); err == nil {
		t.Fatal("expected oversized response to fail")
	}
}

func TestGetTunnelPreservesNotFoundStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		if err := json.NewEncoder(w).Encode(apiResponse{
			Success: false,
			Errors:  []apiError{{Message: "tunnel not found"}},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.GetTunnel(context.Background(), "account", "tunnel")
	if err == nil {
		t.Fatal("expected get tunnel to fail")
	}
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false, want true", err)
	}
}

func TestGetTunnelDoesNotClassifyServerErrorAsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary failure", http.StatusBadGateway)
	}))
	defer server.Close()

	client := testClient(server)
	_, err := client.GetTunnel(context.Background(), "account", "tunnel")
	if err == nil {
		t.Fatal("expected get tunnel to fail")
	}
	if IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = true, want false", err)
	}
}

func writeAPIResponse(t *testing.T, w http.ResponseWriter, result any, info resultInfo) {
	t.Helper()
	response := apiResponse{
		Success:    true,
		ResultInfo: info,
	}
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		response.Result = encoded
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Fatal(err)
	}
}
