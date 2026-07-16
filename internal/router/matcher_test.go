package router

import (
	"net/url"
	"testing"
	"time"
)

func TestLongestPrefix(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	_ = reg.Add(Route{ID: "1", Hostname: "api.example.dev", PathPrefix: "/", Target: mustURL("http://127.0.0.1:3000"), CreatedAt: time.Now()})
	_ = reg.Add(Route{ID: "2", Hostname: "api.example.dev", PathPrefix: "/webhooks", Target: mustURL("http://127.0.0.1:4000"), CreatedAt: time.Now()})

	rt, ok := reg.Match("api.example.dev", "/webhooks/stripe")
	if !ok || rt.ID != "2" {
		t.Fatalf("got %+v ok=%v", rt, ok)
	}
	rt, ok = reg.Match("api.example.dev", "/health")
	if !ok || rt.ID != "1" {
		t.Fatalf("got %+v ok=%v", rt, ok)
	}
	_, ok = reg.Match("other.example.dev", "/")
	if ok {
		t.Fatal("expected miss")
	}
}

func TestPathMatchesPrefixBoundary(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path, prefix string
		want         bool
	}{
		{"/", "/", true},
		{"/webhooks", "/webhooks", true},
		{"/webhooks/stripe", "/webhooks", true},
		{"/webhook", "/webhooks", false},
		{"/we", "/web", false},
		{"/web", "/we", false},
		{"/health", "/", true},
	}
	for _, tc := range cases {
		if got := pathMatches(tc.path, tc.prefix); got != tc.want {
			t.Fatalf("pathMatches(%q, %q)=%v want %v", tc.path, tc.prefix, got, tc.want)
		}
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
