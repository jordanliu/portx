package cli

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestIsDNSPropagationError(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("request failed: %w", &net.DNSError{
		Err:        "no such host",
		Name:       "example.test",
		IsNotFound: true,
	})
	if !isDNSPropagationError(err) {
		t.Fatal("isDNSPropagationError returned false for NXDOMAIN")
	}
	if isDNSPropagationError(errors.New("connection refused")) {
		t.Fatal("isDNSPropagationError returned true for a non-DNS error")
	}
}

func TestVerificationRetryDelay(t *testing.T) {
	t.Parallel()

	want := []time.Duration{
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for attempt, expected := range want {
		got, ok := verificationRetryDelay(attempt)
		if !ok || got != expected {
			t.Fatalf("verificationRetryDelay(%d) = %v, %v; want %v, true", attempt, got, ok, expected)
		}
	}
	if _, ok := verificationRetryDelay(len(want)); ok {
		t.Fatal("verificationRetryDelay returned a delay after the retry budget")
	}
}
