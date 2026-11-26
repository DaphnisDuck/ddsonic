package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// A search result's track row names its source (library.Entry.Detail), at
// every width, without losing its number or its duration.
func TestSearchTrackRowShowsItsSource(t *testing.T) {
	local := playlist.Track{Path: "/m/1.mp3", Title: "Shared Song", Artist: "Echo Artist"}
	remote := playlist.Track{Path: "https://music.youtube.com/watch?v=x", Title: "Shared Song", Artist: "Echo Artist", DurationSecs: 180}
	long := playlist.Track{Path: "/m/2.mp3", Title: strings.Repeat("A Very Long Title ", 12), Artist: "Echo Artist", DurationSecs: 3725}
	wide := playlist.Track{Path: "/m/3.mp3", Title: "夜に駆ける 夜に駆ける 夜に駆ける 夜に駆ける 夜に駆ける", Artist: "ヨアソビ", DurationSecs: 261}
	tests := []struct {
		name     string
		track    playlist.Track
		source   string
		number   int
		duration string
	}{
		{"local, no duration", local, "Local", 1, ""},
		{"youtube, with a duration", remote, "YouTube", 2, "3:00"},
		{"spotify, a two-digit number", remote, "Spotify", 12, "3:00"},
		{"a title longer than the row", long, "Local", 3, "1:02:05"},
		{"double-width characters", wide, "YouTube", 4, "4:21"},
		{"a label of several words", remote, "YouTube · from tracks", 5, "3:00"},
	}
	for _, panel := range []int{146, 100, 76, 40} {
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				defer ui.WithPanelWidth(panel)()
				track := tt.track
				got := libEntryLabel(library.Entry{Title: track.Title, Detail: tt.source, Track: &track}, tt.number, libCols{})

				if !strings.Contains(got, "  "+tt.source) {
					t.Errorf("panel %d: row %q does not show its source %q whole", panel, got, tt.source)
				}
				prefix := fmt.Sprintf("%d. ", tt.number)
				if !strings.HasPrefix(got, prefix) {
					t.Errorf("panel %d: row %q does not start with its number %q", panel, got, prefix)
				}
				if tt.duration != "" && !strings.HasSuffix(got, " "+tt.duration) {
					t.Errorf("panel %d: row %q does not end in its duration %q", panel, got, tt.duration)
				}
				// The row fits where a plain track row fits, and its
				// duration stands in the same column as theirs.
				plain := formatTrackRow(tt.number, trackViewName(track), track.DurationSecs)
				if w, limit := lipgloss.Width(got), ui.PanelWidth-libCursorPrefix; w > limit {
					t.Errorf("panel %d: row %q is %d cells wide, over the %d a row has", panel, got, w, limit)
				}
				if tt.duration != "" && lipgloss.Width(got) != lipgloss.Width(plain) {
					t.Errorf("panel %d: row %q is %d cells wide and the same track's plain row %d: the durations do not line up",
						panel, got, lipgloss.Width(got), lipgloss.Width(plain))
				}
				// Something of the title is still there to read.
				if name := trackViewName(track); !strings.Contains(got, name[:3]) {
					t.Errorf("panel %d: row %q has lost its title %q", panel, got, name)
				}
			})
		}
	}
}

// Where one source's list is browsed, a track row has no label and is the
// row it always was.
func TestBrowsedTrackRowIsUnchanged(t *testing.T) {
	track := playlist.Track{Path: "/m/1.mp3", Title: "Shared Song", Artist: "Echo Artist", DurationSecs: 180}
	for _, panel := range []int{146, 76, 40} {
		restore := ui.WithPanelWidth(panel)
		got := libEntryLabel(library.Entry{Title: track.Title, Track: &track}, 7, libCols{})
		if want := formatTrackRow(7, trackViewName(track), 180); got != want {
			t.Errorf("panel %d: unlabelled track row = %q, want the plain row %q", panel, got, want)
		}
		restore()
	}
}

// The width a labelled row asks for includes its label, so a wide terminal
// gives the list room for it and does not cut the title short to fit one.
func TestSearchTrackRowNeedCountsItsSource(t *testing.T) {
	track := playlist.Track{Path: "/m/1.mp3", Title: "Shared Song", Artist: "Echo Artist", DurationSecs: 180}
	plain := measureTrackCols([]library.Entry{{Title: track.Title, Track: &track}}, []int{1}).table()
	labelled := measureTrackCols([]library.Entry{{Title: track.Title, Detail: "YouTube", Track: &track}}, []int{1}).table()
	if want := plain + libDetailGap + len("YouTube"); labelled != want {
		t.Errorf("a labelled row needs %d cells, want %d: the plain row's %d and the label", labelled, want, plain)
	}
}

// The search screen itself: the same recording from two sources is two
// rows that can be told apart, with and without the cursor, wide and
// narrow, and a station says it is one.
func TestLibrarySearchRendersEachRowsSource(t *testing.T) {
	local := playlist.Track{Path: "/m/1.mp3", Title: "Shared Song", Artist: "Echo Artist"}
	remote := playlist.Track{Path: "https://music.youtube.com/watch?v=x", Title: "Shared Song", Artist: "Echo Artist", DurationSecs: 180}
	station := playlist.Track{Path: "https://echo", Title: "Echo FM", Stream: true, Realtime: true}
	one := func(tr playlist.Track) func(context.Context) ([]playlist.Track, int, error) {
		return func(context.Context) ([]playlist.Track, int, error) { return []playlist.Track{tr}, 0, nil }
	}
	loads := &[]string{}
	search := fakeSearch{loads: loads, results: func(string) []library.Entry {
		return []library.Entry{
			{ID: "track:1", Title: "Shared Song", Detail: "Local", Section: "Tracks", Track: &local, PlayFrom: one(local), Source: "local"},
			{ID: "track:2", Title: "Shared Song", Detail: "YouTube", Section: "Tracks", Track: &remote, PlayFrom: one(remote), Source: "youtube"},
			{ID: "station:3", Title: "Echo FM", Detail: "Radio", Section: "Stations", Track: &station, PlayFrom: one(station), Source: "radio"},
		}
	}}
	for _, panel := range []int{146, 76} {
		defer ui.WithPanelWidth(panel)()
		m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Search", Open: search}))
		m = libPress(t, m, "/")
		m = typeText(t, m, "shared")

		body := m.renderLibraryBody()
		for _, want := range []string{"1. Echo Artist - Shared Song  Local", "2. Echo Artist - Shared Song  YouTube", " 3:00", "Echo FM", "Radio"} {
			if !strings.Contains(body, want) {
				t.Errorf("panel %d: the results do not show %q:\n%s", panel, want, body)
			}
		}
		// With the cursor on the first track, then on the second.
		m = libPress(t, m, "enter")
		if body := m.renderLibraryBody(); !strings.Contains(body, "> 1. Echo Artist - Shared Song  Local") {
			t.Errorf("panel %d: the selected row does not show its source:\n%s", panel, body)
		}
		m = libPress(t, m, "j")
		if body := m.renderLibraryBody(); !strings.Contains(body, "> 2. Echo Artist - Shared Song  YouTube") ||
			!strings.Contains(body, "  1. Echo Artist - Shared Song  Local") {
			t.Errorf("panel %d: after moving, the rows do not show their sources:\n%s", panel, body)
		}
	}
}
