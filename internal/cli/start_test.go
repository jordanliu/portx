package cli

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"testing"

	"portx/internal/config"
)

func TestConfirmProjectOriginsAllowsUnavailableRoute(t *testing.T) {
	originServer := httptest.NewServer(nil)
	target := originServer.URL
	originServer.Close()

	confirmed := false
	opts := startOpts{
		ctx: context.Background(),
		pc: config.ProjectConfig{Routes: map[string]config.ProjectRoute{
			"api": {Target: target},
		}},
		only: map[string]bool{},
		confirmOrigin: func(gotTarget *url.URL, question string, preflightErr error) error {
			confirmed = true
			if gotTarget.String() != target {
				t.Fatalf("target = %q, want %q", gotTarget, target)
			}
			if question != `Start route "api" anyway?` {
				t.Fatalf("question = %q", question)
			}
			if preflightErr == nil {
				t.Fatal("confirmation did not receive the preflight error")
			}
			return nil
		},
	}

	if err := confirmProjectOrigins(opts); err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("unavailable route was not confirmed")
	}
}

func TestConfirmProjectOriginsReturnsDeclinedError(t *testing.T) {
	originServer := httptest.NewServer(nil)
	target := originServer.URL
	originServer.Close()
	want := errors.New("declined")
	opts := startOpts{
		ctx: context.Background(),
		pc: config.ProjectConfig{Routes: map[string]config.ProjectRoute{
			"api": {Target: target},
		}},
		only: map[string]bool{},
		confirmOrigin: func(*url.URL, string, error) error {
			return want
		},
	}

	if err := confirmProjectOrigins(opts); !errors.Is(err, want) {
		t.Fatalf("confirmation error = %v, want %v", err, want)
	}
}
