package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/config"
)

// setupHome isolates config.toml in a temp dir, writing initial when it
// isn't empty, and returns the file's path.
func setupHome(t *testing.T, initial string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "config")
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_MUSIC_DIR", "")
	path := filepath.Join(dir, "config.toml")
	if initial != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// fillSetup runs one entry of ddsonic's wizard as a user would: choose it,
// pick an option (when it has a picker), type the values, submit.
func fillSetup(t *testing.T, key, pick string, values map[string]string) *setupModel {
	t.Helper()
	m := newDdsonicSetupModel()
	idx := slices.IndexFunc(m.provs, func(p providerSpec) bool { return p.key == key })
	if idx < 0 {
		t.Fatalf("no %q in setup", key)
	}
	m.menuCursor = idx
	m.handleKey(keyPress(tea.KeyEnter, ""))
	if p := m.provs[idx].picker; p != nil {
		m.pickerCursor = slices.IndexFunc(p.options, func(o pickerOption) bool { return o.value == pick })
		if m.pickerCursor < 0 {
			t.Fatalf("no option %q", pick)
		}
		m.handleKey(keyPress(tea.KeyEnter, ""))
	}
	if m.stage == stageForm {
		for k, v := range values {
			m.values[k] = v
		}
		m.submitForm()
	}
	if m.stage == stageValidating { // the check runs as a command; deliver it
		m.Update(runValidateCmd(m.provs[m.pidx], m.values)())
	}
	return m
}

func readConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDdsonicSetupOffersOnlyDdsonicSources(t *testing.T) {
	setupHome(t, "")
	var keys []string
	for _, p := range newDdsonicSetupModel().provs {
		keys = append(keys, p.key)
	}
	if want := []string{"spotify", "ytmusic", "local"}; !slices.Equal(keys, want) {
		t.Errorf("setup offers %v, want %v", keys, want)
	}
}

func TestDdsonicSetupFresh(t *testing.T) {
	path := setupHome(t, "")
	m := fillSetup(t, "spotify", "default", nil)
	if m.saveFailed != nil || m.resultErr != nil {
		t.Fatalf("save failed: %v %v", m.saveFailed, m.resultErr)
	}
	if got := readConfig(t, path); got != "[spotify]\nbitrate   = 320\n" {
		t.Errorf("config =\n%s", got)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", info.Mode().Perm())
		}
	}
}

// TestDdsonicSetupRerunKeepsOtherKeys: an existing config and a rerun. The
// wizard's own keys change (client_id goes with the switch to the shared
// client); everything else stays where it was.
func TestDdsonicSetupRerunKeepsOtherKeys(t *testing.T) {
	initial := `theme = "nord"

[spotify]
# my app
client_id = "abc"
bitrate   = 160
device_name = "desk"

[ddsonic]
spotify_refresh = "1h"
`
	path := setupHome(t, initial)
	fillSetup(t, "spotify", "default", map[string]string{"bitrate": "96"})
	want := `theme = "nord"

[spotify]
# my app
device_name = "desk"
bitrate   = 96

[ddsonic]
spotify_refresh = "1h"
`
	if got := readConfig(t, path); got != want {
		t.Errorf("config =\n%s\nwant\n%s", got, want)
	}

	// Again, the other way round: the custom client_id comes back.
	fillSetup(t, "spotify", "custom", map[string]string{"client_id": "xyz", "bitrate": "320"})
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Spotify.ClientID != "xyz" || cfg.Spotify.Bitrate != 320 || cfg.Ddsonic.SpotifyRefresh.String() != "1h0m0s" || cfg.Theme != "nord" {
		t.Errorf("after the rerun: spotify %+v, theme %q, refresh %v", cfg.Spotify, cfg.Theme, cfg.Ddsonic.SpotifyRefresh)
	}
}

// TestDdsonicSetupWritesIntoAliasSection: [yt] and [youtube] are YouTube
// Music's section too; setup writes into it instead of adding [ytmusic].
func TestDdsonicSetupWritesIntoAliasSection(t *testing.T) {
	path := setupHome(t, "[yt]\ncookies_from = \"firefox\"\nexpand_playlist = false\n")
	fillSetup(t, "ytmusic", "cookies", map[string]string{"cookies_from": "brave+gnomekeyring"})
	got := readConfig(t, path)
	if strings.Contains(got, "[ytmusic]") || strings.Count(got, "cookies_from") != 1 ||
		!strings.Contains(got, `cookies_from = "brave+gnomekeyring"`) || !strings.Contains(got, "expand_playlist = false") {
		t.Errorf("config =\n%s", got)
	}
}

func TestDdsonicSetupYouTubeOAuthPointsToSignin(t *testing.T) {
	setupHome(t, "")
	m := fillSetup(t, "ytmusic", "custom", map[string]string{"client_id": "id", "client_secret": "secret"})
	if !strings.Contains(m.resultText, "ddsonic youtube signin") {
		t.Errorf("result %q doesn't say to sign in", m.resultText)
	}
}

