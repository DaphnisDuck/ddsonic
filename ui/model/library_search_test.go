package model

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// fakeSearch is a library.SearchLevel whose results the test scripts.
type fakeSearch struct {
	query   string
	loads   *[]string
	results func(query string) []library.Entry
}

func (s fakeSearch) Title() string { return "Search" }
func (s fakeSearch) Query() string { return s.query }
func (s fakeSearch) WithQuery(q string) library.SearchLevel {
	s.query = q
	return s
}
func (s fakeSearch) Load(context.Context) ([]library.Entry, error) {
	*s.loads = append(*s.loads, s.query)
	return s.results(s.query), nil
}

// liveSearcher is a provider with its own live search.
type liveSearcher struct{ playlist.Provider }

func (liveSearcher) Name() string { return "Spotify" }
func (liveSearcher) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return nil, nil
}

func newSearchModel(t *testing.T) (Model, *[]string) {
	t.Helper()
	loads := &[]string{}
	album := []playlist.Track{{Path: "a1", Title: "One"}, {Path: "a2", Title: "Two"}}
	search := fakeSearch{loads: loads, results: func(q string) []library.Entry {
		two := album[1]
		return []library.Entry{
			{ID: "album:1", Title: "An Album " + q, Section: "Albums", Open: library.Menu("An Album", library.Entry{Title: "inside"})},
			{ID: "track:2", Title: "Two", Section: "Tracks", Track: &two,
				PlayFrom: func(context.Context) ([]playlist.Track, int, error) { return album, 1, nil }},
			{Title: "Search Spotify for “" + q + "”", Section: "Beyond your library",
				Intent: library.IntentSearch, Provider: liveSearcher{}, Query: q},
		}
	}}
	root := library.Menu("Music", library.Entry{Title: "Search", Open: search})
	return newLibraryModel(root), loads
}

func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = libPress(t, m, string(r))
	}
	return m
}

func TestLibrarySearchTypingAndFocus(t *testing.T) {
	m, loads := newSearchModel(t)
	m = libPress(t, m, "/")
	if _, ok := m.libSearchLevel(); !ok || !m.lib.searchInput || len(*loads) != 0 {
		t.Fatalf("after /: search %v, input %v, loads %v", ok, m.lib.searchInput, *loads)
	}
	// j, k, q and space are text while the input has focus.
	m = typeText(t, m, "jq k")
	if sl, _ := m.libSearchLevel(); sl.Query() != "jq k" || m.libTop().entries[0].Title != "An Album jq k" {
		t.Fatalf("query %q, rows %+v", sl.Query(), m.libTop().entries)
	}
	if got := strings.Join(*loads, "|"); got != "j|jq|jq |jq k" {
		t.Errorf("loads = %q", got)
	}

	// Enter moves to the results, where j moves the cursor.
	m = libPress(t, m, "enter")
	m = libPress(t, m, "j")
	if m.lib.searchInput || m.libTop().cursor != 1 {
		t.Errorf("results focus: input %v, cursor %d", m.lib.searchInput, m.libTop().cursor)
	}
	// Esc returns to the input, and Esc there closes search.
	m = libPress(t, m, "esc")
	if !m.lib.searchInput {
		t.Error("esc in the results did not return to the input")
	}
	m = libPress(t, m, "esc")
	if _, ok := m.libSearchLevel(); ok || len(m.lib.stack) != 1 {
		t.Errorf("esc in the input left %d frames", len(m.lib.stack))
	}
	// Reopening keeps the last query and its results.
	m = libPress(t, m, "/")
	if sl, _ := m.libSearchLevel(); sl.Query() != "jq k" || len(m.libTop().entries) == 0 {
		t.Errorf("reopened search: query %q, %d rows", sl.Query(), len(m.libTop().entries))
	}
}

// Fast typing runs only the last query: a superseded tick does nothing.
func TestLibrarySearchSupersedesStaleQueries(t *testing.T) {
	m, loads := newSearchModel(t)
	m = libPress(t, m, "/")
	var ticks []tea.Cmd
	for _, key := range []string{"b", "e"} {
		updated, cmd := m.Update(libKey(key))
		m, ticks = updated.(Model), append(ticks, cmd)
	}
	for _, tick := range ticks {
		m = libRun(t, m, tick)
	}
	if !slices.Equal(*loads, []string{"be"}) {
		t.Errorf("loads = %q, want only the settled query", *loads)
	}
	// Emptying the query clears the results without a load ("b" on the
	// way still runs).
	m = libPress(t, m, "backspace")
	m = libPress(t, m, "backspace")
	if len(m.libTop().entries) != 0 || !slices.Equal(*loads, []string{"be", "b"}) {
		t.Errorf("after clearing: %d rows, loads %q", len(m.libTop().entries), *loads)
	}
}

func TestLibrarySearchResultActions(t *testing.T) {
	m, _ := newSearchModel(t)
	m = libPress(t, m, "/")
	m = typeText(t, m, "x")
	m = libPress(t, m, "enter") // to the results

	// A track plays its album from that track.
	m = libPress(t, m, "j")
	m = libPress(t, m, "enter")
	if m.playlist.Len() != 2 || m.playlist.Index() != 1 {
		t.Errorf("queue %d tracks at %d, want the album from its second track", m.playlist.Len(), m.playlist.Index())
	}

	// / from a level opened from the results returns to the search input.
	m = libPress(t, m, "k")
	m = libPress(t, m, "enter")
	if m.libTop().level.Title() != "An Album" {
		t.Fatalf("opened %q", m.libTop().level.Title())
	}
	m = libPress(t, m, "/")
	if _, ok := m.libSearchLevel(); !ok || !m.lib.searchInput || len(m.lib.stack) != 2 {
		t.Errorf("/ from a result: search %v, input %v, %d frames", ok, m.lib.searchInput, len(m.lib.stack))
	}

	// The Spotify row opens Spotify's own search, already running.
	m = libPress(t, m, "enter")
	m = libPress(t, m, "G")
	m = libPress(t, m, "enter")
	if !m.spotSearch.visible || m.spotSearch.query != "x" || !m.spotSearch.loading {
		t.Errorf("spot search = visible %v, query %q, loading %v", m.spotSearch.visible, m.spotSearch.query, m.spotSearch.loading)
	}
}

