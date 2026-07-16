package cli

import (
	"context"
	"os"
	"path/filepath"
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
	if c, err := rpc.Dial(sock); err == nil {
		_, _ = c.GetStatus()
		_ = c.Close()
		if !cmd.Bool("force") {
			ui.Warn("daemon is running; stop it first or pass --force")
			return apperr.New(apperr.ExitCleanupWarning, "daemon still running")
		}
		interruptDaemonPID(filepath.Join(runtimeDir, "portxd.pid"))
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

	for _, name := range []string{"portxd.sock", "portxd.pid", "portxd.lock", "cloudflared.pid"} {
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

func interruptDaemonPID(pidFile string) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return
	}
	_ = procutil.Interrupt(pid)
	time.Sleep(500 * time.Millisecond)
}
