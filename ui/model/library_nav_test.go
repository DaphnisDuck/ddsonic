package model

// ddsonic: library navigation stack behavior, driven through Update so the
// handleKey/Update hooks are exercised too.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

func libKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// libPress sends key through Update and runs a returned library command,
// delivering its message too.
func libPress(t *testing.T, m Model, key string) Model {
	t.Helper()
	updated, cmd := m.Update(libKey(key))
	m = updated.(Model)
	return libRun(t, m, cmd)
}

func libRun(t testing.TB, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case libraryLoadedMsg, libraryPlayMsg, libraryAuthDoneMsg, librarySearchTickMsg, libFavoriteSavedMsg:
		updated, next := m.Update(msg)
		return libRun(t, updated.(Model), next)
	}
	return m
}

func tracksOf(paths ...string) []playlist.Track {
	out := make([]playlist.Track, len(paths))
	for i, p := range paths {
		out[i] = playlist.Track{Path: p, Title: p}
	}
	return out
}

func newLibraryModel(root library.Level) Model {
	m := Model{player: &playbackFakeEngine{}, playlist: playlist.New(), plVisible: 10}
	m.SetLibrary(root)
	return m
}

func testLibraryRoot() library.Level {
	album := library.TrackLevel("Mahler 5", nil, func(context.Context) ([]playlist.Track, error) {
		return tracksOf("I", "II", "III"), nil
	})
	albums := library.Menu("Albums",
		library.Entry{Title: "Bruckner 7", Open: library.Menu("Bruckner 7")},
		library.Entry{Title: "Mahler 5", Open: album},
	)
	return library.Menu("Music",
		library.Entry{Title: "Local", Open: library.Menu("Local")},
		library.Entry{Title: "Spotify", Open: library.Menu("Spotify", library.Entry{Title: "Albums", Open: albums})},
		library.Entry{Title: "Search", Intent: library.IntentSearch},
	)
}

func libCrumb(m Model) string { return m.libBreadcrumb() }

func TestLibraryPushPopRestoresCursor(t *testing.T) {
	m := newLibraryModel(testLibraryRoot())
	if m.activeScreen() != screenLibrary {
		t.Fatalf("activeScreen = %v, want library", m.activeScreen())
	}
	for _, k := range []string{"j", "l", "enter", "j", "l"} {
		m = libPress(t, m, k)
	}
	if got := libCrumb(m); got != "Music / Spotify / Albums / Mahler 5" {
		t.Fatalf("breadcrumb = %q", got)
	}
	if n := len(m.libTop().entries); n != 3 {
		t.Fatalf("album tracks = %d, want 3", n)
	}

	m = libPress(t, m, "h")
	if got := libCrumb(m); got != "Music / Spotify / Albums" || m.libTop().cursor != 1 {
		t.Fatalf("after back: %q cursor %d, want Albums with cursor on Mahler 5", got, m.libTop().cursor)
	}
	m = libPress(t, m, "esc")
	m = libPress(t, m, "esc")
	if got := libCrumb(m); got != "Music" || m.libTop().cursor != 1 {
		t.Fatalf("at root: %q cursor %d, want Music with cursor on Spotify", got, m.libTop().cursor)
	}
}

func TestLibraryMovementKeys(t *testing.T) {
	tests := []struct {
		keys []string
		want int
	}{
		{[]string{"j"}, 1},
		{[]string{"k"}, 2}, // wraps to the bottom
		{[]string{"G"}, 2},
		{[]string{"G", "g"}, 0},
		{[]string{"j", "j", "j"}, 0}, // wraps to the top
	}
	for _, tt := range tests {
		m := newLibraryModel(testLibraryRoot())
		for _, k := range tt.keys {
			m = libPress(t, m, k)
		}
		if got := m.libTop().cursor; got != tt.want {
			t.Errorf("keys %v: cursor = %d, want %d", tt.keys, got, tt.want)
		}
	}
}

func TestLibraryDropsStaleLoad(t *testing.T) {
	slowA := library.NewLevel("A", func(context.Context) ([]library.Entry, error) {
		return []library.Entry{{Title: "from A"}}, nil
	})
	root := library.Menu("Music", library.Entry{Title: "A", Open: slowA}, library.Entry{Title: "B", Open: library.Menu("B")})
	m := newLibraryModel(root)

	updated, staleCmd := m.Update(libKey("enter")) // open A, keep its load pending
	m = updated.(Model)
	m = libPress(t, m, "h")
	m = libPress(t, m, "j")
	m = libPress(t, m, "enter") // open B and load it
	updated, _ = m.Update(staleCmd())
	m = updated.(Model)
	if got := libCrumb(m); got != "Music / B" || len(m.libTop().entries) != 0 {
		t.Fatalf("stale A load leaked into %q: %+v", got, m.libTop().entries)
	}
}