func TestDdsonicSetupMissingCredentials(t *testing.T) {
	path := setupHome(t, "")
	m := fillSetup(t, "ytmusic", "custom", map[string]string{"client_secret": "secret"})
	if m.resultErr == nil || !strings.Contains(m.resultErr.Error(), "Client ID is required") {
		t.Errorf("resultErr = %v", m.resultErr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("config.toml written despite the missing client ID")
	}
}

func TestDdsonicSetupLocalOnly(t *testing.T) {
	path := setupHome(t, "[spotify]\nbitrate = 320\n")
	music := t.TempDir()
	m := fillSetup(t, "local", "", map[string]string{"initial_directory": music})
	if m.resultErr != nil || m.saveFailed != nil {
		t.Fatalf("save failed: %v %v", m.resultErr, m.saveFailed)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InitialDirectory != music || !cfg.Spotify.IsSet() {
		t.Errorf("initial_directory %q (want %q), spotify kept %v\n%s", cfg.InitialDirectory, music, cfg.Spotify.IsSet(), readConfig(t, path))
	}
}

func TestDdsonicSetupLocalDefaultsToCurrentFolder(t *testing.T) {
	setupHome(t, "")
	home := os.Getenv("HOME")
	music := filepath.Join(home, "Music")
	if err := os.Mkdir(music, 0o700); err != nil {
		t.Fatal(err)
	}
	fillSetup(t, "local", "", nil)
	cfg, _ := config.Load()
	if cfg.InitialDirectory != music {
		t.Errorf("initial_directory = %q, want %q", cfg.InitialDirectory, music)
	}

	// ~ expands, and the result is absolute.
	fillSetup(t, "local", "", map[string]string{"initial_directory": "~/Music"})
	if cfg, _ = config.Load(); cfg.InitialDirectory != music {
		t.Errorf("~/Music saved as %q", cfg.InitialDirectory)
	}
}

func TestDdsonicSetupInvalidInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, key, pick string
		values          map[string]string
		want            string
	}{
		{"missing folder", "local", "", map[string]string{"initial_directory": "/no/such/folder"}, "does not exist"},
		{"a file, not a folder", "local", "", map[string]string{"initial_directory": file}, "is not a folder"},
		{"bitrate not a number", "spotify", "default", map[string]string{"bitrate": "loud"}, "bitrate must be a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := setupHome(t, "")
			m := fillSetup(t, tt.key, tt.pick, tt.values)
			if m.resultErr == nil || !strings.Contains(m.resultErr.Error(), tt.want) {
				t.Errorf("resultErr = %v, want %q", m.resultErr, tt.want)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("config.toml written despite invalid input")
			}
		})
	}
}

// TestDdsonicSetupMalformedConfig: setup edits text, so lines config.Load
// can't make sense of survive untouched.
func TestDdsonicSetupMalformedConfig(t *testing.T) {
	initial := "volume = = 3\n[[[ broken\n\n[spotify]\nbitrate = \"oops\n"
	path := setupHome(t, initial)
	m := fillSetup(t, "spotify", "default", nil)
	if m.saveFailed != nil {
		t.Fatal(m.saveFailed)
	}
	got := readConfig(t, path)
	if !strings.HasPrefix(got, "volume = = 3\n[[[ broken\n\n[spotify]\n") || !strings.Contains(got, "bitrate   = 320") || strings.Contains(got, "oops") {
		t.Errorf("config =\n%s", got)
	}
}

// TestDdsonicSetupUnwritableConfig: the config folder can't be created (its
// parent is read-only). The wizard shows the error instead of quitting.
func TestDdsonicSetupUnwritableConfig(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user")
	}
	setupHome(t, "")
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) })
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(parent, "config"))
	for _, key := range []string{"spotify", "local"} {
		m := fillSetup(t, key, "default", map[string]string{"initial_directory": t.TempDir()})
		if m.saveFailed == nil || m.stage != stageResult {
			t.Errorf("%s: saveFailed = %v, stage %v; want the error shown", key, m.saveFailed, m.stage)
		}
	}
}

func TestDdsonicSetupCtrlCWritesNothing(t *testing.T) {
	path := setupHome(t, "")
	m := newDdsonicSetupModel()
	m.handleKey(keyPress(tea.KeyEnter, "")) // Spotify
	m.handleKey(keyPress(tea.KeyEnter, "")) // own app
	m.values["client_id"] = "abc"
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+C returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("Ctrl+C didn't quit")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("config.toml written on Ctrl+C")
	}
}

func TestDdsonicSetupKeepsSymlinkedConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	path := setupHome(t, "")
	real := filepath.Join(t.TempDir(), "dotfiles-config.toml")
	if err := os.WriteFile(real, []byte("theme = \"nord\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	fillSetup(t, "spotify", "default", nil)
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("config.toml is no longer a symlink")
	}
	if got := readConfig(t, real); !strings.Contains(got, "[spotify]") || !strings.Contains(got, `theme = "nord"`) {
		t.Errorf("link target =\n%s", got)
	}
}

