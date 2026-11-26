package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeConfigFile writes body as the config file under a scratch HOME and
// returns its path.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadReadsTOML: Load reads the file as TOML says (#75): comments after
// headers and values, arrays over several lines, escapes, TOML's integers,
// dotted and quoted keys. Per-key types, ranges and defaults are unchanged.
func TestLoadReadsTOML(t *testing.T) {
	def := defaultConfig()
	for _, tt := range []struct {
		name  string
		toml  string
		check func(Config) any // got
		want  any
	}{
		{"comment after a header", "[ddsonic] # fork settings\nborder = false\n",
			func(c Config) any { return c.Ddsonic.Border }, false},
		{"comment after a float", "volume = -5 # quieter\n",
			func(c Config) any { return c.Volume }, -5.0},
		{"comment after a bool", "shuffle = true # c\n",
			func(c Config) any { return c.Shuffle }, true},
		{"comment after a string", "theme = \"nord\" # dark\n",
			func(c Config) any { return c.Theme }, "nord"},
		{"a # inside a string is no comment", "theme = \"a#b\" # c\n",
			func(c Config) any { return c.Theme }, "a#b"},
		{"comment in a provider section", "[navidrome] # server\nuser = \"me\" # c\n",
			func(c Config) any { return c.Navidrome.User }, "me"},
		{"multi-line eq", "eq = [\n  1, 2.5, # low\n  -3,\n]\n",
			func(c Config) any { return c.EQ }, [10]float64{1, 2.5, -3}},
		{"multi-line sleep presets", "[ddsonic]\nsleep_timer_presets_minutes = [\n  10, # short\n  1_440,\n]\n",
			func(c Config) any { return c.Ddsonic.SleepPresets }, []time.Duration{10 * time.Minute, 1440 * time.Minute}},
		{"multi-line plex libraries", "[plex]\nlibraries = [\n  \"Music\", # main\n  \"Jazz\",\n]\n",
			func(c Config) any { return c.Plex.Libraries }, []string{"Music", "Jazz"}},
		{"multi-line audiobookshelf libraries", "[audiobookshelf]\nlibraries = [\n  \"Books\",\n]\n",
			func(c Config) any { return c.Audiobookshelf.Libraries }, []string{"Books"}},
		{"multi-line mixcloud styles", "[mixcloud]\nstyles = [\n  \"house\",\n  \"jazz\", # c\n]\n",
			func(c Config) any { return c.Mixcloud.Styles }, []string{"house", "jazz"}},
		{"multi-line youtube playlists", "[ddsonic]\nyoutube_playlists = [\n  \"PL1\",\n  \"PL2\",\n]\n",
			func(c Config) any { return c.Ddsonic.YouTubePlaylists }, []string{"PL1", "PL2"}},
		{"escapes in a basic string are decoded", "[navidrome]\npassword = \"p\\\"w\\\\d\"\n",
			func(c Config) any { return c.Navidrome.Password }, `p"w\d`},
		{"a literal string is as written", "[navidrome]\npassword = 'p\\w'\n",
			func(c Config) any { return c.Navidrome.Password }, `p\w`},
		{"a quote at a string's end is kept", "theme = 'x\"'\n",
			func(c Config) any { return c.Theme }, `x"`},
		{"hex integer", "[ddsonic]\nsleep_fade_seconds = 0x10\n",
			func(c Config) any { return c.Ddsonic.SleepFade }, 16 * time.Second},
		{"plus-signed integer", "padding_horizontal = +5\n",
			func(c Config) any { return c.PaddingH }, 5},
		{"a float is no integer", "padding_horizontal = 5.0\n",
			func(c Config) any { return c.PaddingH }, def.PaddingH},
		{"a string is no number", "volume = \"-5\"\n",
			func(c Config) any { return c.Volume }, def.Volume},
		{"a string is no bool", "shuffle = \"true\"\n",
			func(c Config) any { return c.Shuffle }, false},
		{"dotted key", "ddsonic.border = false\n",
			func(c Config) any { return c.Ddsonic.Border }, false},
		{"spaces in a header", "[ ddsonic ]\nborder = false\n",
			func(c Config) any { return c.Ddsonic.Border }, false},
		{"section names ignore case", "[Navidrome]\nuser = \"me\"\n",
			func(c Config) any { return c.Navidrome.User }, "me"},
		{"plugin values", "[plugins.x] # c\nn = 5 # c\ns = \"v\" # c\na = [1,\n 2]\n",
			func(c Config) any { return c.Plugins["x"] }, map[string]string{"n": "5", "s": "v", "a": "[1, 2]"}},
		{"the device line older versions wrote", "audio_device = My Speakers\n",
			func(c Config) any { return c.AudioDevice }, "My Speakers"},
		{"a key after a dotted one is in its own table", "[ddsonic]\nx.y = 1\nborder = false\n",
			func(c Config) any { return c.Ddsonic.Border }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writeConfigFile(t, tt.toml)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if got := tt.check(cfg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestLoadRefusesInvalidTOML: a file that is not TOML gives the defaults
// and a *SyntaxError, and is left as it is: nothing writes the defaults
// over it.
func TestLoadRefusesInvalidTOML(t *testing.T) {
	for _, body := range []string{
		"expanded = True\n",                      // a bool is lower case
		"[ddsonic]\nspotify_refresh = 90s\n",     // a duration is a string
		"theme = \"nord\"\n[ddsonic\nborder = 1", // an unclosed header
	} {
		path := writeConfigFile(t, body)
		cfg, err := Load()
		var syntax *SyntaxError
		if !errors.As(err, &syntax) {
			t.Errorf("%q: err = %v, want a *SyntaxError", body, err)
		}
		if !reflect.DeepEqual(cfg, defaultConfig()) {
			t.Errorf("%q: Load gave %+v, want the defaults", body, cfg)
		}
		if got, _ := os.ReadFile(path); string(got) != body {
			t.Errorf("%q: the file became %q", body, got)
		}
	}
}
