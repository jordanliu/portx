package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"

	"portx/internal/config"
	"portx/internal/ui"
)

func configCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Show or validate configuration",
		Commands: []*cli.Command{
			{
				Name:  "show",
				Usage: "Print global and project configuration",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "merged", Usage: "show merged global + project view"},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load()
					if err != nil {
						return err
					}
					path, _ := config.ConfigFile()
					fmt.Fprintln(os.Stderr, "global:", path)

					var project *config.ProjectConfig
					if pc, err := config.LoadProject(""); err == nil {
						project = &pc
						p, _ := config.FindProjectFile("")
						fmt.Fprintln(os.Stderr, "project:", p)
					}

					enc := yaml.NewEncoder(os.Stdout)
					enc.SetIndent(2)
					if cmd.Bool("merged") {
						view := config.MergeView(cfg, project, cmd.String("profile"))
						return enc.Encode(view)
					}
					if err := enc.Encode(cfg); err != nil {
						return err
					}
					if project != nil {
						fmt.Fprintln(os.Stdout, "---")
						return enc.Encode(project)
					}
					return nil
				},
			},
			{
				Name:  "validate",
				Usage: "Validate global and project configuration",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := config.Load()
					if err != nil {
						return err
					}
					if err := cfg.Validate(); err != nil {
						return err
					}
					ui.CheckOK("global config")
					if pc, err := config.LoadProject(""); err == nil {
						if err := pc.Validate(); err != nil {
							return err
						}
						ui.CheckOK("project config")
					}
					return nil
				},
			},
		},
	}
}
