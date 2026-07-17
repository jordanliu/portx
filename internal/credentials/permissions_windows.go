//go:build windows

package credentials

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const (
	privateFileSDDL      = "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FA;;;OW)"
	privateDirectorySDDL = "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;OW)"
)

func securePrivatePath(path string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("credential path is a symbolic link: %q", path)
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}

	sddl := privateFileSDDL
	if info.IsDir() {
		sddl = privateDirectorySDDL
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("create private Windows security descriptor: %w", err)
	}
	absoluteSD, err := sd.ToAbsolute()
	if err != nil {
		return fmt.Errorf("make private Windows security descriptor absolute: %w", err)
	}
	dacl, _, err := absoluteSD.DACL()
	if err != nil {
		return fmt.Errorf("read private Windows DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("apply private Windows DACL: %w", err)
	}

	return nil
}
