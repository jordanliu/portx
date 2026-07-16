//go:build linux

package credentials

import "fmt"

// Open returns Secret Service (libsecret) when available.
// File store only with PORTX_CREDENTIALS_FILE=1.
func Open() (Store, error) {
	if useFileStore() {
		return openFileStore()
	}
	s, err := openSecretService()
	if err != nil {
		return nil, fmt.Errorf("Linux Secret Service unavailable: %w\n\nInstall gnome-keyring/libsecret and secret-tool, or set PORTX_CREDENTIALS_FILE=1", err)
	}
	return s, nil
}
