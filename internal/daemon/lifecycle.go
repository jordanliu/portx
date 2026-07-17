package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"portx/internal/procutil"
)

const (
	orphanInterruptTimeout = 3 * time.Second
	orphanKillTimeout      = 2 * time.Second
)

var errStalePIDRecord = errors.New("stale process pid record")
var errUnverifiedPIDRecord = errors.New("unverified process pid file")

func (d *Daemon) recoverCloudflared() error {
	pidPath := filepath.Join(d.runtimeDir, "cloudflared.pid")
	record, err := readPIDRecord(pidPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		if errors.Is(err, errUnverifiedPIDRecord) {
			log.Printf("portx: removing stale cloudflared pid file: %v", err)
			return removeIfPresent(pidPath)
		}
		return err
	}
	if !procutil.Alive(record.PID) {
		return removeIfPresent(pidPath)
	}
	if err := validatePIDRecord(record, "cloudflared"); err != nil {
		log.Printf("portx: removing unverified cloudflared pid record: %v", err)
		return removeIfPresent(pidPath)
	}
	if err := stopRecordedProcess(record); err != nil {
		return err
	}
	return removeIfPresent(pidPath)
}

func readPIDRecord(path string) (pidRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return pidRecord{}, err
	}
	var record pidRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return pidRecord{}, fmt.Errorf("%w: invalid JSON", errUnverifiedPIDRecord)
	}
	if record.PID <= 0 || record.Executable == "" || record.Kind == "" || record.StartTime <= 0 {
		return pidRecord{}, fmt.Errorf("%w: missing required fields", errUnverifiedPIDRecord)
	}
	return record, nil
}

func validatePIDRecord(record pidRecord, kind string) error {
	if record.Kind != kind {
		return fmt.Errorf("pid file identifies %q, expected %q", record.Kind, kind)
	}
	if !procutil.Alive(record.PID) {
		return nil
	}
	startTime, err := procutil.StartTime(record.PID)
	if err != nil {
		return fmt.Errorf("verify pid %d start time: %w", record.PID, err)
	}
	if startTime != record.StartTime {
		return fmt.Errorf(
			"%w: pid %d start time does not match pid record",
			errStalePIDRecord,
			record.PID,
		)
	}
	name, command, err := inspectRecordedProcess(record.PID)
	if err != nil {
		return fmt.Errorf("inspect pid %d: %w", record.PID, err)
	}
	expectedName := normalizedProcessName(filepath.Base(record.Executable))
	actualName := normalizedProcessName(filepath.Base(name))
	if actualName != expectedName {
		return fmt.Errorf("pid %d is %q, expected %q", record.PID, actualName, expectedName)
	}
	if !commandHasArgument(command, "tunnel") || !commandHasArgument(command, "run") {
		return fmt.Errorf("pid %d command does not identify a named cloudflared tunnel", record.PID)
	}
	return nil
}

func stopRecordedProcess(record pidRecord) error {
	if err := procutil.Interrupt(record.PID); err != nil && procutil.Alive(record.PID) {
		return fmt.Errorf("interrupt cloudflared pid %d: %w", record.PID, err)
	}
	if waitForProcessExit(record.PID, orphanInterruptTimeout) {
		return nil
	}
	if err := validatePIDRecord(record, "cloudflared"); err != nil {
		return fmt.Errorf("refusing forced termination: %w", err)
	}
	if err := procutil.Kill(record.PID); err != nil && procutil.Alive(record.PID) {
		return fmt.Errorf("kill cloudflared pid %d: %w", record.PID, err)
	}
	if !waitForProcessExit(record.PID, orphanKillTimeout) {
		return fmt.Errorf("cloudflared pid %d did not exit", record.PID)
	}
	return nil
}

func waitForProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for procutil.Alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	return !procutil.Alive(pid)
}

func inspectRecordedProcess(pid int) (string, string, error) {
	if runtime.GOOS == "windows" {
		return inspectWindowsProcess(pid)
	}
	nameOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", "", err
	}
	commandOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(nameOut)), strings.TrimSpace(string(commandOut)), nil
}

func inspectWindowsProcess(pid int) (string, string, error) {
	nameCommand := fmt.Sprintf("(Get-Process -Id %d -ErrorAction Stop).Path", pid)
	nameOut, err := exec.Command(
		"powershell",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		nameCommand,
	).Output()
	if err != nil {
		return "", "", err
	}
	command := fmt.Sprintf(
		"(Get-CimInstance Win32_Process -Filter 'ProcessId = %d').CommandLine",
		pid,
	)
	commandOut, err := exec.Command(
		"powershell",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	).Output()
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(nameOut)), strings.TrimSpace(string(commandOut)), nil
}

func normalizedProcessName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".exe")
}

func commandHasArgument(command, want string) bool {
	for _, argument := range strings.Fields(command) {
		if strings.Trim(argument, `"'`) == want {
			return true
		}
	}
	return false
}
