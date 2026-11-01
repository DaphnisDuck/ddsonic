package main

// ddsonic: the command-line surface ddsonic 1.0 ships (docs/ddsonic/cli-audit.md).
// Upstream's buildApp stays whole, so its tests and merges keep working;
// ddsonicApp takes out what ddsonic doesn't support and rewords what it words
// differently. main runs ddsonicApp through one tagged line.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/ipc"
)

// Removed for 1.0 by the owner (2026-10-01): plugins are unsupported, the
// hidden providers' commands go, cliamp:// links and the radio easter egg are
// upstream's, and mono left the UI in M8.
var (
	removedCommands = []string{"mono", "open", "plugins", "protocol", "qobuz", "radio", "tidal"}
	removedFlags    = []string{"daemon", "expanded", "mono", "simplified"}
)

// sourceSHA is the commit a release build was made from, set at build time
// via -ldflags "-X main.sourceSHA=<sha>" (packaging/release.sh). Empty in
// every other build.
var sourceSHA string

// ddsonicApp is buildApp as ddsonic ships it.
func ddsonicApp() *cli.Command {
	app := buildApp()
	app.Usage = "music for your terminal" // the logo's tagline, in place of cliamp's
	// A plain go build has no version; urfave hides --version when it's empty.
	if app.Version == "" {
		app.Version = "dev"
	}
	// A release build says which commit it was built from, so that the
	// binary alone identifies its source: "ddsonic version v1.0.0 (commit
	// <40 hex digits>)". Release acceptance compares it with the archive's
	// manifest and the expected release commit.
	if sourceSHA != "" {
		app.Version += " (commit " + sourceSHA + ")"
	}
	// A removed command becomes a hidden stub that says so; otherwise the
	// word would fall through to the player as a file to open.
	for i, c := range app.Commands {
		if slices.Contains(removedCommands, c.Name) {
			app.Commands[i] = removedCommand(c.Name)
		}
	}
	app.Flags = slices.DeleteFunc(app.Flags, func(f cli.Flag) bool {
		return slices.Contains(removedFlags, f.Names()[0])
	})
	for _, f := range app.Flags {
		if b, ok := f.(*cli.BoolWithInverseFlag); ok && b.Name == "help-bar" {
			b.Usage = "show the key bar at the bottom of the screen"
		}
		if s, ok := f.(*cli.StringFlag); ok && s.Name == "provider" {
			s.Usage = "default provider: " + strings.Join(startProviders, ", ") + " (cliamp is the legacy provider key for Internet Radio)"
			s.Validator = validProvider // providers_ddsonic.go
		}
	}
	if c := app.Command("setup"); c != nil {
		c.Usage = "set up Spotify, YouTube Music and your Local music folder"
		c.Description = "Writes ~/.config/" + appdir.Name + "/config.toml, keeping what else is in it."
	}
	// history's options are for listing; "history clear" takes none.
	if h := app.Command("history"); h != nil {
		for _, f := range h.Flags {
			switch f := f.(type) {
			case *cli.IntFlag:
				f.Local = true
			case *cli.BoolFlag:
				f.Local = true
			}
		}
	}
	return app
}

func removedCommand(name string) *cli.Command {
	return &cli.Command{
		Name:            name,
		Hidden:          true,
		SkipFlagParsing: true,
		Action: func(context.Context, *cli.Command) error {
			return fmt.Errorf("%q is not part of %s 1.0", name, appmeta.ClientName())
		},
	}
}

// cliError is err as ddsonic prints it: upstream's usage messages name its own
// binary ("usage: cliamp volume <dB>").
func cliError(err error) string {
	return strings.Replace(err.Error(), "usage: cliamp ", "usage: "+appmeta.ClientName()+" ", 1)
}

// checkModeName rejects a shuffle or repeat value the player wouldn't accept,
// before it goes over IPC.
func checkModeName(op, name string) error {
	if ipc.ValidModeName(op, name) {
		return nil
	}
	return fmt.Errorf("%s must be %s (got %q)", op, strings.Join(ipc.ModeNames(op), ", "), name)
}
