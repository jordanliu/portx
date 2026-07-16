//go:build windows

package daemon

import "os"

func flockExclusive(f *os.File) error {
	// best-effort: rely on socket bind uniqueness on Windows for MVP
	return nil
}
