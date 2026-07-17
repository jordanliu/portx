package cli

import (
	"context"
	"errors"
	"testing"

	"portx/internal/cloudflare"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/state"
)

type failingCredentialStore struct {
	err     error
	deleted []string
}

func TestDeleteRemoteResourcesRetainsRecoveryRequirement(t *testing.T) {
	prof := config.Profile{
		AccountID: "account",
		ZoneID:    "zone",
		Wildcard:  "*.example.com",
		TunnelID:  "tunnel",
	}

	err := deleteRemoteResources(
		context.Background(),
		cloudflare.New("test-token"),
		prof,
		state.ProfileState{},
	)
	if err == nil {
		t.Fatal("expected missing DNS recovery state to fail remote cleanup")
	}
}

func (s *failingCredentialStore) Set(string, string) error { return nil }

func (s *failingCredentialStore) Get(string) (string, error) {
	return "", s.err
}

func (s *failingCredentialStore) Delete(key string) error {
	s.deleted = append(s.deleted, key)
	return s.err
}

func (s *failingCredentialStore) Backend() string { return "test" }

var _ credentials.Store = (*failingCredentialStore)(nil)

func TestDeleteProfileSecretsReportsFailures(t *testing.T) {
	wantErr := errors.New("credential backend unavailable")
	store := &failingCredentialStore{err: wantErr}

	err := deleteProfileSecrets(store, "personal")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if len(store.deleted) != 2 {
		t.Fatalf("deleted %d credentials, want 2", len(store.deleted))
	}
}
