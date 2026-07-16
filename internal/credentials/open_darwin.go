//go:build darwin

package credentials

import "fmt"

// Open returns the preferred secret store:
//  1. macOS Keychain (default)
//  2. Plain files only if PORTX_CREDENTIALS_FILE=1
func Open() (Store, error) {
	if useFileStore() {
		return openFileStore()
	}
	s, err := openKeychain()
	if err != nil {
		return nil, fmt.Errorf("macOS Keychain unavailable: %w\n\nSet PORTX_CREDENTIALS_FILE=1 to use a local secrets file instead", err)
	}
	return s, nil
}
