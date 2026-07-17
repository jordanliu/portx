package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/config"
	"portx/internal/procutil"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func cleanupCommand() *cli.Command {
	return &cli.Command{
		Name:  "cleanup",
		Usage: "Remove stale local runtime state (sockets, pids, expired leases)",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force", Usage: "remove runtime files even if daemon appears running"},
		},
		Action: runCleanup,
	}
}

func runCleanup(ctx context.Context, cmd *cli.Command) error {
	runtimeDir, err := config.RuntimeDir()
	if err != nil {
		return err
	}
	warnings := 0
	sock := filepath.Join(runtimeDir, "portxd.sock")
	pidPath := filepath.Join(runtimeDir, "portxd.pid")
	daemonRunning := false
	if c, err := rpc.Dial(sock); err == nil {
		if _, statusErr := c.GetStatus(); statusErr == nil {
			daemonRunning = true
		}
		_ = c.Close()
	}
	if !daemonRunning {
		if _, err := os.Stat(pidPath); err == nil {
			record, readErr := readPID(pidPath)
			if readErr != nil {
				return apperr.New(apperr.ExitCleanupWarning, "refusing cleanup: invalid daemon pid file")
			}
			if procutil.Alive(record.PID) {
				if identityErr := validateProcess(record, "portxd"); identityErr != nil {
					return apperr.New(apperr.ExitCleanupWarning, identityErr.Error())
				}
				daemonRunning = true
			}
		}
	}
	if daemonRunning {
		if !cmd.Bool("force") {
			ui.Warn("daemon is running; stop it first or pass --force")
			return apperr.New(apperr.ExitCleanupWarning, "daemon still running")
		}
		if err := stopDaemon(pidPath); err != nil {
			ui.Warn("could not stop daemon: %v", err)
			return apperr.New(apperr.ExitCleanupWarning, "daemon is still running")
		}
	}
	cloudflaredPID := filepath.Join(runtimeDir, "cloudflared.pid")
	preserveCloudflaredPID := false
	if _, err := os.Stat(cloudflaredPID); err == nil {
		if err := stopProcessFile(cloudflaredPID, "cloudflared", stopOptions{
			interruptTimeout: 5 * time.Second,
			killTimeout:      5 * time.Second,
			processName:      "cloudflared",
		}); err != nil {
			ui.Warn("could not stop orphaned cloudflared: %v", err)
			warnings++
			preserveCloudflaredPID = true
		}
	}

	leasesDir := filepath.Join(runtimeDir, "leases")
	if entries, err := os.ReadDir(leasesDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			_ = os.Remove(filepath.Join(leasesDir, e.Name()))
		}
	}

	files := []string{"portxd.sock", "portxd.pid", "portxd.lock"}
	if !preserveCloudflaredPID {
		files = append(files, "cloudflared.pid")
	}
	for _, name := range files {
		path := filepath.Join(runtimeDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			ui.Warn("could not remove %s: %v", path, err)
			warnings++
		}
	}

	if warnings > 0 {
		return apperr.New(apperr.ExitCleanupWarning, "cleanup completed with warnings")
	}
	ui.Success("cleanup complete")
	return nil
}

type processRecord struct {
	PID        int    `json:"pid"`
	Executable string `json:"executable"`
	Kind       string `json:"kind"`
	StartTime  int64  `json:"start_time"`
}

type stopOptions struct {
	interruptTimeout time.Duration
	killTimeout      time.Duration
	processName      string
}

func stopDaemon(pidFile string) error {
	return stopProcessFile(pidFile, "portxd", stopOptions{
		interruptTimeout: 10 * time.Second,
		killTimeout:      5 * time.Second,
		processName:      "daemon",
	})
}

