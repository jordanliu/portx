//go:build windows

package credentials

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// readSecretFile validates the opened handle rather than re-checking the path
// after opening it. This prevents a path swap from changing which file is
// read between validation and the actual read.
func readSecretFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("credential path is not a regular file: %q", path)
	}
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(file.Fd()),
		&handleInfo,
	); err != nil {
		return nil, err
	}
	if handleInfo.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("credential path is a reparse point: %q", path)
	}
	if err := securePrivatePath(path, 0o600); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}
