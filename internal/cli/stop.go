package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/config"
	"portx/internal/leases"
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
			matches := matchingLeases(list, id)
			if len(matches) == 0 {
				return apperr.New(
					apperr.ExitDaemon,
					fmt.Sprintf("no active lease matching %q", id),
				)
			}
			if len(matches) > 1 {
				return ambiguousLeaseError(id, matches)
			}
			lease := matches[0]
			if err := c.ForceRelease(lease.ID); err != nil {
				return err
			}
			ui.Success("Stopped %s", leaseSelector(lease))
			return nil
		},
	}
}

func matchingLeases(list []leases.Lease, selector string) []leases.Lease {
	exact := make([]leases.Lease, 0, 1)
	for _, lease := range list {
		if lease.ID == selector || lease.RouteID == selector || leaseSelector(lease) == selector {
			exact = append(exact, lease)
		}
	}
	if len(exact) > 0 {
		return exact
	}

	prefix := make([]leases.Lease, 0, 1)
	for _, lease := range list {
		if strings.HasPrefix(lease.ID, selector) {
			prefix = append(prefix, lease)
		}
	}
	if len(prefix) > 0 {
		return prefix
	}

	hostname := make([]leases.Lease, 0, 1)
	for _, lease := range list {
		if lease.Hostname == selector {
			hostname = append(hostname, lease)
		}
	}
	return hostname
}

func ambiguousLeaseError(selector string, matches []leases.Lease) error {
	lines := []string{
		fmt.Sprintf("ambiguous lease selector %q; choose a lease ID or route:", selector),
	}
	for _, lease := range sortedLeases(matches) {
		lines = append(lines, fmt.Sprintf("  %s  %s", shortLeaseID(lease.ID), leaseSelector(lease)))
	}
	return apperr.New(apperr.ExitInvalidArgs, strings.Join(lines, "\n"))
}
