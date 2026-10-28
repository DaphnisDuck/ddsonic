package model

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// hostileText carries an OSC 52 clipboard write ended by BEL, a C1 CSI and a
// DCS string (which the cell renderer passes through to the terminal).
const hostileText = "AUDIT\x1b]52;c;RkFLRQ==\x07TITLE\u009b2J\x1bPtmux;x\x1b\\END"

// sgr matches the styling sequences lipgloss itself emits.
var sgr = regexp.MustCompile("\x1b\\[[0-9;:]*m")

// assertNoControls fails when s, with lipgloss's own styling and line breaks
// removed, still holds a control character. Failures quote s, so nothing raw
// is printed.
func assertNoControls(t *testing.T, what, s string) {
	t.Helper()
	for i, r := range sgr.ReplaceAllString(s, "") {
		if r == '\n' {
			continue
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Fatalf("%s has control character %U at byte %d: %q", what, r, i, s)
		}
	}
}

func TestTrackViewNameDropsControlSequences(t *testing.T) {
	tr := playlist.Track{Title: hostileText, Artist: "A\x1b[2JB"}
	assertNoControls(t, "trackViewName", trackViewName(tr))
	if got, want := trackViewName(tr), "A[2JB - AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END"; got != want {
		t.Errorf("trackViewName = %q, want %q", got, want)
	}
	if tr.Title != hostileText {
		t.Errorf("track title was changed to %q", tr.Title)
	}
}

func TestTrackInfoNameDropsControlSequences(t *testing.T) {
	stream := playlist.Track{Path: "https://radio.example/live", Stream: true, Title: "Station"}
	assertNoControls(t, "ICY title", trackInfoName(stream, hostileText))

	tagged := playlist.Track{Title: "Song", Album: hostileText}
	assertNoControls(t, "album suffix", trackInfoName(tagged, ""))
}

func TestRenderedTrackInfoDropsICYControlSequences(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 120
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	p := playlist.New()
	p.Add(playlist.Track{Path: "https://radio.example/live", Stream: true, Title: "Station"})
	m := Model{playlist: p, streamTitle: hostileText}
	assertNoControls(t, "rendered track info", m.renderSimplifiedTrackInfo())
}

func TestRenderTransientDropsControlSequences(t *testing.T) {
	m := Model{layout: frameLayout{panelWidth: 120}}
	m.status.Showf(statusTTLShort, "Playing: %s", hostileText)
	assertNoControls(t, "status line", m.renderTransient())
}

func TestAlbumLabelsDropControlSequences(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 120
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	assertNoControls(t, "album suffix", trackAlbumSuffix(playlist.Track{Album: hostileText}, false))
	assertNoControls(t, "album header", Model{}.albumSeparator(hostileText, 2026))
}

func TestPlaylistPickerDropsControlSequences(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 120
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	tracks := []playlist.Track{{Title: hostileText}}
	m := Model{plVisible: 10, plPicker: playlistPickerState{tracks: tracks}}
	assertNoControls(t, "picker head", m.renderPlaylistPickerBody())

	m.plPicker.title = "Track: " + hostileText
	assertNoControls(t, "picker title", m.renderPlaylistPickerBody())

	m.plPicker.screen = plPickerNewName
	assertNoControls(t, "picker new name", m.renderPlaylistPickerBody())
}

func TestTitlesWithoutControlsAreUnchanged(t *testing.T) {
	title := "Björk - Jóga ★ 日本語 (Live) [2026] 100% & \"quotes\""
	tr := playlist.Track{Title: title, Album: "Homogenic"}
	if got := trackViewName(tr); got != title {
		t.Errorf("trackViewName = %q, want %q", got, title)
	}
	if got, want := trackInfoName(tr, ""), title+" · Homogenic"; got != want {
		t.Errorf("trackInfoName = %q, want %q", got, want)
	}
	stream := playlist.Track{Stream: true}
	if got := trackInfoName(stream, title); got != title {
		t.Errorf("ICY trackInfoName = %q, want %q", got, title)
	}
	if got, want := trackAlbumSuffix(tr, false), " · Homogenic"; got != want {
		t.Errorf("trackAlbumSuffix = %q, want %q", got, want)
	}
}

