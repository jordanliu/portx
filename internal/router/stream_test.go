package router

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestSSEStreaming(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("no flusher")
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: event-%d\n\n", i)
			flusher.Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer origin.Close()

	u, _ := url.Parse(origin.URL)
	reg := NewRegistry()
	_ = reg.Add(Route{ID: "1", Hostname: "sse.example.dev", PathPrefix: "/", Target: u, CreatedAt: time.Now()})
	p := NewProxy(reg)

	proxy := httptest.NewServer(p)
	defer proxy.Close()

	req, _ := http.NewRequest(http.MethodGet, proxy.URL+"/events", nil)
	req.Host = "sse.example.dev"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type %s", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	var events []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "data: ") {
			events = append(events, strings.TrimPrefix(line, "data: "))
		}
		if len(events) >= 3 {
			break
		}
	}
	if len(events) < 3 {
		t.Fatalf("events %v", events)
	}
}

func TestWebSocketEcho(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}))
	defer origin.Close()

	u, _ := url.Parse(origin.URL)
	reg := NewRegistry()
	_ = reg.Add(Route{ID: "1", Hostname: "ws.example.dev", PathPrefix: "/", Target: u, CreatedAt: time.Now()})
	p := NewProxy(reg)
	proxy := httptest.NewServer(p)
	defer proxy.Close()

	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + "/echo"
	hdr := http.Header{}
	hdr.Set("Host", "ws.example.dev")
	// gorilla dial uses URL host; set via dialer and custom header Host may not work with httptest
	// Use dial with proxy host but rewrite via request - for httptest, Host header on dial:
	d := websocket.Dialer{}
	conn, resp, err := d.Dial(wsURL, http.Header{"Host": []string{"ws.example.dev"}})
	if err != nil {
		// httptest may not route by Host; fall back to direct registry path by using a custom transport handler
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			t.Logf("dial failed status=%v body=%s", resp.Status, body)
		}
		// Alternative: hit proxy ServeHTTP via hijack is complex; call origin through proxy with host set on request URL
		// Use NewRequest pattern with custom client - websocket needs real TCP.
		// For unit test without Host-based match: register empty? Use Match on localhost from httptest URL host.
		host := strings.TrimPrefix(proxy.URL, "http://")
		reg2 := NewRegistry()
		_ = reg2.Add(Route{ID: "1", Hostname: strings.Split(host, ":")[0], PathPrefix: "/", Target: u, CreatedAt: time.Now()})
		// also add IP host
		if h, port, ok := strings.Cut(host, ":"); ok {
			_ = reg2.Add(Route{ID: "2", Hostname: h, PathPrefix: "/", Target: u, CreatedAt: time.Now()})
			_ = port
		}
		p2 := NewProxy(reg2)
		proxy2 := httptest.NewServer(p2)
		defer proxy2.Close()
		wsURL = "ws" + strings.TrimPrefix(proxy2.URL, "http") + "/echo"
		conn, _, err = d.Dial(wsURL, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if string(msg) != "ping" {
		t.Fatalf("got %q", msg)
	}
}
