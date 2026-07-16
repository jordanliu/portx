package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/config"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func stopCommand() *cli.Command {
	return &cli.Command{
		Name:      "stop",
		Usage:     "Stop a route/lease",
		ArgsUsage: "<route-or-lease>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return apperr.New(apperr.ExitInvalidArgs, "usage: portx stop <hostname-or-lease-id>")
			}
			id := cmd.Args().First()
			runtimeDir, err := config.RuntimeDir()
			if err != nil {
				return err
			}
			c, err := rpc.Dial(filepath.Join(runtimeDir, "portxd.sock"))
			if err != nil {
				return apperr.New(apperr.ExitDaemon, "daemon is not running")
			}
			defer c.Close()
			list, err := c.ListLeases()
			if err != nil {
				return err
			}
			for _, l := range list {
				matches := l.ID == id ||
					strings.HasPrefix(l.ID, id) ||
					l.Hostname == id ||
					l.RouteID == id
				if !matches {
					continue
				}
				if err := c.ForceRelease(l.ID); err != nil {
					return err
				}
				ui.Success("Stopped %s", l.Hostname)
				return nil
			}
			return apperr.New(apperr.ExitDaemon, fmt.Sprintf("no active lease matching %q", id))
		},
	}
}
