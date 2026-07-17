//go:build !windows

package credentials

import "os"

func securePrivatePath(path string, mode os.FileMode) error {
	return os.Chmod(path, mode)
}
