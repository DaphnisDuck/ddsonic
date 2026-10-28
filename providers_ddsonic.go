package main

// ddsonic: the providers ddsonic 1.0 supports. The rest are cliamp's, and the
// owner removed their entry points for 1.0 (docs/ddsonic/cli-audit.md): their
// sections stay in config.toml untouched, but ddsonic constructs none of them,
// and neither the library, --provider, IPC nor a hotkey reaches them. Plugins
// go the same way. main.go calls in through tagged lines.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/ui/model"
)

// supportedProviders are the provider keys ddsonic 1.0 ships; "cliamp" is
// cliamp radio's channels, which ddsonic keeps.
var supportedProviders = []string{"cliamp", "radio", "local", "spotify", "ytmusic"}

// startProviders are the supported providers ddsonic can start in (--provider,
// provider = in config.toml): upstream never offered Local as one.
var startProviders = []string{"cliamp", "radio", "spotify", "ytmusic"}

// hideProviders turns the unsupported providers off in cfg, in memory, so
// main constructs none of them. A start provider ddsonic doesn't offer falls
// back to the default.
func hideProviders(cfg *config.Config) {
	cfg.Navidrome = config.NavidromeConfig{}
	cfg.Lyrion = config.LyrionConfig{}
	cfg.Plex = config.PlexConfig{}
	cfg.Jellyfin = config.JellyfinConfig{}
	cfg.Emby = config.EmbyConfig{}
	cfg.Audiobookshelf = config.AudiobookshelfConfig{}
	cfg.Qobuz = config.QobuzConfig{}
	cfg.Tidal = config.TidalConfig{}
	cfg.SoundCloud = config.SoundCloudConfig{}
	cfg.Mixcloud = config.MixcloudConfig{}
	cfg.NetEase = config.NetEaseConfig{}
	cfg.Yandex = config.YandexConfig{}
	if !slices.Contains(startProviders, cfg.Provider) {
		cfg.Provider = ""
	}
}

// supportedOnly drops the providers main builds whatever the config says:
// the podcast directory, YouTube's non-music views ("yt", "youtube"), and
// servers named by environment variables (NAVIDROME_*, LYRION_*).
func supportedOnly(providers []model.ProviderEntry) []model.ProviderEntry {
	return slices.DeleteFunc(providers, func(p model.ProviderEntry) bool {
		return !slices.Contains(supportedProviders, p.Key)
	})
}

// ddsonicOperations is the IPC registry without what 1.0 removed: mono (the
// UI dropped it in M8) and the plugin operations (no plugins load).
func ddsonicOperations() *ipc.OperationRegistry {
	ops := ipc.DefaultOperationRegistry()
	ops.Unregister("mono", "plugin.call", "plugin.commands")
	return ops
}

// validProvider is --provider's validator: only the start providers.
func validProvider(v string) error {
	if slices.Contains(startProviders, strings.ToLower(v)) {
		return nil
	}
	return fmt.Errorf("--provider must be %s (got %q)", strings.Join(startProviders, ", "), v)
}
