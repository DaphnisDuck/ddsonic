package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeConfigFile writes body as the config file in a scratch config
// folder (CLIAMP_CONFIG_DIR, which wins over XDG_CONFIG_HOME and HOME), under
// a scratch HOME, and returns its path.
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(t.TempDir(), "config")
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.toml")
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

// TestWritersReplaceTOMLStatements: Save and saveSectionValue replace a
// key's whole statement, a value over several lines included, and keep every
// other byte: unrelated and unknown keys, comments, a comment after a
// replaced one-line value (#75). A file the parser refuses is still edited
// line by line, never replaced by the defaults.
func TestWritersReplaceTOMLStatements(t *testing.T) {
	tests := []struct {
		name, initial string
		save          func() error
		want          string
	}{
		{"one-line value keeps its comment",
			"volume = 1 # loud\ntheme = \"x\" # mine\nmystery = 7\n",
			func() error { return Save("volume", "-3") },
			"volume = -3 # loud\ntheme = \"x\" # mine\nmystery = 7\n"},
		{"a string over several lines is not scanned as lines",
			"notes = \"\"\"\n[not a header]\nvolume = 9\n\"\"\"\nvolume = 1\n[ddsonic]\n",
			func() error { return Save("volume", "-3") },
			"notes = \"\"\"\n[not a header]\nvolume = 9\n\"\"\"\nvolume = -3\n[ddsonic]\n"},
		{"a new key goes above a header with a comment",
			"volume = 1\n[ddsonic] # mine\nborder = 1\n",
			func() error { return Save("theme", `"nord"`) },
			"volume = 1\ntheme = \"nord\"\n[ddsonic] # mine\nborder = 1\n"},
		{"a section's value over several lines is replaced whole",
			"# top\n[mixcloud] # mine\nstyles = [\n  \"a\", # first\n  \"b\",\n]\nother = 1 # keep\n\n[radio]\ncountry = \"US\"\n",
			func() error { return SaveMixcloudStyles([]string{"c"}) },
			"# top\n[mixcloud] # mine\nstyles = [\"c\"]\nother = 1 # keep\n\n[radio]\ncountry = \"US\"\n"},
		{"a section's one-line value keeps its comment",
			"[radio]\ncountry = \"US\" # home\n",
			func() error { return SaveRadioCountry("DE") },
			"[radio]\ncountry = \"DE\" # home\n"},
		{"a new key goes at the end of its section",
			"[radio] # mine\nlist = [\n  1,\n]\n\n[ddsonic]\n",
			func() error { return SaveRadioCountry("DE") },
			"[radio] # mine\nlist = [\n  1,\n]\n\ncountry = \"DE\"\n[ddsonic]\n"},
		{"a quoted dotted name is not the section",
			"[\"radio.x\"]\ncountry = \"US\"\n\"a.b\" = 2\n",
			func() error { return SaveRadioCountry("DE") },
			"[\"radio.x\"]\ncountry = \"US\"\n\"a.b\" = 2\n\n[radio]\ncountry = \"DE\""},
		{"a quoted dotted name is not that dotted table",
			"[\"a.b\"]\nk = 1\n[a.b]\nk = 2\n",
			func() error { return saveSectionValue("a.b", "k", "3") },
			"[\"a.b\"]\nk = 1\n[a.b]\nk = 3\n"},
		{"a quoted dotted key is not the key",
			"\"volume.x\" = 1\nvolume = 2\n",
			func() error { return Save("volume", "-3") },
			"\"volume.x\" = 1\nvolume = -3\n"},
		{"a broken file is edited line by line",
			"volume = 1\n[[[ broken\n",
			func() error { return Save("volume", "-3") },
			"volume = -3\n[[[ broken\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfigFile(t, tt.initial)
			if err := tt.save(); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
			if checkSyntax(path, []byte(tt.initial)) == nil {
				if err := checkSyntax(path, got); err != nil {
					t.Errorf("a valid file became invalid: %v", err)
				}
			}
		})
	}
}
