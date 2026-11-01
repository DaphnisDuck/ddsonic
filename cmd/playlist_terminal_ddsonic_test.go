package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostileTitle carries an OSC 52 clipboard write ended by BEL, a C1 CSI and
// a DCS string, the shapes a terminal acts on rather than prints.
const hostileTitle = "AUDIT\x1b]52;c;RkFLRQ==\x07TITLE\u009b2J\x1bPtmux;x\x1b\\END"

// assertTerminalSafe fails when out holds a control character other than a
// line break. Failures quote the output, so nothing raw reaches the terminal.
func assertTerminalSafe(t *testing.T, out string) {
	t.Helper()
	for i, r := range out {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("output has control character %U at byte %d: %q", r, i, out)
		}
	}
}

func importHostile(t *testing.T, home, name, title string) {
	t.Helper()
	m3u := filepath.Join(home, name+".m3u")
	body := "#EXTM3U\n#EXTINF:1," + title + "\nhttps://example.invalid/never-fetched.mp3\n"
	if err := os.WriteFile(m3u, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error { return PlaylistImport(m3u, name) }); err != nil {
		t.Fatalf("PlaylistImport: %v", err)
	}
}

func TestPlaylistShowDropsControlSequences(t *testing.T) {
	home := setupTestEnv(t)
	importHostile(t, home, "audit", hostileTitle)

	out, err := captureStdout(t, func() error { return PlaylistShow("audit", false) })
	if err != nil {
		t.Fatalf("PlaylistShow: %v", err)
	}
	assertTerminalSafe(t, out)
	if want := "AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END"; !strings.Contains(out, want) {
		t.Errorf("show output = %q, want the visible text %q", out, want)
	}

	// The stored metadata and the JSON form keep the title byte for byte.
	prov, err := newProvider()
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := prov.Tracks("audit")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("stored tracks: %d, %v; want 1", len(tracks), err)
	}
	if tracks[0].Title != hostileTitle {
		t.Fatalf("stored title = %q, want %q", tracks[0].Title, hostileTitle)
	}
	js, err := captureStdout(t, func() error { return PlaylistShow("audit", true) })
	if err != nil {
		t.Fatalf("PlaylistShow json: %v", err)
	}
	var got []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(js), &got); err != nil || len(got) != 1 || got[0].Title != hostileTitle {
		t.Fatalf("json = %q (%v), want the title unchanged", js, err)
	}
}

func TestPlaylistBookmarksDropControlSequences(t *testing.T) {
	home := setupTestEnv(t)
	importHostile(t, home, "audit", hostileTitle)

	out, err := captureStdout(t, func() error { return PlaylistBookmark("audit", 1) })
	if err != nil {
		t.Fatalf("PlaylistBookmark: %v", err)
	}
	assertTerminalSafe(t, out)

	out, err = captureStdout(t, PlaylistBookmarks)
	if err != nil {
		t.Fatalf("PlaylistBookmarks: %v", err)
	}
	assertTerminalSafe(t, out)
	if !strings.Contains(out, "AUDIT]52;c;RkFLRQ==TITLE") {
		t.Errorf("bookmarks output = %q, want the visible title", out)
	}
}

func TestPlaylistShowKeepsOrdinaryTitles(t *testing.T) {
	home := setupTestEnv(t)
	title := "Björk - Jóga ★ 日本語 (Live) [2026] 100% & \"quotes\""
	importHostile(t, home, "plain", title)

	out, err := captureStdout(t, func() error { return PlaylistShow("plain", false) })
	if err != nil {
		t.Fatalf("PlaylistShow: %v", err)
	}
	want := "Playlist: plain (1 tracks)\n\n    1. " + title + "\n"
	if out != want {
		t.Errorf("show output = %q, want %q", out, want)
	}
}