func TestLibraryPlaysTrackInContext(t *testing.T) {
	m := newLibraryModel(testLibraryRoot())
	for _, k := range []string{"j", "l", "l", "j", "l", "j", "enter"} {
		m = libPress(t, m, k)
	}
	if m.playlist.Len() != 3 || m.playlist.Index() != 1 {
		t.Fatalf("queue = %d tracks at %d, want album of 3 at track II", m.playlist.Len(), m.playlist.Index())
	}
	if m.activeScreen() != screenLibrary {
		t.Error("playing left the library")
	}
}

func TestLibraryGatesKeys(t *testing.T) {
	m := newLibraryModel(testLibraryRoot())
	tests := []struct {
		key         string
		wantHandled bool
	}{
		{"S", true}, // provider jump: swallowed
		{"t", true}, // theme picker: swallowed
		{"o", true}, // file browser: swallowed
		{"+", false},
		{"s", false},
	}
	for _, tt := range tests {
		_, handled := m.handleLibraryKey(libKey(tt.key))
		if handled != tt.wantHandled {
			t.Errorf("key %q handled = %v, want %v", tt.key, handled, tt.wantHandled)
		}
	}

	m = libPress(t, m, "tab")
	if m.activeScreen() != screenMain {
		t.Fatalf("tab: activeScreen = %v, want the queue", m.activeScreen())
	}
	for key, wantHandled := range map[string]bool{"t": true, "j": false, "enter": false} {
		if _, handled := m.handleLibraryKey(libKey(key)); handled != wantHandled {
			t.Errorf("queue key %q handled = %v, want %v", key, handled, wantHandled)
		}
	}
	m = libPress(t, m, "tab")
	if m.activeScreen() != screenLibrary {
		t.Error("tab from the queue did not return to the library")
	}
}

type libAuthProv struct {
	authed bool
}

func (p *libAuthProv) Name() string                                { return "Auth" }
func (p *libAuthProv) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (p *libAuthProv) Tracks(string) ([]playlist.Track, error) {
	if !p.authed {
		return nil, playlist.ErrNeedsAuth
	}
	return tracksOf("x"), nil
}
func (p *libAuthProv) Authenticate() error { p.authed = true; return nil }

func TestLibrarySignInRetriesLoad(t *testing.T) {
	prov := &libAuthProv{}
	liked := library.TrackLevel("Liked", prov, func(context.Context) ([]playlist.Track, error) { return prov.Tracks("") })
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Liked", Open: liked}))
	m.SetLibrarySignIn("auth", prov) // only a registered provider signs in (library_signin.go)

	m = libPress(t, m, "enter")
	if m.libTop().err == nil {
		t.Fatal("expected a sign-in error before authenticating")
	}
	m = libPress(t, m, "enter") // sign in, then reload
	if m.libTop().err != nil || len(m.libTop().entries) != 1 {
		t.Fatalf("after sign-in: err %v, entries %d", m.libTop().err, len(m.libTop().entries))
	}
}

// catLevel is a catalog-backed level whose rows the test can change, as a
// sync would.
type catLevel struct {
	title string
	rows  *[]library.Entry
	loads *int
}

func (c catLevel) Title() string { return c.title }
func (c catLevel) Load(context.Context) ([]library.Entry, error) {
	*c.loads++
	return slices.Clone(*c.rows), nil
}
func (catLevel) CatalogProvider() string { return "spotify" }

func TestCatalogSyncRefreshesVisibleLevelKeepingCursor(t *testing.T) {
	rows := []library.Entry{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}
	loads := 0
	albums := catLevel{"Albums", &rows, &loads}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: albums}))
	m = libPress(t, m, "enter")
	m = libPress(t, m, "j") // cursor on B
	if m.libTop().cursor != 1 {
		t.Fatalf("cursor = %d", m.libTop().cursor)
	}

	// A sync adds an album before B; the cursor must stay on B.
	rows = []library.Entry{{ID: "a", Title: "A"}, {ID: "z", Title: "Z"}, {ID: "b", Title: "B"}, {ID: "c", Title: "C"}}
	updated, cmd := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncCollectionDone, Collection: "albums"})
	m = libRun(t, updated.(Model), cmd)
	if loads != 2 || len(m.libTop().entries) != 4 || m.libTop().entries[m.libTop().cursor].ID != "b" {
		t.Errorf("after sync: loads=%d cursor on %q, want reload with cursor kept on b",
			loads, m.libTop().entries[m.libTop().cursor].ID)
	}

	// Another provider's sync leaves the level alone.
	updated, cmd = m.Update(CatalogSyncMsg{Provider: "tidal", Phase: CatalogSyncCollectionDone})
	m = libRun(t, updated.(Model), cmd)
	if loads != 2 {
		t.Errorf("another provider's sync reloaded the level (loads=%d)", loads)
	}
}

func TestCatalogSyncReloadsStaleParentOnBack(t *testing.T) {
	rows := []library.Entry{{ID: "a", Title: "A", Open: library.Menu("A")}}
	loads := 0
	albums := catLevel{"Albums", &rows, &loads}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: albums}))
	m = libPress(t, m, "enter") // Albums
	m = libPress(t, m, "enter") // A (a plain menu, not catalog)
	updated, cmd := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncCollectionDone})
	m = libRun(t, updated.(Model), cmd)
	if loads != 1 {
		t.Fatalf("a hidden level reloaded while not visible (loads=%d)", loads)
	}
	rows = append(rows, library.Entry{ID: "b", Title: "B"})
	m = libPress(t, m, "h")
	if loads != 2 || len(m.libTop().entries) != 2 {
		t.Errorf("stale Albums after Back: loads=%d entries=%d, want a reload", loads, len(m.libTop().entries))
	}
}

