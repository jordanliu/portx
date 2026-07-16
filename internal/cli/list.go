package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/config"
	"portx/internal/rpc"
	"portx/internal/ui"
)

func listCommand() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List active leases",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			runtimeDir, err := config.RuntimeDir()
			if err != nil {
				return err
			}
			c, err := rpc.Dial(filepath.Join(runtimeDir, "portxd.sock"))
			if err != nil {
				ui.Dim("No active leases (daemon not running).")
				return nil
			}
			defer c.Close()
			list, err := c.ListLeases()
			if err != nil {
				return err
			}
			if len(list) == 0 {
				ui.Dim("No active leases.")
				return nil
			}
			ui.Title("Active leases")
			for _, l := range list {
				id := l.ID
				if len(id) > 8 {
					id = id[:8]
				}
				ui.KeyValue(id, fmt.Sprintf("%s  →  %s  pid=%d  exp=%s",
					l.Hostname, l.Target, l.OwnerPID, l.ExpiresAt.Format(time.RFC3339)))
			}
			return nil
		},
	}
}
