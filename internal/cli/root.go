package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"portx/internal/apperr"
	"portx/internal/buildinfo"
	"portx/internal/ui"
)

func Run(ctx context.Context, args []string) int {
	args = expandBareURLFlag(args)
	app := &cli.Command{
		Name:    "portx",
		Usage:   "Temporary public development URLs via Cloudflare Tunnel",
		Version: buildinfo.String(),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "log-level",
				Value: "info",
				Usage: "debug|info|warn|error",
			},
			&cli.StringFlag{
				Name:  "profile",
				Usage: "config profile (default: config default_profile or personal)",
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			level := slog.LevelInfo
			switch cmd.String("log-level") {
			case "debug":
				level = slog.LevelDebug
			case "warn":
				level = slog.LevelWarn
			case "error":
				level = slog.LevelError
			}
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
			return ctx, nil
		},
		Commands: []*cli.Command{
			httpCommand(),
			startCommand(),
			loginCommand(),
			setupCommand(),
			listCommand(),
			stopCommand(),
			doctorCommand(),
			configCommand(),
			daemonCommand(),
			cloudflaredCommand(),
			cleanupCommand(),
			resetCommand(),
			{
				Name:  "version",
				Usage: "Print version",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Println(buildinfo.String())
					return nil
				},
			},
		},
	}

	if err := app.Run(ctx, args); err != nil {
		var shown ui.ShownError
		if errors.As(err, &shown) {
			// TUI already rendered the error
			return apperr.ExitCode(shown.Err)
		}
		ui.PrintError(err)
		return apperr.ExitCode(err)
	}
	return apperr.ExitOK
}

// expandBareURLFlag turns `portx http 3000 --url` into `--url=` so managed
// mode can infer the hostname from the repo/folder name.
func expandBareURLFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--url" {
			nextMissing := i+1 >= len(args)
			nextIsFlag := !nextMissing && strings.HasPrefix(args[i+1], "-")
			if nextMissing || nextIsFlag {
				out = append(out, "--url=")
				continue
			}
		}
		out = append(out, a)
	}
	return out
}
