package main

// ddsonic: subcommands of its own. Kept out of commands.go so upstream
// merges there stay conflict-free; commands.go registers them with one
// tagged line.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/ytmusic"
)

// youtubeSignInTimeout bounds waiting for the browser sign-in.
const youtubeSignInTimeout = 5 * time.Minute

// youtubeCommand is "ddsonic youtube": YouTube Music account commands.
func youtubeCommand() *cli.Command {
	return &cli.Command{
		Name:  "youtube",
		Usage: "YouTube Music account",
		Commands: []*cli.Command{{
			Name:  "signin",
			Usage: "sign in with your own Google OAuth client (client_id and client_secret under [ytmusic])",
			Flags: []cli.Flag{&cli.BoolFlag{
				Name:  "force",
				Usage: "sign in through the browser even if already signed in, e.g. to switch Google accounts",
			}},
			Action: func(ctx context.Context, cmd *cli.Command) error {
				cfg, err := config.Load()
				if err != nil {
					return fmt.Errorf("load config: %w", err)
				}
				id, secret := strings.TrimSpace(cfg.YouTubeMusic.ClientID), strings.TrimSpace(cfg.YouTubeMusic.ClientSecret)
				if id == "" || secret == "" {
					return errors.New("set client_id and client_secret under [ytmusic] in config.toml first (see docs/ddsonic/youtube.md)")
				}
				fmt.Println("Signing in to Google. If your browser opens, approve read-only access there.")
				ctx, cancel := context.WithTimeout(ctx, youtubeSignInTimeout)
				defer cancel()
				signIn := ytmusic.NewSession
				if cmd.Bool("force") {
					signIn = ytmusic.NewSessionForced
				}
				sess, err := signIn(ctx, id, secret)
				if err != nil {
					return fmt.Errorf("sign in: %w", err)
				}
				sess.Close()
				fmt.Println("Signed in.")
				return nil
			},
		}},
	}
}
