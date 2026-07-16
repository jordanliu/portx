//go:build !darwin && !linux && !windows

package credentials

import "fmt"

func Open() (Store, error) {
	if useFileStore() {
		return openFileStore()
	}
	return nil, fmt.Errorf("no OS credential store for this platform; set PORTX_CREDENTIALS_FILE=1")
}
