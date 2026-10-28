package model

import "github.com/bjarneo/cliamp/internal/appdir"

// ddsonic: cliamp's empty-pane hints (view.go) for the providers ddsonic
// offers name cliamp's folder and a command neither player has. The library
// keeps cliamp's provider pane off the screen, but the hints stay correct for
// any path that still reaches it.
func init() {
	local := "Add .toml playlists to ~/.config/" + appdir.Name + "/playlists/."
	ytmusic := "Run `" + appdir.Name + " setup` to sign in, then refresh."
	providerEmptyStateHint["local playlists"] = local
	providerEmptyStateHint["local"] = local
	providerEmptyStateHint["youtube music"] = ytmusic
	providerEmptyStateHint["ytmusic"] = ytmusic
}
