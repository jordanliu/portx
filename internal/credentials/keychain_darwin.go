//go:build darwin

package credentials

import (
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"

	"portx/internal/apperr"
)

// keychainStore uses macOS Keychain via `security -i` so the secret is never
// placed on process argv (unlike `security … -w <password>`).
type keychainStore struct{}

func openKeychain() (Store, error) {
	if _, err := exec.LookPath("security"); err != nil {
		return nil, err
	}
	return &keychainStore{}, nil
}

func (k *keychainStore) Backend() string { return "keychain" }

func (k *keychainStore) Set(key, value string) error {
	_ = k.Delete(key)
	// -X expects hex-encoded password data; pass via security -i stdin (not argv).
	hexPass := hex.EncodeToString([]byte(value))
	// Quote account for the security command language.
	acct := shellQuote(key)
	script := fmt.Sprintf("add-generic-password -a %s -s %s -X %s -U\n", acct, shellQuote(serviceName), hexPass)
	cmd := exec.Command("security", "-i")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return apperr.Wrap(apperr.ExitAuth, "keychain set failed: "+strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (k *keychainStore) Get(key string) (string, error) {
	// -w prints password only; secret is not passed as an argument.
	cmd := exec.Command("security", "find-generic-password", "-a", key, "-s", serviceName, "-w")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", apperr.New(apperr.ExitAuth, "credential not found in keychain")
	}
	return strings.TrimSpace(string(out)), nil
}

func (k *keychainStore) Delete(key string) error {
	cmd := exec.Command("security", "delete-generic-password", "-a", key, "-s", serviceName)
	_, _ = cmd.CombinedOutput()
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
