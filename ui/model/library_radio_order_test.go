package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// orderRadio is a radio provider with a tag browser whose tags arrive most
// stations first, not A–Z; each tag's one station is named after it.
type orderRadio struct{}

func (orderRadio) Name() string                                 { return "Radio" }
func (orderRadio) Playlists() ([]playlist.PlaylistInfo, error)  { return nil, nil }
func (orderRadio) Tracks(string) ([]playlist.Track, error)      { return nil, nil }
func (orderRadio) StationSelection() string                     { return "Top 200 by votes" }
func (orderRadio) GenreBrowserFor(string) provider.GenreBrowser { return orderTags{} }
func (orderRadio) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{{ID: "tags", Name: "Browse tags", Mode: provider.BrowseGenres}}
}

type orderTags struct{}

func (orderTags) GenreLabel() string { return "Genres & Tags" }
func (orderTags) Genres() ([]provider.GenreInfo, error) {
	return []provider.GenreInfo{{ID: "rock", Name: "rock (900)"}, {ID: "jazz", Name: "jazz (400)"}, {ID: "ambient", Name: "ambient (300)"}}, nil
}
func (orderTags) GenreSortTypes() []provider.SortType { return []provider.SortType{{ID: "votes"}} }
func (orderTags) GenreTracks(id, _ string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://" + id, Title: "in " + id, Stream: true, Realtime: true}}, nil
}

func openTags(t *testing.T) Model {
	t.Helper()
	m := newLibraryModel(library.Root(library.Sources{Radio: orderRadio{}}))
	for _, title := range []string{"Radio", "Browse Stations", "Genres & Tags"} {
		m = libEnter(t, m, title)
	}
	return m
}

// The tags keep the browser's order, most stations first, and the row the
// cursor is on is the tag that opens. Back returns to that row, and opening
// it again opens the same tag.
func TestRadioTagsOpenTheRowSelected(t *testing.T) {
	m := openTags(t)
	if got, want := libRowTitles(m), []string{"rock (900)", "jazz (400)", "ambient (300)"}; !slices.Equal(got, want) {
		t.Fatalf("tags = %q, want %q", got, want)
	}
	m.libTop().cursor = 1
	m = libPress(t, m, "enter")
	if got := libRowTitles(m); !slices.Equal(got, []string{"in jazz"}) {
		t.Fatalf("the second row opened %q, want the jazz tag", got)
	}
	m = libPress(t, m, "esc")
	if f := m.libTop(); f.level.Title() != "Genres & Tags" || f.cursor != 1 || f.entries[f.cursor].Title != "jazz (400)" {
		t.Fatalf("back on %q at row %d, want Genres & Tags on jazz (400)", f.level.Title(), f.cursor)
	}
	m = libPress(t, m, "enter")
	if got := libRowTitles(m); !slices.Equal(got, []string{"in jazz"}) {
		t.Errorf("reopened %q, want the jazz tag again", got)
	}
}

// A tag's stations say above them which they are, on one line where it
// fits, wrapped and never cut where it does not.
func TestRadioStationListNoteFits(t *testing.T) {
	const note = "Top 200 by votes, listed A–Z."
	for _, size := range []struct{ w, h int }{{40, 30}, {56, 30}, {80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m := openTags(t)
			m = resized(t, libEnter(t, m, "jazz (400)"), size.w, size.h)
			width, rows := m.bodySize()
			lines := strings.Split(ansi.Strip(m.renderLibraryBody()), "\n")
			shown := len(m.libNote())
			if len(lines) != rows || shown == 0 {
				t.Fatalf("body is %d lines with a %d-line note, want %d lines with a note", len(lines), shown, rows)
			}
			for _, l := range lines[:shown] {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("note line is %d columns, panel is %d: %q", w, width, l)
				}
			}
			if got := strings.Join(strings.Fields(strings.Join(lines[:shown], " ")), " "); got != note {
				t.Errorf("note reads %q, want %q", got, note)
			}
			if !strings.Contains(lines[shown], "in jazz") {
				t.Errorf("first row after the note = %q, want the station", lines[shown])
			}
			checkFrame(t, m, size.w, size.h)
		})
	}
}
