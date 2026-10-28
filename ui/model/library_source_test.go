package model

// ddsonic: SRC names the playing track's source while the library is enabled
// (M7.2).

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

func TestTrackSource(t *testing.T) {
	tests := []struct {
		name  string
		track playlist.Track
		want  string
	}{
		{"recorded", playlist.Track{Path: "/m/a.flac", ProviderMeta: map[string]string{librarySourceMeta: catalog.YouTube}}, catalog.YouTube},
		{"spotify uri", playlist.Track{Path: "spotify:track:x"}, catalog.Spotify},
		{"youtube", playlist.Track{Path: "https://www.youtube.com/watch?v=x", Stream: true}, catalog.YouTube},
		{"youtube music", playlist.Track{Path: "https://music.youtube.com/watch?v=x", Stream: true}, catalog.YouTube},
		{"live stream", playlist.Track{Path: "https://wbgo/stream", Stream: true, Realtime: true}, catalog.Radio},
		{"local file", playlist.Track{Path: "/m/a.flac"}, catalog.Local},
		{"other url", playlist.Track{Path: "https://example.com/a.mp3", Stream: true}, ""},
		{"feed", playlist.Track{Path: "https://example.com/feed.xml", Feed: true}, ""},
		{"empty", playlist.Track{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trackSource(tt.track); got != tt.want {
				t.Errorf("trackSource = %q, want %q", got, tt.want)
			}
		})
	}
}

// sourceRoot is a Music menu whose Spotify source holds an album of tracks
// whose paths do not tell their source, so only the library's record can.
func sourceRoot(shared map[string]string) library.Level {
	album := library.TrackLevel("Album", nil, func(context.Context) ([]playlist.Track, error) {
		return []playlist.Track{{Path: "a", Title: "a", ProviderMeta: shared}, {Path: "b", Title: "b"}}, nil
	})
	spotify := library.Menu("Spotify", library.Entry{Title: "Album", Open: album})
	return library.Menu("Music",
		library.Entry{Title: "Spotify", Open: spotify, Source: catalog.Spotify},
		library.Entry{Title: "Mixed", Open: library.Menu("Mixed",
			library.Entry{Title: "Station", Source: catalog.Radio, Play: func(context.Context) ([]playlist.Track, error) {
				return []playlist.Track{{Path: "http://s", Title: "s", Stream: true}}, nil
			}})},
	)
}

func srcRows(m Model) (pane, pill, closed string) {
	return ansi.Strip(m.settingsSource(60)), ansi.Strip(m.renderProviderPill()), ansi.Strip(m.renderSourceVolume())
}

func TestLibrarySourceShownInSRC(t *testing.T) {
	shared := map[string]string{"x.id": "1"}
	m := newLibraryModel(sourceRoot(shared))
	m.providers = []ProviderEntry{{Name: "cliamp radio"}, {Name: "Spotify"}}

	pane, pill, closed := srcRows(m)
	if pane != "" || pill != "" || strings.Contains(closed, "SRC") {
		t.Errorf("empty queue: pane %q, pill %q, closed %q; want no SRC", pane, pill, closed)
	}

	// Spotify → Album → play the second track: the source is inherited.
	for _, k := range []string{"enter", "enter", "j", "enter"} {
		m = libPress(t, m, k)
	}
	pane, pill, closed = srcRows(m)
	if pane != "SRC [Spotify]" || pill != "SRC [Spotify]" || !strings.Contains(closed, "SRC [Spotify]") {
		t.Errorf("Spotify album: pane %q, pill %q, closed %q; want SRC [Spotify], no provider index", pane, pill, closed)
	}
	if shared[librarySourceMeta] != "" {
		t.Error("recording the source wrote into the level's track meta")
	}
	if tr, _ := m.playlist.Track(0); tr.Meta("x.id") != "1" {
		t.Error("recording the source dropped the track's own meta")
	}

	// A row's own source beats its frame's (none here), through Play.
	m = libPress(t, m, "esc")
	m = libPress(t, m, "esc")
	for _, k := range []string{"j", "enter", "enter"} {
		m = libPress(t, m, k)
	}
	if pane, _, _ = srcRows(m); pane != "SRC [Radio]" {
		t.Errorf("station: pane %q, want SRC [Radio]", pane)
	}
}

// Titles from providers, tags and the catalog cannot send escape sequences
// to the terminal through the library's rows.
func TestLibraryRowsDropControlCharacters(t *testing.T) {
	evil := "Title\x1b]52;c;ZXZpbA==\x07\x1b[2J"
	rows := []library.Entry{
		{Title: evil, Detail: evil, Open: library.Menu("x")},
		{Track: &playlist.Track{Path: "/m/a.flac", Title: evil}},
	}
	for i, e := range rows {
		if got := libEntryLabel(e, 1, 0); strings.ContainsAny(got, "\x1b\x07") {
			t.Errorf("row %d label %q keeps control characters", i, got)
		}
	}
}
