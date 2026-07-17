//go:build unix

package credentials

import (
	"io"
	"os"
	"syscall"
)

func readSecretFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	if err := securePrivatePath(path, 0o600); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}
