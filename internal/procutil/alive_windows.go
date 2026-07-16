//go:build windows

package procutil

import (
	"os"

	"golang.org/x/sys/windows"
)

const stillActive = 259

// Alive reports whether pid is a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// Interrupt on Windows has no SIGINT equivalent for arbitrary processes; force kill.
func Interrupt(pid int) error {
	return Kill(pid)
}

// Kill force-terminates the process.
func Kill(pid int) error {
	if pid <= 0 {
		return os.ErrInvalid
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}
