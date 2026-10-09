package model

// ddsonic: a provider's own search, opened from the library search ("Search
// Spotify for …"), says whose results it shows and for what, and an edit
// while it loads does not leave it stuck.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// namedSearcher is a provider with its own live search, under any name.
type namedSearcher struct {
	playlist.Provider
	name string
}

func (s namedSearcher) Name() string { return s.name }
func (namedSearcher) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return nil, nil
}

// liveSearchModel is a library search for query at 80×24, its cursor on
// the row that searches the provider called name.
func liveSearchModel(t *testing.T, name, query string) Model {
	t.Helper()
	loads := &[]string{}
	search := fakeSearch{loads: loads, results: func(q string) []library.Entry {
		track := playlist.Track{Path: "/music/local.flac", Title: "Catalog Track"}
		return []library.Entry{
			{ID: "album:1", Title: "Catalog Album", Detail: "Local", Section: "Albums", Open: library.Menu("Catalog Album")},
			{ID: "track:2", Title: "Catalog Track", Section: "Tracks", Track: &track},
			{Title: "Search " + name + " for “" + q + "”", Section: "Beyond your library",
				Intent: library.IntentSearch, Provider: namedSearcher{name: name}, Query: q},
		}
	}}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Search", Open: search}))
	m = resized(t, m, 80, 24)
	m = libPress(t, m, "/")
	m = typeText(t, m, query)
	m = libPress(t, m, "enter") // to the results
	return libPress(t, m, "G")  // the provider's row
}

// pressKey sends a key and returns the command unrun, so a search's reply can
// be delivered when the test chooses.
func pressKey(m Model, key string) (Model, tea.Cmd) {
	updated, cmd := m.Update(libKey(key))
	return updated.(Model), cmd
}

func viewText(m Model) string { return stripAnsi(m.View().Content) }

func wantInView(t *testing.T, view string, want, unwanted []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(view, s) {
			t.Errorf("view lacks %q:\n%s", s, view)
		}
	}
	for _, s := range unwanted {
		if strings.Contains(view, s) {
			t.Errorf("view shows %q:\n%s", s, view)
		}
	}
}

// catalogRows are what the library search drew; none may show once the
// provider's own search has taken the screen.
var catalogRows = []string{"Catalog Album", "Catalog Track", "Music / Search", "Beyond your library"}

func TestLiveSearchNamesItsProviderAndQuery(t *testing.T) {
	for _, name := range []string{"Spotify", "YouTube Music"} {
		t.Run(name, func(t *testing.T) {
			m := liveSearchModel(t, name, "daphnis")
			wantInView(t, viewText(m), catalogRows, nil)

			// Chosen: the provider's search has the screen at once, before
			// any reply, and says what it is doing.
			m, cmd := pressKey(m, "enter")
			if cmd == nil || !m.spotSearch.loading {
				t.Fatalf("no search started: cmd nil %v, loading %v", cmd == nil, m.spotSearch.loading)
			}
			loading := viewText(m)
			wantInView(t, loading, []string{"Search " + name + ": daphnis", "Searching " + name + " for “daphnis”…"}, catalogRows)

			// Results: still named, and only the provider's rows.
			reply := cmd().(spotSearchResultsMsg)
			reply.tracks = []playlist.Track{
				{Path: "live:1", Title: "Live One", Artist: "Haitink"},
				{Path: "live:2", Title: "Live Two", Artist: "Haitink"},
			}
			updated, _ := m.Update(reply)
			m = updated.(Model)
			results := viewText(m)
			wantInView(t, results, []string{name + " results for “daphnis”  1/2", "Haitink - Live One", "Haitink - Live Two"}, catalogRows)
			checkFrame(t, m, 80, 24)

			// No other provider's name leaks into either screen.
			if name != "Spotify" {
				wantInView(t, loading+results, nil, []string{"Spotify"})
			}

			// Esc leaves the results for the query, Esc again the search:
			// the library search is as it was, cursor on the provider's row.
			m, _ = pressKey(m, "esc")
			wantInView(t, viewText(m), []string{"Search " + name + ": daphnis"}, catalogRows)
			m, _ = pressKey(m, "esc")
			if m.spotSearch.visible {
				t.Fatal("the provider's search is still open")
			}
			sl, ok := m.libSearchLevel()
			f := m.libTop()
			if !ok || sl.Query() != "daphnis" || len(f.entries) != 3 || f.cursor != 2 || m.lib.searchInput {
				t.Errorf("library search after leaving: ok %v, %d rows, cursor %d, input %v", ok, len(f.entries), f.cursor, m.lib.searchInput)
			}
			wantInView(t, viewText(m), append([]string{"Search: daphnis", "> Search " + name + " for “daphnis”"}, catalogRows...),
				[]string{name + " results for", "Live One"})
		})
	}
}

