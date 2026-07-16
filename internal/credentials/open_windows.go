//go:build windows

package credentials

import "fmt"

func Open() (Store, error) {
	if useFileStore() {
		return openFileStore()
	}
	s, err := openWincred()
	if err != nil {
		return nil, fmt.Errorf("Windows Credential Manager unavailable: %w\n\nSet PORTX_CREDENTIALS_FILE=1 to use a local secrets file", err)
	}
	return s, nil
}
