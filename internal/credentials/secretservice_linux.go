//go:build linux

package credentials

import (
	"fmt"
	"os/exec"
	"strings"

	"portx/internal/apperr"
)

// secretServiceStore uses secret-tool (libsecret); password is passed on stdin.
type secretServiceStore struct{}

func openSecretService() (Store, error) {
	if _, err := exec.LookPath("secret-tool"); err != nil {
		return nil, err
	}
	return &secretServiceStore{}, nil
}

func (s *secretServiceStore) Backend() string { return "secretservice" }

func (s *secretServiceStore) Set(key, value string) error {
	cmd := exec.Command("secret-tool", "store", "--label", "portx:"+key, "service", serviceName, "account", key)
	cmd.Stdin = strings.NewReader(value)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return apperr.Wrap(apperr.ExitAuth, fmt.Sprintf("secret-tool store: %s", strings.TrimSpace(string(out))), err)
	}
	return nil
}

func (s *secretServiceStore) Get(key string) (string, error) {
	cmd := exec.Command("secret-tool", "lookup", "service", serviceName, "account", key)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", apperr.New(apperr.ExitAuth, "credential not found")
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *secretServiceStore) Delete(key string) error {
	cmd := exec.Command("secret-tool", "clear", "service", serviceName, "account", key)
	_, _ = cmd.CombinedOutput()
	return nil
}