// With no results the screen still says whose search found nothing.
func TestLiveSearchNamesItsProviderWithNoResults(t *testing.T) {
	m := liveSearchModel(t, "Spotify", "zzz")
	m, cmd := pressKey(m, "enter")
	updated, _ := m.Update(cmd())
	wantInView(t, viewText(updated.(Model)), []string{"Spotify results for “zzz”", "No results"}, catalogRows)
}

// On a narrow panel a long query gives way, never the provider's name or
// the position: it is cut short, or left out where a few characters of it
// would not fit.
func TestLiveSearchLabelsFitNarrowPanels(t *testing.T) {
	const long = "ravel daphnis et chloe haitink chicago symphony orchestra complete ballet"
	for _, width := range []int{40, 56, 80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := liveSearchModel(t, "YouTube Music", "r")
			m, cmd := pressKey(m, "enter")
			m.spotSearch.query = long
			m = resized(t, m, width, 24)
			loading := viewText(m)
			wantInView(t, loading, []string{"Searching YouTube Music"}, nil)
			checkFrame(t, m, width, 24)

			reply := cmd().(spotSearchResultsMsg)
			reply.query, reply.tracks = long, tracksOf("a", "b")
			updated, _ := m.Update(reply)
			m = updated.(Model)
			results := viewText(m)
			wantInView(t, results, []string{"YouTube Music results", "  1/2 "}, nil)
			checkFrame(t, m, width, 24)
			t.Logf("%d columns:\n%s\n%s", width, lineWith(loading, "Searching"), lineWith(results, "results"))
			if width >= 120 {
				wantInView(t, loading+results, []string{"Searching YouTube Music for “" + long + "”…", "YouTube Music results for “" + long + "”  1/2"}, nil)
			}
		})
	}
}

func lineWith(view, s string) string {
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, s) {
			return l
		}
	}
	return ""
}

// Editing the query while its search is in flight ends that search: the
// screen stops loading, the old reply is ignored, and Enter searches for
// the edited query. It used to stay on "Searching..." with Enter inert.
func TestLiveSearchEditWhileLoading(t *testing.T) {
	edits := map[string]func(Model) Model{
		"typing": func(m Model) Model { m, _ = pressKey(m, "s"); return m },
		"paste": func(m Model) Model {
			updated, _ := m.Update(tea.PasteMsg{Content: "s"})
			return updated.(Model)
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			m := liveSearchModel(t, "Spotify", "daphni")
			m, first := pressKey(m, "enter")
			if !m.spotSearch.loading {
				t.Fatal("no search in flight")
			}
			m = edit(m)
			if m.spotSearch.query != "daphnis" || m.spotSearch.loading {
				t.Fatalf("after the edit: query %q, loading %v", m.spotSearch.query, m.spotSearch.loading)
			}
			wantInView(t, viewText(m), []string{"Search Spotify: daphnis"}, []string{"Searching Spotify"})

			// The old reply lands late and changes nothing.
			stale := first().(spotSearchResultsMsg)
			stale.tracks = tracksOf("stale")
			updated, _ := m.Update(stale)
			m = updated.(Model)
			if m.spotSearch.screen != spotSearchInput || len(m.spotSearch.results) != 0 || m.spotSearch.loading {
				t.Fatalf("the stale reply landed: screen %v, %d results, loading %v", m.spotSearch.screen, len(m.spotSearch.results), m.spotSearch.loading)
			}

			// Enter searches for the edited query, and its reply lands.
			m, second := pressKey(m, "enter")
			if second == nil || !m.spotSearch.loading {
				t.Fatalf("Enter started no search: cmd nil %v, loading %v", second == nil, m.spotSearch.loading)
			}
			reply := second().(spotSearchResultsMsg)
			if reply.query != "daphnis" {
				t.Fatalf("searched for %q", reply.query)
			}
			reply.tracks = tracksOf("fresh")
			updated, _ = m.Update(reply)
			m = updated.(Model)
			if m.spotSearch.screen != spotSearchResults || len(m.spotSearch.results) != 1 || m.spotSearch.results[0].Path != "fresh" {
				t.Errorf("the new reply did not land: screen %v, results %v", m.spotSearch.screen, m.spotSearch.results)
			}
		})
	}
}

// Moving the cursor in the query is not an edit: the search stays in flight
// and its reply still lands.
func TestLiveSearchCursorMoveKeepsTheSearch(t *testing.T) {
	m := liveSearchModel(t, "Spotify", "daphnis")
	m, cmd := pressKey(m, "enter")
	m, _ = pressKey(m, "left")
	if !m.spotSearch.loading {
		t.Fatal("a cursor move ended the search")
	}
	reply := cmd().(spotSearchResultsMsg)
	reply.tracks = tracksOf("t")
	updated, _ := m.Update(reply)
	if m = updated.(Model); m.spotSearch.screen != spotSearchResults {
		t.Error("the reply was dropped after a cursor move")
	}
}
