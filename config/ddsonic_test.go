// ddsonic: tests for the [ddsonic] config section.

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDdsonicSection(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want time.Duration
	}{
		{"default without the section", "volume = -3\n", DefaultSpotifyRefresh},
		{"quoted duration", "[ddsonic]\nspotify_refresh = \"45m\"\n", 45 * time.Minute},
		{"bare duration", "[ddsonic]\nspotify_refresh = 90s\n", 90 * time.Second},
		{"zero syncs every startup", "[ddsonic]\nspotify_refresh = \"0s\"\n", 0},
		{"invalid keeps the default", "[ddsonic]\nspotify_refresh = \"soon\"\n", DefaultSpotifyRefresh},
		{"negative keeps the default", "[ddsonic]\nspotify_refresh = \"-5m\"\n", DefaultSpotifyRefresh},
		{"top-level key is not the section's", "spotify_refresh = \"2h\"\n", DefaultSpotifyRefresh},
	}
	for _, tt := range []struct {
		name string
		toml string
		want time.Duration
	}{
		{"youtube default", "", DefaultYouTubeRefresh},
		{"youtube set", "[ddsonic]\nyoutube_refresh = \"6h\"\n", 6 * time.Hour},
		{"youtube invalid", "[ddsonic]\nyoutube_refresh = \"often\"\n", DefaultYouTubeRefresh},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Ddsonic.YouTubeRefresh != tt.want {
				t.Errorf("YouTubeRefresh = %v, want %v", cfg.Ddsonic.YouTubeRefresh, tt.want)
			}
		})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Ddsonic.SpotifyRefresh != tt.want {
				t.Errorf("SpotifyRefresh = %v, want %v", cfg.Ddsonic.SpotifyRefresh, tt.want)
			}
		})
	}
}

// A section that follows [ddsonic] gets its own keys back.
func TestDdsonicSectionDoesNotLeak(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[ddsonic]\nspotify_refresh = \"1h\"\n\n[spotify]\nbitrate = 160\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Ddsonic.SpotifyRefresh != time.Hour || !cfg.Spotify.Enabled || cfg.Spotify.Bitrate != 160 {
		t.Errorf("ddsonic %+v, spotify %+v", cfg.Ddsonic, cfg.Spotify)
	}
}

func TestLoadYouTubePlaylists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "[ddsonic]\nyoutube_playlists = [\"https://music.youtube.com/playlist?list=PLa&si=x\", \"PLb\"]\n"
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://music.youtube.com/playlist?list=PLa&si=x", "PLb"}
	if len(cfg.Ddsonic.YouTubePlaylists) != 2 || cfg.Ddsonic.YouTubePlaylists[0] != want[0] || cfg.Ddsonic.YouTubePlaylists[1] != want[1] {
		t.Errorf("YouTubePlaylists = %q, want %q", cfg.Ddsonic.YouTubePlaylists, want)
	}
}

// border and artwork are both on by default and read the same way.
func TestLoadDdsonicBooleans(t *testing.T) {
	for _, key := range []string{"border", "artwork"} {
		for _, tt := range []struct {
			name string
			val  string // "" leaves the key out
			want bool
		}{
			{"on by default", "", true},
			{"off", "false", false},
			{"on", "true", true},
			{"invalid keeps the default", `"maybe"`, true},
		} {
			t.Run(key+"/"+tt.name, func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				toml := ""
				if tt.val != "" {
					toml = "[ddsonic]\n" + key + " = " + tt.val + "\n"
				}
				if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
					t.Fatal(err)
				}
				cfg, err := Load()
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]bool{"border": cfg.Ddsonic.Border, "artwork": cfg.Ddsonic.Artwork}[key]
				if got != tt.want {
					t.Errorf("%s = %v, want %v", key, got, tt.want)
				}
			})
		}
	}
}

// TestLoadIgnoresOldDdmusSection: the section was [ddmus] up to v1.0.0-rc.1.
// ddsonic has no alias for it (docs/ddsonic/files.md says to rename the
// header by hand), so its keys must not reach the config.
func TestLoadIgnoresOldDdmusSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "[ddmus]\nspotify_refresh = \"45m\"\nyoutube_refresh = \"6h\"\nyoutube_playlists = [\"PLabc\"]\nborder = false\nartwork = false\n"
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := defaultDdsonicConfig()
	got := cfg.Ddsonic
	if got.SpotifyRefresh != want.SpotifyRefresh || got.YouTubeRefresh != want.YouTubeRefresh ||
		len(got.YouTubePlaylists) != 0 || got.Border != want.Border || got.Artwork != want.Artwork {
		t.Errorf("a [ddmus] section changed the config: got %+v, want the defaults %+v", got, want)
	}
}
