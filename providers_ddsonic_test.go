package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/ui/model"
)

func TestHideProvidersTurnsOffUnsupported(t *testing.T) {
	cfg := config.Config{
		Provider:       "plex",
		Navidrome:      config.NavidromeConfig{URL: "http://nav", User: "u", Password: "p"},
		Lyrion:         config.LyrionConfig{URL: "http://lms"},
		Plex:           config.PlexConfig{URL: "http://plex", Token: "t"},
		Jellyfin:       config.JellyfinConfig{URL: "http://jf", Token: "t"},
		Emby:           config.EmbyConfig{URL: "http://emby", Token: "t"},
		Audiobookshelf: config.AudiobookshelfConfig{URL: "http://abs", Token: "t"},
		Qobuz:          config.QobuzConfig{Enabled: true},
		Tidal:          config.TidalConfig{Enabled: true},
		SoundCloud:     config.SoundCloudConfig{Enabled: true},
		Mixcloud:       config.MixcloudConfig{Enabled: true},
		NetEase:        config.NetEaseConfig{Enabled: true},
		Yandex:         config.YandexConfig{Enabled: true, Token: "t"},
		Plugins:        map[string]map[string]string{"now-playing": {"enabled": "true"}},
		Spotify:        config.SpotifyConfig{Enabled: true},
		YouTubeMusic:   config.YouTubeMusicConfig{Enabled: true, CookiesFrom: "brave"},
	}
	hideProviders(&cfg)

	if cfg.Navidrome.IsSet() || cfg.Lyrion.IsSet() || cfg.Plex.IsSet() || cfg.Jellyfin.IsSet() ||
		cfg.Emby.IsSet() || cfg.Audiobookshelf.IsSet() || cfg.Qobuz.IsSet() || cfg.Tidal.IsSet() ||
		cfg.SoundCloud.IsSet() || cfg.Mixcloud.Enabled || cfg.NetEase.Enabled || cfg.Yandex.Enabled {
		t.Errorf("an unsupported provider is still configured: %+v", cfg)
	}
	if cfg.Provider != "" {
		t.Errorf("Provider = %q, want the default for an unsupported one", cfg.Provider)
	}
	if !cfg.Spotify.IsSet() || !cfg.YouTubeMusic.Enabled || cfg.YouTubeMusic.CookiesFrom != "brave" {
		t.Errorf("a supported provider lost its config: spotify %+v, ytmusic %+v", cfg.Spotify, cfg.YouTubeMusic)
	}
}

func TestHideProvidersKeepsSupportedDefault(t *testing.T) {
	for _, p := range []string{"", "cliamp", "radio", "spotify", "ytmusic"} {
		cfg := config.Config{Provider: p}
		hideProviders(&cfg)
		if cfg.Provider != p {
			t.Errorf("Provider %q became %q", p, cfg.Provider)
		}
	}
	for _, p := range []string{"local", "podcast", "yt", "youtube", "navidrome", "tidal"} {
		cfg := config.Config{Provider: p}
		hideProviders(&cfg)
		if cfg.Provider != "" {
			t.Errorf("Provider %q kept, want the default", p)
		}
	}
}

func TestSupportedOnly(t *testing.T) {
	var entries []model.ProviderEntry
	for _, k := range []string{"cliamp", "radio", "local", "podcast", "navidrome", "lyrion", "spotify", "yt", "youtube", "ytmusic"} {
		entries = append(entries, model.ProviderEntry{Key: k})
	}
	var got []string
	for _, e := range supportedOnly(entries) {
		got = append(got, e.Key)
	}
	want := []string{"cliamp", "radio", "local", "spotify", "ytmusic"}
	if !slices.Equal(got, want) {
		t.Errorf("supportedOnly kept %v, want %v", got, want)
	}
}

func TestDdsonicOperationsDropRemoved(t *testing.T) {
	ops := ddsonicOperations()
	for _, name := range []string{"mono", "plugin.call", "plugin.commands"} {
		if _, ok := ops.Lookup(name); ok {
			t.Errorf("operation %q is still registered", name)
		}
	}
	for _, name := range []string{"shuffle", "repeat", "provider.list", "provider.load"} {
		if _, ok := ops.Lookup(name); !ok {
			t.Errorf("operation %q went missing", name)
		}
	}
}

// TestProviderFlagNarrowed runs --provider through ddsonicApp's validator and
// upstream's overridesFromFlags, as a real start does.
func TestProviderFlagNarrowed(t *testing.T) {
	parse := func(v string) (config.Overrides, error) {
		app := ddsonicApp()
		var ov config.Overrides
		app.Action = func(_ context.Context, c *cli.Command) error {
			var err error
			ov, err = overridesFromFlags(c)
			return err
		}
		err := app.Run(context.Background(), []string{"ddsonic", "--provider", v})
		return ov, err
	}
	// Every advertised value must survive the whole path, not just the validator.
	for _, v := range append(slices.Clone(startProviders), "Spotify") {
		ov, err := parse(v)
		if err != nil {
			t.Errorf("--provider %s: %v", v, err)
		} else if ov.Provider == nil || *ov.Provider != strings.ToLower(v) {
			t.Errorf("--provider %s: override %v", v, ov.Provider)
		}
	}
	for _, v := range []string{"local", "plex", "podcast", "yt", "abs", "tidal", ""} {
		if _, err := parse(v); err == nil {
			t.Errorf("--provider %q accepted", v)
		}
	}
}
