package model

// ddsonic: the file browser lists directories and playable audio by default;
// f shows every file (P-42, filebrowser_audio_ddsonic.go).

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fbTree makes a directory holding each path (a trailing slash makes a
// directory) and returns it.
func fbTree(t *testing.T, paths ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fbAt returns a model whose file browser is open on dir, as Folders opens it.
func fbAt(dir string) *Model {
	m := &Model{plVisible: 4, initialDir: dir}
	m.openFileBrowser()
	return m
}

func fbNames(m *Model) []string {
	var names []string
	for i := range m.fbCount() {
		names = append(names, m.fbEntry(i).name)
	}
	return names
}

func fbPress(m *Model, key string) { m.handleFileBrowserKey(tea.KeyPressMsg{Text: key}) }

func fbCursorName(m *Model) string { return m.fbEntry(m.fileBrowser.cursor).name }

func fbMoveTo(t *testing.T, m *Model, name string) {
	t.Helper()
	i := slices.Index(fbNames(m), name)
	if i < 0 {
		t.Fatalf("%q is not listed: %q", name, fbNames(m))
	}
	m.fileBrowser.cursor = i
}

// The music tree the tests browse: audio in mixed case, artwork, a log, a
// cue sheet, dotfiles, and a folder holding no audio at all.
var fbMusic = []string{
	"Album/01 intro.mp3", "Album/cover.jpg",
	"Scans/front.png", "Scans/rip.log",
	"a.mp3", "B.FLAC", "c.Opus", "cover.jpg", "rip.log", "disc.cue", "notes.txt",
	".hidden.mp3", ".dotdir/", "Empty/",
}

func TestFileBrowserListsDirectoriesAndAudioByDefault(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	want := []string{"..", "Album/", "Empty/", "Scans/", "B.FLAC", "a.mp3", "c.Opus"}
	if got := fbNames(m); !slices.Equal(got, want) {
		t.Fatalf("listed %q, want %q", got, want)
	}
	for i := range m.fbCount() {
		if e := m.fbEntry(i); !e.isDir && !e.isAudio {
			t.Errorf("%q is listed but is neither a directory nor audio", e.name)
		}
	}
	if got := m.fbHeaderLine(); !strings.Contains(got, "(audio only, 4 hidden)") {
		t.Errorf("header %q does not say the listing is audio only, with 4 files hidden", got)
	}
	if help := m.fbHelpLine(); !strings.Contains(help, " f ") || !strings.Contains(help, "All files") {
		t.Errorf("help %q does not offer f", help)
	}
}

func TestFileBrowserFShowsAllFilesAndBack(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	fbPress(m, "f")
	want := []string{"..", "Album/", "Empty/", "Scans/", "B.FLAC", "a.mp3", "c.Opus", "cover.jpg", "disc.cue", "notes.txt", "rip.log"}
	if got := fbNames(m); !slices.Equal(got, want) {
		t.Fatalf("all files: listed %q, want %q", got, want)
	}
	if got := m.fbHeaderLine(); !strings.Contains(got, "(all files)") {
		t.Errorf("header %q does not say all files are listed", got)
	}
	if help := m.fbHelpLine(); !strings.Contains(help, "Audio only") {
		t.Errorf("help %q does not offer going back to audio only", help)
	}
	// Non-audio files are still not actionable: selecting one does nothing.
	fbMoveTo(t, m, "cover.jpg")
	fbPress(m, "space")
	if len(m.fileBrowser.selected) != 0 {
		t.Errorf("a non-audio file was selected: %v", m.fileBrowser.selected)
	}

	fbPress(m, "f")
	if got := fbNames(m); !slices.Equal(got, []string{"..", "Album/", "Empty/", "Scans/", "B.FLAC", "a.mp3", "c.Opus"}) {
		t.Errorf("back to audio only: listed %q", got)
	}
}

// Dotfiles stay hidden in both listings, as they always were.
func TestFileBrowserKeepsDotfilesHiddenInBothListings(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	for _, listing := range []string{"audio only", "all files"} {
		for _, name := range fbNames(m) {
			if strings.HasPrefix(name, ".") && name != ".." {
				t.Errorf("%s: dotfile %q is listed", listing, name)
			}
		}
		fbPress(m, "f")
	}
}

// The toggle keeps the cursor on its entry, or on the nearest one above it
// that is still listed, and keeps the selection.
func TestFileBrowserToggleKeepsCursorAndSelection(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	fbMoveTo(t, m, "a.mp3")
	fbPress(m, "space") // selects a.mp3; the cursor moves on to c.Opus
	if got := fbCursorName(m); got != "c.Opus" {
		t.Fatalf("cursor on %q after selecting, want c.Opus", got)
	}

	fbPress(m, "f")
	if got := fbCursorName(m); got != "c.Opus" {
		t.Errorf("all files: cursor on %q, want it to stay on c.Opus", got)
	}
	fbMoveTo(t, m, "notes.txt")
	fbPress(m, "f")
	if got := fbCursorName(m); got != "c.Opus" {
		t.Errorf("audio only: cursor on %q, want c.Opus, the nearest listed entry above notes.txt", got)
	}
	if !m.fileBrowser.selected[filepath.Join(m.fileBrowser.dir, "a.mp3")] || len(m.fileBrowser.selected) != 1 {
		t.Errorf("selection after two toggles = %v, want a.mp3 alone", m.fileBrowser.selected)
	}

	// The cursor stays visible when the listing gets shorter.
	m.fileBrowser.cursor, m.fileBrowser.scroll = m.fbCount()-1, m.fbCount()-1
	fbPress(m, "f")
	fbPress(m, "f")
	if c, s := m.fileBrowser.cursor, m.fileBrowser.scroll; c >= m.fbCount() || c < s || c >= s+m.fbVisible() {
		t.Errorf("cursor %d, scroll %d: not within the %d listed entries and the window of %d", c, s, m.fbCount(), m.fbVisible())
	}
}

// Moving into a folder with no audio lists it with only the way back, and
// says what it left out; .. and h come back to the folder.
func TestFileBrowserFolderWithoutAudio(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	fbMoveTo(t, m, "Scans/")
	fbPress(m, "enter")
	if got := fbNames(m); !slices.Equal(got, []string{".."}) {
		t.Fatalf("Scans: listed %q, want only ..", got)
	}
	if got := m.fbHeaderLine(); !strings.Contains(got, "(audio only, 2 hidden)") {
		t.Errorf("Scans: header %q does not say 2 files are hidden", got)
	}
	if body := m.renderFileBrowserBody(); !strings.Contains(body, "..") || strings.Contains(body, "(empty)") {
		t.Errorf("Scans: body %q, want the .. entry", body)
	}
	fbPress(m, "enter") // on ..
	if got := fbCursorName(m); got != "Scans/" {
		t.Errorf("back with ..: cursor on %q, want Scans/", got)
	}

	fbMoveTo(t, m, "Empty/")
	fbPress(m, "l")
	if got := m.fbHeaderLine(); !strings.Contains(got, "(audio only)") || strings.Contains(got, "hidden") {
		t.Errorf("Empty: header %q, want audio only with nothing hidden", got)
	}
	fbPress(m, "h")
	if got := fbCursorName(m); got != "Empty/" {
		t.Errorf("back with h: cursor on %q, want Empty/", got)
	}
}

// The choice holds as the browser moves between folders and is opened again.
func TestFileBrowserAllFilesHoldsAcrossFolders(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	fbPress(m, "f")
	fbMoveTo(t, m, "Album/")
	fbPress(m, "enter")
	if got := fbNames(m); !slices.Equal(got, []string{"..", "01 intro.mp3", "cover.jpg"}) {
		t.Errorf("Album, all files: listed %q", got)
	}
	m.fileBrowser.visible = false
	m.openFileBrowser()
	if got := fbNames(m); !slices.Contains(got, "cover.jpg") {
		t.Errorf("opened again: listed %q, want all files still", got)
	}
}

// Enter on an audio file still plays it.
func TestFileBrowserAudioOnlyStillPlays(t *testing.T) {
	m := fbAt(fbTree(t, fbMusic...))
	fbMoveTo(t, m, "B.FLAC")
	if cmd := m.handleFileBrowserKey(tea.KeyPressMsg{Text: "enter"}); cmd == nil {
		t.Fatal("Enter on an audio file returned no command")
	}
	if m.fileBrowser.visible {
		t.Error("the browser stayed open after Enter on an audio file")
	}
}
