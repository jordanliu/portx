package credentials

import (
	"fmt"
	"os"
)

func atomicWriteFile(dir, target string, data []byte) error {
	temp, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()

	if err := securePrivatePath(tempName, 0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("secure temporary credential file: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write credential file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync credential file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary credential file: %w", err)
	}
	if err := replaceFile(tempName, target); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	if err := securePrivatePath(target, 0o600); err != nil {
		return fmt.Errorf("secure credential file: %w", err)
	}

	return nil
}
