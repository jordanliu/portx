package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/config"
	"portx/internal/leases"
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
			for _, l := range sortedLeases(list) {
				ui.KeyValue(shortLeaseID(l.ID), fmt.Sprintf("%s  →  %s  pid=%d  exp=%s",
					leaseSelector(l), l.Target, l.OwnerPID, l.ExpiresAt.Format(time.RFC3339)))
			}
			return nil
		},
	}
}

func shortLeaseID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func leaseSelector(l leases.Lease) string {
	path := l.PathPrefix
	if path == "" {
		path = "/"
	}
	return strings.TrimSuffix(l.Hostname, ".") + path
}

func sortedLeases(list []leases.Lease) []leases.Lease {
	sorted := append([]leases.Lease{}, list...)
	sort.Slice(sorted, func(i, j int) bool {
		left := leaseSelector(sorted[i])
		right := leaseSelector(sorted[j])
		if left != right {
			return left < right
		}
		return sorted[i].ID < sorted[j].ID
	})
	return sorted
}
