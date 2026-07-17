//go:build unix

package procutil

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
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

// StartTime returns the process start time with enough precision to distinguish
// a rapidly reused PID on supported Unix platforms.
func StartTime(pid int) (int64, error) {
	if pid <= 0 {
		return 0, os.ErrInvalid
	}

	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return 0, err
	}
	started, err := time.ParseInLocation(
		"Mon Jan _2 15:04:05 2006",
		strings.TrimSpace(string(out)),
		time.Local,
	)
	if err != nil {
		return 0, fmt.Errorf("parse process start time: %w", err)
	}
	return started.UnixNano(), nil
}