// visible is the text a line shows once lipgloss's styling is removed.
func visible(s string) string { return sgr.ReplaceAllString(s, "") }

func TestRenderTransientDropsErrorAndLogControls(t *testing.T) {
	t.Run("error", func(t *testing.T) {
		m := Model{layout: frameLayout{panelWidth: 120}, err: errors.New("load " + hostileText)}
		out := m.renderTransient()
		assertNoControls(t, "error line", out)
		if want := "ERR: load AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END"; !strings.Contains(visible(out), want) {
			t.Errorf("error line = %q, want it to show %q", out, want)
		}
	})
	t.Run("log", func(t *testing.T) {
		m := Model{layout: frameLayout{panelWidth: 120}, logLines: []logLine{{text: "plugin: " + hostileText}}}
		out := m.renderTransient()
		assertNoControls(t, "log line", out)
		if want := "plugin: AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END"; !strings.Contains(visible(out), want) {
			t.Errorf("log line = %q, want it to show %q", out, want)
		}
	})
}

func TestPlaylistLabelDropsControlSequences(t *testing.T) {
	info := playlist.PlaylistInfo{Name: hostileText, TrackCount: 2}
	if got, want := playlistLabel("", info), "AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END · 2 tracks"; got != want {
		t.Errorf("playlistLabel = %q, want %q", got, want)
	}
	if info.Name != hostileText {
		t.Errorf("stored name was changed to %q", info.Name)
	}
}

func TestPlaylistPickerListDropsControlSequences(t *testing.T) {
	oldPanelWidth := ui.PanelWidth
	ui.PanelWidth = 120
	t.Cleanup(func() { ui.PanelWidth = oldPanelWidth })

	t.Run("list", func(t *testing.T) {
		m := Model{plVisible: 10, plPicker: playlistPickerState{
			tracks:    []playlist.Track{{Title: "Song"}},
			playlists: []playlist.PlaylistInfo{{Name: hostileText, TrackCount: 1}, {Name: "Plain", TrackCount: 3}},
		}}
		out := m.renderPlaylistPickerBody()
		assertNoControls(t, "picker list", out)
		for _, want := range []string{"AUDIT]52;c;RkFLRQ==TITLE2JPtmux;x\\END · 1 tracks", "Plain · 3 tracks", "+ New Playlist..."} {
			if !strings.Contains(visible(out), want) {
				t.Errorf("picker list = %q, want it to show %q", out, want)
			}
		}
		if m.plPicker.playlists[0].Name != hostileText {
			t.Errorf("stored name was changed to %q", m.plPicker.playlists[0].Name)
		}
	})
	t.Run("error", func(t *testing.T) {
		m := Model{plVisible: 10, plPicker: playlistPickerState{screen: plPickerNewName}}
		m.plPicker.inputErr = "Write failed: open /lists/" + hostileText + ".toml"
		assertNoControls(t, "picker error", m.renderPlaylistPickerBody())
	})
}

func TestPlaylistNamesAndMessagesWithoutControlsAreUnchanged(t *testing.T) {
	name := "Björk ★ 日本語 Mix (2026) 100% & \"quotes\""
	if got, want := playlistLabel("▸ ", playlist.PlaylistInfo{Name: name, TrackCount: 3}), "▸ "+name+" · 3 tracks"; got != want {
		t.Errorf("playlistLabel = %q, want %q", got, want)
	}
	m := Model{layout: frameLayout{panelWidth: 120}, err: errors.New("open " + name)}
	if got, want := visible(m.renderTransient()), "ERR: open "+name; !strings.Contains(got, want) {
		t.Errorf("error line = %q, want it to show %q", got, want)
	}
	m = Model{layout: frameLayout{panelWidth: 120}, logLines: []logLine{{text: name}}}
	if got := visible(m.renderTransient()); !strings.Contains(got, name) {
		t.Errorf("log line = %q, want it to show %q", got, name)
	}
}
