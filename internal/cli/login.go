package cli

import (
	"context"
	"time"

	"github.com/urfave/cli/v3"

	"portx/internal/auth"
	"portx/internal/cloudflare"
	"portx/internal/cloudflared"
	"portx/internal/config"
	"portx/internal/credentials"
	"portx/internal/ui"
)

func loginCommand() *cli.Command {
	return &cli.Command{
		Name:  "login",
		Usage: "Log in to Cloudflare via cloudflared browser auth",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force", Usage: "ignore existing cert and run a fresh browser login"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _ := config.Load()
			profile := config.ResolveProfile(cmd.String("profile"), "", cfg.DefaultProfile)

			st, err := cloudflared.EnsureInstalled()
			if err != nil {
				return err
			}
			ui.KeyValue("cloudflared", st.Version+"  "+st.Path)

			if cmd.Bool("force") {
				if err := auth.RemoveBrowserCredentials(); err != nil {
					return err
				}
			}

			ui.Info("Opening Cloudflare in your browser…")
			loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
			defer cancel()
			res, err := auth.BrowserLogin(loginCtx)
			if err != nil {
				return err
			}
			if err := cloudflare.New(res.APIToken).VerifyToken(ctx); err != nil {
				return err
			}
			store, err := credentials.Open()
			if err != nil {
				return err
			}
			if err := store.Set(credentials.APITokenKey(profile), res.APIToken); err != nil {
				return err
			}
			ui.Success("Logged in. API token stored for profile %s", profile)
			if res.AccountID != "" {
				ui.KeyValue("Account", res.AccountID)
			}
			if res.ZoneID != "" {
				ui.KeyValue("Zone", res.ZoneID)
			}
			ui.Blank()
			ui.Info("Next:")
			ui.Code("portx setup")
			return nil
		},
	}
}