func TestLibrarySearchRenders(t *testing.T) {
	m, _ := newSearchModel(t)
	m = libPress(t, m, "/")
	if body := m.renderLibraryBody(); !strings.Contains(body, "Search: _") || !strings.Contains(body, "artist:") {
		t.Errorf("empty search body = %q, want the prompt and the operator hint", body)
	}
	m = typeText(t, m, "x")
	body := m.renderLibraryBody()
	if !strings.Contains(body, "Search: x_") || !strings.Contains(body, "An Album x") || !strings.Contains(body, "Tracks") {
		t.Errorf("search body = %q", body)
	}
	// The Tracks section counts from 1, and no row has the cursor while
	// the input has focus.
	if !strings.Contains(body, " 1. Two") || strings.Contains(body, "> ") {
		t.Errorf("typing body = %q, want track 1 and no cursor", body)
	}
	m = libPress(t, m, "enter")
	if body := m.renderLibraryBody(); !strings.Contains(body, "> An Album x") {
		t.Errorf("results body = %q, want the cursor on the first result", body)
	}
}

func TestLibTrackNumbers(t *testing.T) {
	tr := &playlist.Track{}
	entries := []library.Entry{
		{Section: "Albums", Title: "A"}, {Section: "Tracks", Track: tr}, {Section: "Tracks", Track: tr},
		{Section: "Tracks", Title: "More…"}, {Section: "Stations", Track: tr},
	}
	if got := libTrackNumbers(entries); !slices.Equal(got, []int{0, 1, 2, 0, 1}) {
		t.Errorf("numbers = %v", got)
	}
}

// orderedLevel lists its rows forwards or backwards.
type orderedLevel struct {
	rows     []library.Entry
	backward *bool
}

func (orderedLevel) Title() string { return "Albums" }
func (l orderedLevel) Load(context.Context) ([]library.Entry, error) {
	out := slices.Clone(l.rows)
	if *l.backward {
		slices.Reverse(out)
	}
	return out, nil
}
func (l orderedLevel) OrderName() string {
	if *l.backward {
		return "by artist"
	}
	return "by title"
}
func (l orderedLevel) NextOrder() string {
	*l.backward = !*l.backward
	return l.OrderName()
}

func TestLibraryOrderKey(t *testing.T) {
	backward := false
	albums := orderedLevel{rows: []library.Entry{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}, backward: &backward}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: albums}))
	m = libPress(t, m, "enter")
	m = libPress(t, m, "j") // on B
	if !strings.Contains(m.libHeaderLine(), "by title") || !strings.Contains(m.renderTierHelp(), "Order") {
		t.Errorf("header %q, help %q; want the order shown", m.libHeaderLine(), m.renderTierHelp())
	}
	m = libPress(t, m, "o")
	f := m.libTop()
	if got := []string{f.entries[0].ID, f.entries[1].ID, f.entries[2].ID}; !slices.Equal(got, []string{"c", "b", "a"}) ||
		f.entries[f.cursor].ID != "b" || !strings.Contains(m.libHeaderLine(), "by artist") {
		t.Errorf("after o: rows %v, cursor on %q, header %q", got, f.entries[f.cursor].ID, m.libHeaderLine())
	}
}

func TestStationRowsHaveNoNumber(t *testing.T) {
	station := playlist.Track{Path: "https://s", Title: "WBGO", Stream: true, Realtime: true}
	song := playlist.Track{Path: "/a.flac", Title: "Song", DurationSecs: 60}
	if got := libEntryLabel(library.Entry{Title: "WBGO", Track: &station}, 1, 0); strings.Contains(got, "1.") || !strings.Contains(got, "WBGO") {
		t.Errorf("station row = %q, want no number", got)
	}
	if got := libEntryLabel(library.Entry{Title: "Song", Track: &song}, 3, 0); !strings.HasPrefix(got, "3. ") {
		t.Errorf("track row = %q, want its number", got)
	}
	if got := libTrackNumbers([]library.Entry{{Track: &station}, {Track: &song}}); !slices.Equal(got, []int{0, 1}) {
		t.Errorf("numbers = %v, want stations skipped", got)
	}
}

// Enter within the debounce runs the typed query at once and focuses its
// results; the late tick changes nothing.
func TestLibrarySearchEnterRunsPendingQuery(t *testing.T) {
	m, loads := newSearchModel(t)
	m = libPress(t, m, "/")
	m = typeText(t, m, "a") // settled: rows for "a"
	updated, tick := m.Update(libKey("b"))
	m = updated.(Model)
	m = libPress(t, m, "enter") // before the tick for "ab"
	if m.lib.searchInput || m.libTop().entries[0].Title != "An Album ab" {
		t.Fatalf("after enter: input %v, rows %+v", m.lib.searchInput, m.libTop().entries)
	}
	m = libPress(t, m, "j")
	m = libRun(t, m, tick) // the stale tick
	if m.libTop().cursor != 1 || !slices.Equal(*loads, []string{"a", "ab"}) {
		t.Errorf("after the late tick: cursor %d, loads %q", m.libTop().cursor, *loads)
	}
}
