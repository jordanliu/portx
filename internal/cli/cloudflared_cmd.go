package cli

import (
	"context"
	"runtime"

	"github.com/urfave/cli/v3"

	"portx/internal/cloudflared"
	"portx/internal/ui"
)

func cloudflaredCommand() *cli.Command {
	return &cli.Command{
		Name:  "cloudflared",
		Usage: "Check cloudflared (required external dependency)",
		Commands: []*cli.Command{
			{
				Name:  "version",
				Usage: "Show cloudflared on PATH",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					st, err := cloudflared.Lookup()
					if err != nil {
						return err
					}
					ui.KeyValue("version", st.Version)
					ui.KeyValue("path", st.Path)
					return nil
				},
			},
			{
				Name:  "install",
				Usage: "Show how to install cloudflared",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if st, err := cloudflared.Lookup(); err == nil {
						ui.Success("cloudflared %s", st.Version)
						ui.KeyValue("path", st.Path)
						return nil
					}
					ui.Title("Install cloudflared")
					ui.Info("PortX uses the cloudflared CLI from your PATH.")
					ui.Info("It does not download or embed it.")
					ui.Blank()
					if runtime.GOOS == "windows" {
						ui.Info("Download and install:")
						ui.Code(cloudflared.InstallCommand())
					} else {
						ui.Info("Install:")
						ui.Code(cloudflared.InstallCommand())
						ui.Info("Then verify:")
						ui.Code("cloudflared version")
					}
					return nil
				},
			},
			{
				Name:  "update",
				Usage: "Show how to upgrade cloudflared",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					ui.Title("Upgrade cloudflared")
					switch runtime.GOOS {
					case "darwin":
						ui.Code("brew upgrade cloudflared")
					case "windows":
						ui.Info("Download the latest release:")
						ui.Code("https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/downloads/")
					default:
						ui.Code("brew upgrade cloudflared")
					}
					return nil
				},
			},
		},
	}
}