func stopProcessFile(pidFile, kind string, opts stopOptions) error {
	record, err := readPID(pidFile)
	if err != nil {
		return err
	}
	if !procutil.Alive(record.PID) {
		return nil
	}
	if err := validateProcess(record, kind); err != nil {
		return err
	}
	if !procutil.Alive(record.PID) {
		return nil
	}
	if err := procutil.Interrupt(record.PID); err != nil && procutil.Alive(record.PID) {
		return err
	}
	deadline := time.Now().Add(opts.interruptTimeout)
	for procutil.Alive(record.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if !procutil.Alive(record.PID) {
		return nil
	}
	if err := validateProcess(record, kind); err != nil {
		return fmt.Errorf("refusing forced termination: %w", err)
	}
	if err := procutil.Kill(record.PID); err != nil && procutil.Alive(record.PID) {
		return err
	}
	deadline = time.Now().Add(opts.killTimeout)
	for procutil.Alive(record.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if procutil.Alive(record.PID) {
		return apperr.New(apperr.ExitCleanupWarning, fmt.Sprintf("%s did not exit", opts.processName))
	}
	return nil
}

func readPID(pidFile string) (processRecord, error) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return processRecord{}, err
	}
	var record processRecord
	if err := json.Unmarshal(b, &record); err != nil {
		return processRecord{}, apperr.New(apperr.ExitCleanupWarning, "unverified process pid file")
	}
	if record.PID <= 0 || record.Executable == "" || record.Kind == "" || record.StartTime <= 0 {
		return processRecord{}, apperr.New(apperr.ExitCleanupWarning, "unverified process pid file")
	}
	return record, nil
}

func validateProcess(record processRecord, kind string) error {
	if record.Kind != kind {
		return fmt.Errorf("pid file identifies %s, expected %s", record.Kind, kind)
	}
	requiredArguments, ok := map[string][]string{
		"portxd":      []string{"daemon", "run"},
		"cloudflared": []string{"tunnel", "run"},
	}[kind]
	if !ok {
		return fmt.Errorf("unknown process kind %q", kind)
	}
	name, command, startTime, err := inspectProcess(record.PID)
	if err != nil {
		return fmt.Errorf("could not verify pid %d: %w", record.PID, err)
	}
	expectedName := filepath.Base(record.Executable)
	actualName := filepath.Base(strings.TrimSpace(name))
	if !sameProcessName(actualName, expectedName) {
		return fmt.Errorf("pid %d is %q, expected %q", record.PID, actualName, expectedName)
	}
	if startTime != record.StartTime {
		return fmt.Errorf("pid %d start time does not match pid record", record.PID)
	}
	for _, argument := range requiredArguments {
		if !commandHasArgument(command, argument) {
			return fmt.Errorf("pid %d command does not identify %s", record.PID, kind)
		}
	}
	return nil
}

func inspectProcess(pid int) (name, command string, startTime int64, err error) {
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
		if err != nil {
			return "", "", 0, err
		}
		line := strings.TrimSpace(string(out))
		if line == "" || strings.HasPrefix(line, "INFO:") {
			return "", "", 0, fmt.Errorf("process not found")
		}
		parts := strings.SplitN(line, ",", 2)
		command, err := windowsProcessCommand(pid)
		if err != nil {
			return "", "", 0, err
		}
		startTime, err := processStartTime(pid)
		if err != nil {
			return "", "", 0, err
		}
		return strings.Trim(parts[0], "\""), command, startTime, nil
	}
	nameOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return "", "", 0, err
	}
	commandOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", "", 0, err
	}
	startTime, err = processStartTime(pid)
	if err != nil {
		return "", "", 0, err
	}
	return strings.TrimSpace(string(nameOut)), strings.TrimSpace(string(commandOut)), startTime, nil
}

func windowsProcessCommand(pid int) (string, error) {
	command := fmt.Sprintf(
		"(Get-CimInstance Win32_Process -Filter 'ProcessId = %d').CommandLine",
		pid,
	)
	out, err := exec.Command(
		"powershell",
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		command,
	).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func processStartTime(pid int) (int64, error) {
	if pid <= 0 {
		return 0, os.ErrInvalid
	}
	if runtime.GOOS == "windows" {
		command := fmt.Sprintf(
			"$p=Get-Process -Id %d -ErrorAction Stop; $p.StartTime.ToUniversalTime().ToString('o')",
			pid,
		)
		out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", command).Output()
		if err != nil {
			return 0, err
		}
		started, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
		if err != nil {
			return 0, err
		}
		return started.UnixNano(), nil
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
		return 0, err
	}
	return started.UnixNano(), nil
}

func sameProcessName(actual, expected string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(actual, expected)
	}
	return actual == expected
}

func commandHasArgument(command, want string) bool {
	for _, argument := range strings.Fields(command) {
		if strings.Trim(argument, "\"'") == want {
			return true
		}
	}
	return false
}
