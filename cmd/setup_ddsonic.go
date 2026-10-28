package cmd

// ddsonic: setup as ddsonic 1.0 ships it (docs/ddsonic/cli-audit.md). Upstream's
// wizard and its provider list stay whole, so its tests and merges keep
// working; ddsonic offers only what it uses: Spotify, YouTube Music and the
// Local music folder. A rerun keeps the keys of a section the wizard doesn't
// manage (owner, 2026-10-02), and every write replaces config.toml
// atomically. setup.go calls in through tagged lines.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
)

// newDdsonicSetupModel is upstream's wizard with ddsonic's three entries.
func newDdsonicSetupModel() *setupModel {
	m := newSetupModel()
	m.provs = ddsonicSetupProviders()
	return m
}

// ddsonicSetupProviders lists what ddsonic sets up, Spotify and YouTube Music
// reworded from upstream's specs.
func ddsonicSetupProviders() []providerSpec {
	var spotify, ytmusic providerSpec
	for _, p := range providers() {
		switch p.key {
		case "spotify":
			spotify = p
		case "ytmusic":
			ytmusic = p
		}
	}

	spotify.intro = []string{
		"Your Spotify library: albums, artists, playlists and liked songs.",
		"Requires a Spotify Premium account.",
		"",
		"Recommended: your own Spotify Developer app, from",
		"developer.spotify.com/dashboard, with the redirect URI",
		"http://127.0.0.1:19872/login. Its client_id gives ddsonic a quota",
		"of its own for syncing your library and searching.",
		"",
		"Otherwise ddsonic uses the built-in client_id shared by every",
		"librespot-based player, which Spotify rate-limits more often.",
		"",
		"You sign in from ddsonic's library: open Spotify there and",
		"press Enter on \"Sign in to Spotify\".",
	}
	spotify.save = mergeSave(spotify)

	ytmusic.intro = []string{
		"Your YouTube Music playlists and liked music. Needs yt-dlp.",
		"Saved Library Albums and followed artists are not synchronized:",
		"\"Albums from tracks\" and \"Artists from tracks\" group what those",
		"tracks name, and albums may be incomplete.",
		"",
		"Browser cookies need no setup beyond a browser signed in to",
		"YouTube. On Linux, name a Chrome-family browser with its keyring,",
		"e.g. brave+gnomekeyring (see docs/ddsonic/youtube.md).",
		"Your own Google OAuth client is the other way; you can use both.",
	}
	ytSave := mergeSave(ytmusic)
	ytmusic.save = func(v map[string]string) (string, error) {
		text, err := ytSave(v)
		if err == nil && v[keyYTMusicMode] == "custom" {
			text += " Now run: " + appdir.Name + " youtube signin"
		}
		return text, err
	}

	return []providerSpec{spotify, ytmusic, localSetupSpec()}
}

// localSetupSpec sets the folder Local's Albums, Artists and Genres are
// indexed from: initial_directory, a top-level key. The folder is checked
// in the background, like a server, but a failed check is final: only a
// readable folder is saved.
func localSetupSpec() providerSpec {
	current := currentMusicDir()
	return providerSpec{
		key:  "local",
		name: "Local music folder",
		intro: []string{
			"The folder ddsonic indexes for Local's albums, artists and genres.",
			"",
			"Now: " + current,
		},
		fields: []fieldSpec{
			{key: "initial_directory", label: "Music folder", defaultV: current,
				help: "leave blank to keep " + current + "; ~ is your home folder"},
		},
		extraValidate: func(v map[string]string) error {
			_, err := folderPath(v["initial_directory"])
			return err
		},
		validate: func(v map[string]string) error {
			dir, err := folderPath(v["initial_directory"])
			if err != nil {
				return err
			}
			return readableFolder(dir)
		},
		strict: true,
		save: func(v map[string]string) (string, error) {
			dir, err := folderPath(v["initial_directory"])
			if err != nil {
				return "", err
			}
			if err := setTopLevelKey("initial_directory", config.QuoteString(dir)); err != nil {
				return "", err
			}
			return "Saved initial_directory = " + dir + ".", nil
		},
	}
}

// currentMusicDir is the folder ddsonic indexes now. A config.toml that
// doesn't load leaves the fallbacks.
func currentMusicDir() string {
	cfg, err := config.Load()
	if err != nil {
		return local.MusicDir("")
	}
	return local.MusicDir(cfg.InitialDirectory)
}