func TestSetTopLevelKey(t *testing.T) {
	tests := []struct{ name, initial, want string }{
		{"empty file", "", "k = 1\n"},
		{"after top-level keys, before a section", "a = 1\n\n[yt]\nx = 2\n", "a = 1\nk = 1\n\n[yt]\nx = 2\n"},
		{"only sections", "[yt]\nx = 2\n", "k = 1\n\n[yt]\nx = 2\n"},
		{"replaced in place", "a = 1\nk = 9\n\n[s]\nk = 3\n", "a = 1\nk = 1\n\n[s]\nk = 3\n"},
		{"a section's key isn't top-level", "[s]\nk = 3\n", "k = 1\n\n[s]\nk = 3\n"},
		{"comments stay above", "# mine\n\n[s]\n", "# mine\nk = 1\n\n[s]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := setupHome(t, tt.initial)
			if err := setTopLevelKey("k", "1"); err != nil {
				t.Fatal(err)
			}
			if got := readConfig(t, path); got != tt.want {
				t.Errorf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// TestDdsonicSetupClearsEveryAliasSection: config.Load reads [ytmusic], [yt]
// and [youtube] in file order, so a later one would override the saved
// choice; each loses the wizard's keys.
func TestDdsonicSetupClearsEveryAliasSection(t *testing.T) {
	for _, order := range [][2]string{{"yt", "youtube"}, {"youtube", "ytmusic"}, {"ytmusic", "yt"}} {
		t.Run(order[0]+","+order[1], func(t *testing.T) {
			path := setupHome(t, fmt.Sprintf("[%s]\nenabled = true\ncookies_from = \"firefox\"\n\n[%s]\nenabled = true\nexpand_playlist = false\n", order[0], order[1]))
			fillSetup(t, "ytmusic", "off", nil)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			got := readConfig(t, path)
			if !cfg.YouTubeMusic.Disabled || strings.Count(got, "enabled") != 1 || strings.Contains(got, "firefox") ||
				!strings.Contains(got, "expand_playlist = false") {
				t.Errorf("YouTube Music not disabled after Disable:\n%s", got)
			}
		})
	}
}

// TestDdsonicSetupLocalRoundTrips: config.toml's reader strips the quotes and
// decodes no escapes, so the saved folder must read back unchanged.
func TestDdsonicSetupLocalRoundTrips(t *testing.T) {
	for _, name := range []string{`music\archive`, `a "quoted" name`, "with # hash", "space and ümlaut", `it's`} {
		t.Run(name, func(t *testing.T) {
			setupHome(t, "")
			dir := filepath.Join(t.TempDir(), name)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			m := fillSetup(t, "local", "", map[string]string{"initial_directory": dir})
			if m.resultErr != nil || m.saveFailed != nil {
				t.Fatalf("save failed: %v %v", m.resultErr, m.saveFailed)
			}
			if cfg, _ := config.Load(); cfg.InitialDirectory != dir {
				t.Errorf("read back %q, want %q", cfg.InitialDirectory, dir)
			}
		})
	}
	for _, bad := range []string{"line\nbreak", `ends with "`, "ends with '", "music'/", `music"//`} {
		setupHome(t, "")
		dir := t.TempDir() + "/" + bad // not filepath.Join, which drops the trailing slash
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if m := fillSetup(t, "local", "", map[string]string{"initial_directory": dir}); m.resultErr == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestDdsonicSetupChecksFolderInBackground: Enter doesn't touch the disk on
// the UI goroutine (a stalled mount would freeze the wizard, Ctrl+C too).
func TestDdsonicSetupChecksFolderInBackground(t *testing.T) {
	setupHome(t, "")
	m := newDdsonicSetupModel()
	m.menuCursor = slices.IndexFunc(m.provs, func(p providerSpec) bool { return p.key == "local" })
	m.handleKey(keyPress(tea.KeyEnter, ""))
	m.values["initial_directory"] = "/no/such/folder"
	_, cmd := m.submitForm()
	if m.stage != stageValidating || m.resultErr != nil || cmd == nil {
		t.Errorf("stage %v, resultErr %v: the folder was checked on the UI goroutine", m.stage, m.resultErr)
	}
}

// TestDdsonicSetupLocalFailureIsFinal: a failed folder check offers no "save
// anyway"; y writes nothing.
func TestDdsonicSetupLocalFailureIsFinal(t *testing.T) {
	file := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{file, "/no/such/folder"} {
		path := setupHome(t, "")
		m := fillSetup(t, "local", "", map[string]string{"initial_directory": input})
		if m.awaitingSave {
			t.Errorf("%s: offered to save anyway", input)
		}
		m.handleKey(keyPress('y', "y"))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: saved after a failed check", input)
		}
	}
}
