package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"portx/internal/config"
	"portx/internal/rpc"
)

func EnsureRunning(ctx context.Context, profile string) (*rpc.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
	logFile, err := openDaemonLog(logPath)
	if err != nil {
		return nil, fmt.Errorf("open daemon log: %w", err)
	}
	// The daemon must outlive the CLI session that started it. Startup
	// cancellation is handled explicitly below; tying the process to ctx would
	// force-kill it before its deferred cloudflared cleanup can run.
	cmd := newDaemonProcess(self, profile)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, err
	}
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
		_ = logFile.Close()
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if c, err := dialDaemon(sock, profile); err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			stopStartedDaemon(cmd, waitDone)
			return nil, ctx.Err()
		case <-deadline.C:
			stopStartedDaemon(cmd, waitDone)
			hint := ""
			if b, err := os.ReadFile(logPath); err == nil && len(b) > 0 {
				tail := string(b)
				if len(tail) > 400 {
					tail = tail[len(tail)-400:]
				}
				hint = "\n\nDaemon log:\n" + tail
			}
			return nil, fmt.Errorf("daemon did not become ready (is proxy port free?)%s\n\nSee: %s", hint, logPath)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func newDaemonProcess(executable, profile string) *exec.Cmd {
	return exec.Command(executable, "--profile", profile, "daemon", "run")
}

func openDaemonLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("daemon log path is not a regular file: %q", path)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func stopStartedDaemon(cmd *exec.Cmd, waitDone <-chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
	}
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
