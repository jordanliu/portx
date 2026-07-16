package cli

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/config"
	"portx/internal/daemon"
	"portx/internal/procutil"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func daemonCommand() *cli.Command {
	return &cli.Command{
		Name:  "daemon",
		Usage: "Manage the local PortX daemon",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "Run the daemon in the foreground",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load()
					if err != nil {
						return err
					}
					profile := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)
					d, err := daemon.New(cfg, profile)
					if err != nil {
						return err
					}
					ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
					defer stop()
					return d.Run(ctx)
				},
			},
			{
				Name:  "status",
				Usage: "Show daemon status",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					runtimeDir, err := config.RuntimeDir()
					if err != nil {
						return err
					}
					c, err := rpc.Dial(filepath.Join(runtimeDir, "portxd.sock"))
					if err != nil {
						ui.KeyValue("Daemon", ui.StatusValue(false, "not running"))
						return nil
					}
					defer c.Close()
					st, err := c.GetStatus()
					if err != nil {
						return err
					}
					ui.KeyValue("Daemon", ui.StatusValue(true, "running"))
					ui.KeyValue("Proxy", st.ProxyAddr)
					ui.KeyValue("Tunnel", strconv.FormatBool(st.TunnelRunning))
					ui.KeyValue("Leases", strconv.Itoa(st.LeaseCount))
					return nil
				},
			},
			{
				Name:  "stop",
				Usage: "Stop the daemon",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					runtimeDir, err := config.RuntimeDir()
					if err != nil {
						return err
					}
					pidFile := filepath.Join(runtimeDir, "portxd.pid")
					b, err := os.ReadFile(pidFile)
					if err != nil {
						return apperr.New(apperr.ExitDaemon, "daemon not running")
					}
					pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
					if err != nil || pid <= 0 {
						return apperr.New(apperr.ExitDaemon, "invalid pid file")
					}
					if !procutil.Alive(pid) {
						_ = os.Remove(pidFile)
						return apperr.New(apperr.ExitDaemon, "daemon not running")
					}
					return procutil.Interrupt(pid)
				},
			},
		},
	}
}