// A refresh that lands after search opened over its level still updates it.
func TestCatalogRefreshLandsBehindSearch(t *testing.T) {
	rows := []library.Entry{{ID: "a", Title: "A", Open: library.Menu("A")}}
	loads := 0
	albums := catLevel{"Albums", &rows, &loads}
	search := fakeSearch{loads: &[]string{}, results: func(string) []library.Entry { return nil }}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: albums},
		library.Entry{Title: "Search", Open: search}))
	m = libPress(t, m, "enter") // Albums
	rows = append(rows, library.Entry{ID: "b", Title: "B"})
	updated, refresh := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncCollectionDone})
	m = updated.(Model)
	m = libPress(t, m, "/") // search opens over Albums while the refresh is in flight
	m = libRun(t, m, refresh)
	m = libPress(t, m, "esc") // close search
	if got := libCrumb(m); got != "Music / Albums" {
		t.Fatalf("after closing search: %q", got)
	}
	if got := len(m.libTop().entries); got != 2 {
		t.Errorf("Albums after search = %d rows, want the refreshed 2", got)
	}
}

func TestCatalogSyncBadgeAndRefreshKey(t *testing.T) {
	m := newLibraryModel(testLibraryRoot())
	if m.libSyncBadge() != "" {
		t.Fatal("badge shown without a catalog")
	}
	var refreshed []string
	m.SetCatalogSync(map[string]CatalogStatus{"spotify": {LastSuccess: time.Now().Add(-2 * time.Minute)}}, func(p string) { refreshed = append(refreshed, p) })
	if got := m.libSyncBadge(); got != "✓ synced 2m ago" {
		t.Errorf("badge = %q", got)
	}

	updated, _ := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncStarted})
	m = updated.(Model)
	if got := m.libSyncBadge(); got != "↻ syncing" {
		t.Errorf("badge while syncing = %q", got)
	}
	updated, _ = m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncFinished, Err: errors.New("http status 503")})
	m = updated.(Model)
	if got := m.libSyncBadge(); got != "sync failed · cached" {
		t.Errorf("badge after failure = %q", got)
	}
	updated, _ = m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncFinished})
	m = updated.(Model)
	if got := m.libSyncBadge(); got != "✓ synced just now" {
		t.Errorf("badge after success = %q", got)
	}

	// At the Music root, r syncs every provider.
	updated, _ = m.Update(libKey("r"))
	m = updated.(Model)
	if !slices.Equal(refreshed, []string{""}) {
		t.Errorf("refresh calls = %q, want one for every provider", refreshed)
	}

	// With several providers, each badge names its provider.
	m.SetCatalogSync(map[string]CatalogStatus{
		"spotify": {LastError: "http status 503"},
		"local":   {LastSuccess: time.Now()},
	}, nil)
	if got := m.libSyncBadge(); got != "Local ✓ synced just now · Spotify sync failed · cached" {
		t.Errorf("badge with two providers = %q", got)
	}
}

// r syncs the provider of the catalog level being browsed, including from a
// plain level opened beneath it.
func TestRefreshKeySyncsTheBrowsedProvider(t *testing.T) {
	rows := []library.Entry{{ID: "a", Title: "A", Open: library.Menu("A", library.Entry{Title: "x"})}}
	loads := 0
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: catLevel{"Albums", &rows, &loads}}))
	var refreshed []string
	m.SetCatalogSync(map[string]CatalogStatus{"spotify": {}}, func(p string) { refreshed = append(refreshed, p) })
	m = libPress(t, m, "enter") // Albums
	m = libPress(t, m, "r")
	m = libPress(t, m, "enter") // A, a plain level under Albums
	m = libPress(t, m, "r")
	if !slices.Equal(refreshed, []string{"spotify", "spotify"}) {
		t.Errorf("refresh calls = %q, want spotify twice", refreshed)
	}
}

// allLevel is a catalog level of every source.
type allLevel struct{ catLevel }

func (allLevel) CatalogProvider() string { return "" }

// A level of every source reloads after any source's sync.
func TestCatalogSyncRefreshesAllSourceLevels(t *testing.T) {
	rows := []library.Entry{{ID: "a", Title: "A"}}
	loads := 0
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "All Music", Open: allLevel{catLevel{"Albums", &rows, &loads}}}))
	m = libPress(t, m, "enter")
	for _, p := range []string{"spotify", "local"} {
		updated, cmd := m.Update(CatalogSyncMsg{Provider: p, Phase: CatalogSyncCollectionDone})
		m = libRun(t, updated.(Model), cmd)
	}
	if loads != 3 {
		t.Errorf("loads = %d, want the first and one per source's sync", loads)
	}
}
