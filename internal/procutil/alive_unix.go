//go:build unix

package procutil

import (
	"os"
	"syscall"
)

// Alive reports whether pid is a live process.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Interrupt sends SIGINT (graceful stop).
func Interrupt(pid int) error {
	if pid <= 0 {
		return os.ErrInvalid
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(os.Interrupt)
}

// Kill force-terminates the process.
func Kill(pid int) error {
	if pid <= 0 {
		return os.ErrInvalid
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