// folderPath is a typed folder made absolute, with ~ expanded, without
// touching the disk. config.toml's reader strips the quotes around a value
// and decodes no escapes, so the path is written between plain quotes; one
// that wouldn't read back the same is refused.
func folderPath(input string) (string, error) {
	dir := local.ExpandPath(strings.TrimSpace(input))
	if dir == "" {
		return "", errors.New("a music folder is required")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	// Checked on the path as written: Abs drops a trailing slash.
	if strings.ContainsAny(dir, "\r\n") || strings.HasSuffix(dir, `"`) || strings.HasSuffix(dir, "'") {
		return "", errors.New("config.toml can't hold that folder's name (a line break, or a quote at the end)")
	}
	return dir, nil
}

// readableFolder checks that dir is a folder ddsonic can list, reading one
// entry rather than the whole folder.
func readableFolder(dir string) error {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s does not exist", dir)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s is not a folder", dir)
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("%s can't be read: %w", dir, err)
	}
	defer f.Close()
	if _, err := f.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s can't be read: %w", dir, err)
	}
	return nil
}

// sectionAliases are the other headers config.Load reads as a section.
var sectionAliases = map[string][]string{"ytmusic": {"yt", "youtube"}}

// mergeSave saves spec's section, managing only its own keys: the fields'
// and the body's. A managed key the body leaves out (client_id after
// switching to the shared one) is removed; every other line stays.
func mergeSave(spec providerSpec) func(map[string]string) (string, error) {
	return func(v map[string]string) (string, error) {
		body := spec.body(v)
		managed := map[string]bool{}
		for _, f := range spec.fields {
			managed[f.key] = true
		}
		for _, line := range strings.Split(body, "\n") {
			if k, ok := tomlKey(line); ok {
				managed[k] = true
			}
		}
		names := append([]string{spec.section}, sectionAliases[spec.section]...)
		header, err := mergeSection(names, body, managed)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Saved %s section.", header), nil
	}
}

// mergeSection writes body into the last section named any of names
// (a new names[0] when there is none), keeping the rest of the file.
// config.Load reads the sections in file order, and a header alone turns
// YouTube Music on, so the last one decides; the others lose their managed
// keys too, and keep their other lines. It returns the header it wrote
// under.
func mergeSection(names []string, body string, managed map[string]bool) (string, error) {
	path, err := configFilePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	bodyLines := strings.Split(strings.TrimRight(body, "\n"), "\n")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}

	named := func(line string) bool {
		return isHeader(line) && slices.ContainsFunc(names, func(n string) bool {
			return strings.EqualFold(strings.TrimSpace(line), "["+n+"]")
		})
	}
	last := -1 // the last matching header, where the body goes
	for i, line := range lines {
		if named(line) {
			last = i
		}
	}

	var out []string
	header := ""
	for i := 0; i < len(lines); {
		at, line := i, lines[i]
		i++
		if !named(line) {
			out = append(out, line)
			continue
		}
		// A matching section: its lines up to the next header.
		end := i
		for end < len(lines) && !isHeader(lines[end]) {
			end++
		}
		section := lines[i:end]
		i = end
		trail := len(section) // blank lines before the next section stay last
		for trail > 0 && strings.TrimSpace(section[trail-1]) == "" {
			trail--
		}
		out = append(out, line)
		for _, l := range section[:trail] {
			if k, ok := tomlKey(l); !ok || !managed[k] {
				out = append(out, l)
			}
		}
		if at == last {
			header = strings.TrimSpace(line)
			out = append(out, bodyLines...)
		}
		out = append(out, section[trail:]...)
	}
	if header == "" {
		header = "[" + names[0] + "]"
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(append(out, header), bodyLines...)
	}
	return header, writeConfigAtomic(path, out)
}

func isHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// setTopLevelKey sets a key above the first section: in place when it is
// there, else after the last top-level line, so a blank line before the
// first section stays.
func setTopLevelKey(key, value string) error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	line := key + " = " + value
	var lines []string
	if len(data) > 0 {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	first := slices.IndexFunc(lines, func(l string) bool {
		t := strings.TrimSpace(l)
		return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
	})
	if first < 0 {
		first = len(lines)
	}
	at := 0 // after the last non-blank top-level line
	for i, l := range lines[:first] {
		if k, ok := tomlKey(l); ok && k == key {
			lines[i] = line
			return writeConfigAtomic(path, lines)
		}
		if strings.TrimSpace(l) != "" {
			at = i + 1
		}
	}
	insert := []string{line}
	if at == first && first < len(lines) {
		insert = append(insert, "") // directly above a section header
	}
	return writeConfigAtomic(path, slices.Insert(lines, at, insert...))
}

// tomlKey is the key of a "key = value" line, or ok=false for anything else
// (comments, blank lines, headers).
func tomlKey(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return "", false
	}
	k, _, ok := strings.Cut(t, "=")
	return strings.TrimSpace(k), ok
}

func writeConfigAtomic(path string, lines []string) error {
	return fileutil.WriteFileAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// persist saves the active spec: its own way when it has one (ddsonic's
// entries), else upstream's section rewrite.
func (m *setupModel) persist(spec providerSpec) (string, error) {
	if spec.save != nil {
		return spec.save(m.values)
	}
	if err := saveSection(spec.section, spec.body(m.values)); err != nil {
		return "", err
	}
	return fmt.Sprintf("Saved [%s] section.", spec.section), nil
}
