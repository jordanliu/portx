package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"portx/internal/apperr"
	"portx/internal/config"
)

// Store is a secret backend for API/tunnel tokens.
// Prefer OS credential stores; plain files only when explicitly opted in.
type Store interface {
	Set(key, value string) error
	Get(key string) (string, error)
	Delete(key string) error
	// Backend is a short name for diagnostics (keychain, secretservice, wincred, file).
	Backend() string
}

func APITokenKey(profile string) string {
	return fmt.Sprintf("portx/profile/%s/api-token", profile)
}

func TunnelTokenKey(profile string) string {
	return fmt.Sprintf("portx/profile/%s/tunnel-token", profile)
}

// serviceName is the Keychain / Secret Service / Credential Manager service id.
const serviceName = "portx"

type fileStore struct {
	dir string
}

func openFileStore() (*fileStore, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "secrets")
	if err := config.EnsureDir(dir); err != nil {
		return nil, err
	}
	return &fileStore{dir: dir}, nil
}

func (f *fileStore) Backend() string { return "file" }

func (f *fileStore) path(key string) string {
	// Hash keys so paths stay short and don't leak structure in filenames.
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:16]))
}

func (f *fileStore) Set(key, value string) error {
	return os.WriteFile(f.path(key), []byte(value), 0o600)
}

func (f *fileStore) Get(key string) (string, error) {
	b, err := os.ReadFile(f.path(key))
	if err != nil {
		if os.IsNotExist(err) {
			return "", apperr.New(apperr.ExitAuth, "credential not found")
		}
		return "", err
	}
	return string(b), nil
}

func (f *fileStore) Delete(key string) error {
	err := os.Remove(f.path(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func useFileStore() bool {
	return os.Getenv("PORTX_CREDENTIALS_FILE") == "1"
}
