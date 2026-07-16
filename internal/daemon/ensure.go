package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"portx/internal/config"
	"portx/internal/rpc"
)

func EnsureRunning(profile string) (*rpc.Client, error) {
	runtimeDir, err := config.RuntimeDir()
	if err != nil {
		return nil, err
	}
	sock := filepath.Join(runtimeDir, "portxd.sock")
	logPath := filepath.Join(runtimeDir, "portxd.log")

	if c, err := dialDaemon(sock, profile); err == nil {
		return c, nil
	}

	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logFile, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	cmd := exec.Command(self, "--profile", profile, "daemon", "run")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := dialDaemon(sock, profile); err == nil {
			return c, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	hint := ""
	if b, err := os.ReadFile(logPath); err == nil && len(b) > 0 {
		tail := string(b)
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		hint = "\n\nDaemon log:\n" + tail
	}
	return nil, fmt.Errorf("daemon did not become ready (is proxy port free?)%s\n\nSee: %s", hint, logPath)
}

func dialDaemon(sock, profile string) (*rpc.Client, error) {
	c, err := rpc.Dial(sock)
	if err != nil {
		return nil, err
	}
	st, err := c.GetStatus()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	profileMismatch := st.Profile != "" && profile != "" && st.Profile != profile
	if profileMismatch {
		_ = c.Close()
		return nil, fmt.Errorf(
			"daemon is running for profile %q, but you requested %q\n\n  portx daemon stop\n  portx --profile %s http …",
			st.Profile, profile, profile)
	}
	return c, nil
}
