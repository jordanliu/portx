package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"portx/internal/procutil"
)

func TestDaemonProcessIsNotBoundToCallerContext(t *testing.T) {
	t.Parallel()

	cmd := newDaemonProcess("portx", "personal")
	if cmd.Cancel != nil {
		t.Fatal("daemon process has context cancellation configured")
	}
}

func TestRecoverCloudflaredStopsVerifiedOrphan(t *testing.T) {
	if os.Getenv("PORTX_CLOUDFLARED_HELPER") == "1" {
		time.Sleep(time.Minute)
		return
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		executable,
		"-test.run=TestRecoverCloudflaredStopsVerifiedOrphan",
		"--",
		"tunnel",
		"run",
	)
	cmd.Env = append(os.Environ(), "PORTX_CLOUDFLARED_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	t.Cleanup(func() {
		if procutil.Alive(cmd.Process.Pid) {
			_ = cmd.Process.Kill()
		}
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	})

	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "cloudflared.pid")
	if err := persistPIDRecord(
		pidPath,
		cmd.Process.Pid,
		executable,
		"cloudflared",
	); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{runtimeDir: runtimeDir}
	if err := d.recoverCloudflared(); err != nil {
		t.Fatal(err)
	}
	if procutil.Alive(cmd.Process.Pid) {
		t.Fatalf("cloudflared helper pid %d is still alive", cmd.Process.Pid)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("cloudflared pid file still exists: %v", err)
	}
}

func TestRecoverCloudflaredRemovesUnverifiedProcessRecord(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "cloudflared.pid")
	if err := persistPIDRecord(
		pidPath,
		os.Getpid(),
		executable,
		"cloudflared",
	); err != nil {
		t.Fatal(err)
	}
	d := &Daemon{runtimeDir: runtimeDir}
	if err := d.recoverCloudflared(); err != nil {
		t.Fatal(err)
	}
	if !procutil.Alive(os.Getpid()) {
		t.Fatal("recovery stopped the unverified process")
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("unverified cloudflared pid file still exists: %v", err)
	}
}

func TestRecoverCloudflaredRemovesLegacyPIDFile(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "cloudflared.pid")
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{runtimeDir: runtimeDir}
	if err := d.recoverCloudflared(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("legacy cloudflared pid file still exists: %v", err)
	}
}

func TestRecoverCloudflaredRemovesReusedPIDRecord(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "cloudflared.pid")
	record, err := newPIDRecord(os.Getpid(), executable, "cloudflared")
	if err != nil {
		t.Fatal(err)
	}
	record.StartTime++
	if err := writePIDRecord(pidPath, record); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{runtimeDir: runtimeDir}
	if err := d.recoverCloudflared(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("stale cloudflared pid file still exists: %v", err)
	}
}
