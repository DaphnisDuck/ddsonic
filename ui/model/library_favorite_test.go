package model

// ddsonic: f stars and unstars the directory station the view in front has
// selected (RADIO-001). cliamp's own tests of the star run without the
// library, where its gate does not exist, so these enable it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// favFixture is what newFavModel builds its library from.
type favFixture struct {
	favs     *radio.Favorites
	uno, dos radio.CatalogStation
	loads    *[]string // the search level's queries, one per load
}

// newFavModel is a library over a real radio provider and favorites store,
// in a config directory of its own:
//
//	Radio          the library's own Radio menu (Favorites, Browse Stations)
//	Chile          a directory list, titled as a country list decorates it
//	cliamp radio   a channel row, which plays but is no directory station
//	Mixed          a directory station among streams that are not
//	Search         results holding a station row and a directory search
func newFavModel(t *testing.T) (Model, favFixture) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	fx := favFixture{
		favs:  radio.LoadFavorites(),
		uno:   radio.CatalogStation{Name: "Radio Uno", URL: "https://uno.example/stream", Country: "Chile", Codec: "MP3", Bitrate: 128, Tags: "news"},
		dos:   radio.CatalogStation{Name: "Radio Dos", URL: "https://dos.example/stream", Country: "Chile"},
		loads: &[]string{},
	}
	prov := radio.New(radio.Options{Favorites: fx.favs, Country: radio.CountryDeclined})
	prov.AppendCatalog([]radio.CatalogStation{fx.uno, fx.dos})
	station := func(id, title string) playlist.Track {
		tracks, err := prov.Tracks(id)
		if err != nil || len(tracks) != 1 {
			t.Fatalf("station %s: %v, %v", id, tracks, err)
		}
		tracks[0].Title = title // as a directory list shows it, not as it is saved
		return tracks[0]
	}
	uno, dos := station("c:0", "Radio Uno [Chile · MP3 128k]"), station("c:1", "Radio Dos [Chile]")
	channel := playlist.Track{Path: "https://channel.example/groove", Title: "Groove", Stream: true, Realtime: true}
	custom := prov.LocalStationTracks()[0] // the built-in station, as radios.toml's are
	httpTrack := playlist.Track{Path: "https://files.example/song.mp3", Title: "A song", Stream: true}
	file := playlist.Track{Path: "/music/song.flac", Title: "A file"}

	entries, err := library.Root(library.Sources{Radio: prov}).Load(context.Background())
	if err != nil || len(entries) == 0 || entries[0].Title != "Radio" {
		t.Fatalf("library root = %+v, %v; want Radio first", entries, err)
	}
	tracks := func(title string, list ...playlist.Track) library.Level {
		return library.TrackLevel(title, prov, func(context.Context) ([]playlist.Track, error) { return slices.Clone(list), nil })
	}
	search := catalogSearch{fakeSearch{loads: fx.loads, results: func(string) []library.Entry {
		found := uno
		return []library.Entry{
			{ID: "station:1", Title: "Radio Uno", Detail: "Radio", Section: "Stations", Track: &found, Source: catalog.Radio,
				PlayFrom: func(context.Context) ([]playlist.Track, int, error) { return []playlist.Track{found}, 0, nil }},
			{Title: "Search the radio directory", Section: "Beyond your library", Source: catalog.Radio,
				Open: tracks("Radio directory", dos, uno)},
		}
	}}}
	root := library.Menu("Music",
		library.Entry{Title: "Radio", Open: entries[0].Open, Source: catalog.Radio},
		library.Entry{Title: "Chile", Open: tracks("Chile", uno, dos), Source: catalog.Radio},
		library.Entry{Title: "cliamp radio", Open: library.Menu("cliamp radio", library.Entry{Title: "Groove",
			Play: func(context.Context) ([]playlist.Track, error) { return []playlist.Track{channel}, nil }})},
		library.Entry{Title: "Mixed", Open: tracks("Mixed", uno, channel, custom, httpTrack, file)},
		library.Entry{Title: "Search", Open: search},
	)
	m := newLibraryModel(root)
	m.SetRadioFavorites(fx.favs)
	return m, fx
}

// catalogSearch is a search level read from every source's catalog, as the
// library's own is.
type catalogSearch struct{ fakeSearch }

func (catalogSearch) CatalogProvider() string { return "" }
func (s catalogSearch) WithQuery(q string) library.SearchLevel {
	return catalogSearch{s.fakeSearch.WithQuery(q).(fakeSearch)}
}

// libEnter opens (or plays) the top level's row titled title.
func libEnter(t *testing.T, m Model, title string) Model {
	t.Helper()
	libSelect(t, &m, title)
	return libPress(t, m, "enter")
}

// libSelect moves the cursor to the top level's row titled title.
func libSelect(t *testing.T, m *Model, title string) {
	t.Helper()
	f := m.libTop()
	for i, e := range f.entries {
		if e.Title == title {
			f.cursor = i
			return
		}
	}
	t.Fatalf("no row %q in %q", title, f.level.Title())
}

func libRowTitles(m Model) []string {
	var titles []string
	for _, e := range m.libTop().entries {
		titles = append(titles, e.Title)
	}
	return titles
}

// favKeyHint is the key bar's hint for f, or "" when the bar has none.
func favKeyHint(m Model) string {
	v := m.libraryKeyView()
	for _, r := range slices.Concat(v.own, v.pass) {
		if slices.Contains(r.keys, "f") {
			return r.label
		}
	}
	return ""
}

// favSaved is the favorites as a fresh start reads them from disk.
func favSaved() []radio.CatalogStation { return radio.LoadFavorites().Stations() }

// The contexts f works in. Each selects Radio Uno while something else is
// playing or selected elsewhere, so a toggle of the wrong selection shows.
var favContexts = []struct {
	name string
	open func(t *testing.T, m Model) Model
}{
	{"country or tag list", func(t *testing.T, m Model) Model {
		m = libEnter(t, m, "Chile")
		m = libEnter(t, m, "Radio Dos [Chile]") // plays Dos: the queue's selection is Dos
		libSelect(t, &m, "Radio Uno [Chile · MP3 128k]")
		return m
	}},
	{"directory search results", func(t *testing.T, m Model) Model {
		m = libEnter(t, m, "Search")
		m = libPress(t, typeText(t, m, "uno"), "enter")
		m = libEnter(t, m, "Search the radio directory")
		libSelect(t, &m, "Radio Uno [Chile · MP3 128k]")
		return m
	}},
	{"search results", func(t *testing.T, m Model) Model {
		m = libEnter(t, m, "Search")
		m = libPress(t, typeText(t, m, "uno"), "enter")
		libSelect(t, &m, "Radio Uno")
		return m
	}},
	{"queue", func(t *testing.T, m Model) Model {
		m = libEnter(t, m, "Chile")
		m = libEnter(t, m, "Radio Dos [Chile]") // the library's selection stays on Dos
		m = libPress(t, m, "tab")
		return libPress(t, m, "k") // the queue's selection: Uno
	}},
}

func TestFavoriteKeyTogglesTheSelectedStation(t *testing.T) {
	for _, tt := range favContexts {
		t.Run(tt.name, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = tt.open(t, m)
			queued := m.playlist.Len()
			if got := favKeyHint(m); got != "Favorite" {
				t.Errorf("key bar hint for f = %q, want Favorite", got)
			}

			m = libPress(t, m, "f")
			// The station as the directory names it: not the decorated row
			// title, and not whatever else is selected or playing.
			if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.uno}) {
				t.Fatalf("after f, favorites = %+v, want only %+v", got, fx.uno)
			}
			if got := favSaved(); !slices.Equal(got, []radio.CatalogStation{fx.uno}) {
				t.Errorf("after f, saved favorites = %+v, want only %+v", got, fx.uno)
			}
			if m.playlist.Len() != queued {
				t.Errorf("f changed the queue: %d tracks, was %d", m.playlist.Len(), queued)
			}
			if !strings.Contains(m.status.text, "Radio Uno") {
				t.Errorf("status = %q, want it to name the station", m.status.text)
			}
			if got := favKeyHint(m); got != "Unfavorite" {
				t.Errorf("key bar hint for f on a favorite = %q, want Unfavorite", got)
			}

			m = libPress(t, m, "f")
			if got := fx.favs.Stations(); len(got) != 0 {
				t.Fatalf("after a second f, favorites = %+v, want none", got)
			}
			if got := favSaved(); len(got) != 0 {
				t.Errorf("after a second f, saved favorites = %+v, want none", got)
			}
			if got := favKeyHint(m); got != "Favorite" {
				t.Errorf("key bar hint for f after unstarring = %q, want Favorite", got)
			}
		})
	}
}

// Starring needs nothing to be playing, and leaves playing and moving as
// they were.
func TestFavoriteKeyNeedsNoPlayback(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Chile")
	m = libPress(t, m, "f")
	if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.uno}) {
		t.Fatalf("favorites = %+v, want %+v starred with nothing played", got, fx.uno)
	}
	if m.playlist.Len() != 0 {
		t.Fatalf("f queued %d tracks", m.playlist.Len())
	}
	m = libPress(t, m, "j")
	if got := m.libTop().cursor; got != 1 {
		t.Errorf("j after f: cursor %d, want 1", got)
	}
	m = libPress(t, m, "enter")
	if cur, _ := m.playlist.Current(); cur.Path != fx.dos.URL || m.playlist.Len() != 2 {
		t.Errorf("Enter after f plays %q of %d, want %q of the list of 2", cur.Path, m.playlist.Len(), fx.dos.URL)
	}
}

func TestFavoritesListFollowsTheToggle(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Chile")
	m = libPress(t, m, "f") // Uno
	m = libPress(t, m, "esc")
	m = libEnter(t, m, "Radio")
	m = libEnter(t, m, "Favorites")
	if got := libRowTitles(m); !slices.Equal(got, []string{"Radio Uno"}) {
		t.Fatalf("Favorites = %v, want the starred station", got)
	}

	// Unstarred in Favorites, it leaves the list at once.
	m = libPress(t, m, "f")
	if got := libRowTitles(m); len(got) != 0 {
		t.Fatalf("Favorites after unstarring = %v, want empty", got)
	}
	if len(fx.favs.Stations()) != 0 {
		t.Fatalf("favorites = %+v, want none", fx.favs.Stations())
	}

	// Starred from the queue while Favorites is the list behind it.
	m = libPress(t, m, "esc")
	m = libPress(t, m, "esc")
	m = libEnter(t, m, "Chile")
	m = libEnter(t, m, "Radio Uno [Chile · MP3 128k]")
	m = libPress(t, m, "esc")
	m = libEnter(t, m, "Radio")
	m = libEnter(t, m, "Favorites")
	m = libPress(t, m, "tab")
	m = libPress(t, m, "f")
	m = libPress(t, m, "tab")
	if got := libRowTitles(m); !slices.Equal(got, []string{"Radio Uno"}) {
		t.Fatalf("Favorites after starring in the queue = %v, want the station", got)
	}

	// Unstarred from search results opened over Favorites: the list is
	// reread on the way back.
	m = libPress(t, m, "/")
	m = libPress(t, typeText(t, m, "uno"), "enter")
	libSelect(t, &m, "Radio Uno")
	m = libPress(t, m, "f")
	m = libPress(t, m, "esc") // the query
	m = libPress(t, m, "esc") // Favorites
	if title := m.libTop().level.Title(); title != "Favorites" {
		t.Fatalf("back from search: %q, want Favorites", title)
	}
	if got := libRowTitles(m); len(got) != 0 {
		t.Errorf("Favorites behind the search after unstarring = %v, want empty", got)
	}
}

// What is not a directory station is neither starred nor offered the key,
// and f still stops at the library: cliamp's own star never sees it.
func TestFavoriteKeyIgnoresWhatIsNotADirectoryStation(t *testing.T) {
	queueAt := func(title string) func(*testing.T, Model) Model {
		return func(t *testing.T, m Model) Model {
			m = libEnter(t, m, "Mixed")
			m = libEnter(t, m, title)
			return libPress(t, m, "tab")
		}
	}
	rowAt := func(level, title string) func(*testing.T, Model) Model {
		return func(t *testing.T, m Model) Model {
			m = libEnter(t, m, level)
			libSelect(t, &m, title)
			return m
		}
	}
	tests := []struct {
		name string
		open func(t *testing.T, m Model) Model
	}{
		{"menu row", func(t *testing.T, m Model) Model { libSelect(t, &m, "Radio"); return m }},
		{"Favorites menu row", rowAt("Radio", "Favorites")},
		{"cliamp radio channel row", rowAt("cliamp radio", "Groove")},
		{"cliamp radio channel stream row", rowAt("Mixed", "Groove")},
		{"radios.toml station row", rowAt("Mixed", "cliamp radio")},
		{"HTTP track row", rowAt("Mixed", "A song")},
		{"file row", rowAt("Mixed", "A file")},
		{"search row that opens the directory", func(t *testing.T, m Model) Model {
			m = libEnter(t, m, "Search")
			m = libPress(t, typeText(t, m, "uno"), "enter")
			libSelect(t, &m, "Search the radio directory")
			return m
		}},
		{"queued cliamp radio channel stream", queueAt("Groove")},
		{"queued radios.toml station", queueAt("cliamp radio")},
		{"queued HTTP track", queueAt("A song")},
		{"queued file", queueAt("A file")},
		{"empty queue", func(t *testing.T, m Model) Model { return libPress(t, m, "tab") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = tt.open(t, m)
			if got := favKeyHint(m); got != "" {
				t.Errorf("key bar offers f (%s)", got)
			}
			if _, handled := m.handleLibraryKey(libKey("f")); !handled {
				t.Error("f reaches cliamp's handlers")
			}
			m = libPress(t, m, "f")
			if got := fx.favs.Stations(); len(got) != 0 {
				t.Errorf("favorites = %+v, want none", got)
			}
			if got := favSaved(); len(got) != 0 {
				t.Errorf("saved favorites = %+v, want none", got)
			}
		})
	}
}

// While the query is being typed, f is a letter.
func TestFavoriteKeyIsTextInTheSearchInput(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Search")
	m = typeText(t, m, "f")
	sl, _ := m.libSearchLevel()
	if sl.Query() != "f" || len(fx.favs.Stations()) != 0 || favKeyHint(m) != "" {
		t.Errorf("query %q, favorites %+v, hint %q; want the letter typed and nothing starred",
			sl.Query(), fx.favs.Stations(), favKeyHint(m))
	}
}

// The key bar and the keymap entries show f where it works.
func TestFavoriteKeyIsInTheKeyBar(t *testing.T) {
	for _, tt := range favContexts {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newFavModel(t)
			m = tt.open(t, m)
			bar := strings.Join(strings.Fields(ansi.Strip(strings.Join(m.libKeyBarLines(200), "\n"))), " ")
			if !strings.Contains(bar, " f Favorite ") {
				t.Errorf("key bar = %q, want f Favorite", bar)
			}
			if !slices.ContainsFunc(m.libraryKeymapEntries(), func(e keymapEntry) bool {
				return e.key == "f" && e.action == "Favorite"
			}) {
				t.Errorf("keymap entries lack f: %+v", m.libraryKeymapEntries())
			}
		})
	}
}

// A save that fails says so and stars nothing.
func TestFavoriteKeyReportsAFailedSave(t *testing.T) {
	m, _ := newFavModel(t)
	m = libEnter(t, m, "Chile")
	// A store whose directory cannot be made: /dev/null is not a directory.
	t.Setenv("CLIAMP_CONFIG_DIR", "/dev/null/nowhere")
	blocked := radio.LoadFavorites()
	m.SetRadioFavorites(blocked)
	m = libPress(t, m, "f")
	if len(blocked.Stations()) != 0 {
		t.Errorf("favorites = %+v, want none", blocked.Stations())
	}
	if !strings.Contains(m.status.text, "Favorite save failed") {
		t.Errorf("status = %q, want the failure", m.status.text)
	}
}

// A sync of a source that shows no status (radio's, after a toggle) rereads
// the search results and leaves the header's badges alone.
func TestCatalogChangeRereadsSearchWithoutStatus(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Search")
	m = libPress(t, typeText(t, m, "uno"), "enter")
	loads, badge := len(*fx.loads), m.libSyncBadge()

	updated, cmd := m.Update(CatalogChangedMsg{Provider: catalog.Radio})
	m = libRun(t, updated.(Model), cmd)
	if got := len(*fx.loads); got != loads+1 {
		t.Errorf("search loads = %d, want %d: one reread", got, loads+1)
	}
	if got := m.libSyncBadge(); got != badge || len(m.lib.sync) != 0 {
		t.Errorf("sync badge = %q with %d sources, want %q and none", got, len(m.lib.sync), badge)
	}
}

// A list that is loading or failed selects nothing, as Enter has it: its
// rows, if any still show, are not acted on.
func TestFavoriteKeyWaitsForTheList(t *testing.T) {
	for name, hold := range map[string]func(*libFrame){
		"loading": func(f *libFrame) { f.cancel = func() {} },
		"failed":  func(f *libFrame) { f.err = context.DeadlineExceeded },
	} {
		t.Run(name, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = libEnter(t, m, "Chile")
			hold(m.libTop())
			if got := favKeyHint(m); got != "" {
				t.Errorf("key bar offers f (%s)", got)
			}
			m = libPress(t, m, "f")
			if got := fx.favs.Stations(); len(got) != 0 {
				t.Errorf("favorites = %+v, want none", got)
			}
		})
	}
}

// A station starred some other way (the remote's provider.favorite, another
// ddsonic) reaches the store, whose resync tells the UI: an open Favorites
// list follows, and a list of another source's does not reread.
func TestFavoritesListFollowsAChangeMadeElsewhere(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Radio")
	m = libEnter(t, m, "Favorites")
	if _, err := fx.favs.Toggle(fx.dos); err != nil {
		t.Fatal(err)
	}
	if got := libRowTitles(m); len(got) != 0 {
		t.Fatalf("Favorites before the UI is told = %v", got)
	}
	updated, cmd := m.Update(CatalogChangedMsg{Provider: catalog.Spotify})
	m = libRun(t, updated.(Model), cmd)
	if got := libRowTitles(m); len(got) != 0 {
		t.Errorf("Favorites after Spotify changed = %v, want it left alone", got)
	}
	updated, cmd = m.Update(CatalogChangedMsg{Provider: catalog.Radio})
	m = libRun(t, updated.(Model), cmd)
	if got := libRowTitles(m); !slices.Equal(got, []string{"Radio Dos"}) {
		t.Errorf("Favorites after radio changed = %v, want the station", got)
	}
}

// A directory station the remote loaded into the queue (provider.load) is a
// directory station like any other: cliamp's star leaves such a queue to
// its bookmarks, the library's f does not.
func TestFavoriteKeyOnAStationLoadedOverIPC(t *testing.T) {
	m, fx := newFavModel(t)
	prov := radio.New(radio.Options{Favorites: fx.favs, Country: radio.CountryDeclined})
	prov.AppendCatalog([]radio.CatalogStation{fx.uno})
	m.providers = []ProviderEntry{{Key: "radio", Name: "Radio", Provider: prov}}
	req := ipc.LibraryRequestMsg{Op: "provider.load", Provider: "radio", Playlist: "c:0", Reply: make(chan ipc.Response, 1)}
	m.handleIPCProviderLoad(m.handleIPCLibrary(req)().(ipcProviderLoadResult))
	if reply := <-req.Reply; !reply.OK || m.loadedPlaylist == "" {
		t.Fatalf("provider.load: %+v, loaded playlist %q", reply, m.loadedPlaylist)
	}
	m = libPress(t, m, "tab")
	if got := favKeyHint(m); got != "Favorite" {
		t.Errorf("key bar hint for f = %q, want Favorite", got)
	}
	if m.playlistStarCount() != 0 {
		t.Errorf("stars before f = %d", m.playlistStarCount())
	}
	m = libPress(t, m, "f")
	if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.uno}) {
		t.Errorf("favorites = %+v, want %+v", got, fx.uno)
	}
	// The queue's star agrees with the key bar.
	track, _ := m.playlist.Track(m.plCursor)
	if got := favKeyHint(m); got != "Unfavorite" || !m.playlistTrackStarred(track) || m.playlistStarCount() != 1 {
		t.Errorf("after f: hint %q, starred %v, %d stars; want Unfavorite, a star, 1",
			got, m.playlistTrackStarred(track), m.playlistStarCount())
	}
	m = favSized(t, m)
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "★") {
		t.Errorf("the queue shows no star:\n%s", view)
	}
	m = libPress(t, m, "f")
	if m.playlistTrackStarred(track) || m.playlistStarCount() != 0 || favKeyHint(m) != "Favorite" {
		t.Errorf("after a second f: starred %v, %d stars, hint %q", m.playlistTrackStarred(track), m.playlistStarCount(), favKeyHint(m))
	}
}

// favSized gives m a terminal to draw in.
func favSized(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(Model)
}

// favPromptly fails the test unless fn returns within a second: the UI
// goroutine waited for a save.
func favPromptly[T any](t *testing.T, what string, fn func() T) T {
	t.Helper()
	done := make(chan T, 1)
	go func() { done <- fn() }()
	select {
	case v := <-done:
		return v
	case <-time.After(time.Second):
		t.Fatalf("%s waited for the favorites file", what)
		panic("unreachable")
	}
}

// favHoldFile takes the favorites file's lock, as another ddsonic saving its
// own toggle holds it.
func favHoldFile(t *testing.T) (release func()) {
	t.Helper()
	unlock, err := fileutil.LockFile(filepath.Join(os.Getenv("CLIAMP_CONFIG_DIR"), "radio_favorites.toml.lock"))
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release = func() {
		if !released {
			released = true
			_ = unlock()
		}
	}
	t.Cleanup(release)
	return release
}

type favUpdate struct {
	m   Model
	cmd tea.Cmd
}

func favKey(m Model, key string) favUpdate {
	updated, cmd := m.Update(libKey(key))
	return favUpdate{updated.(Model), cmd}
}

// While another ddsonic holds the favorites file, f returns at once and the
// save waits in the background: keys, the key bar and drawing go on, in a
// list and in the queue, and the star lands when the file is free.
func TestFavoriteKeyDoesNotWaitForTheFavoritesFile(t *testing.T) {
	for _, view := range []string{"list", "queue"} {
		t.Run(view, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = favSized(t, libEnter(t, m, "Chile"))
			if view == "queue" {
				m = libPress(t, libEnter(t, m, "Radio Uno [Chile · MP3 128k]"), "tab")
			}
			release := favHoldFile(t)

			pressed := favPromptly(t, "f", func() favUpdate { return favKey(m, "f") })
			m = pressed.m
			if pressed.cmd == nil {
				t.Fatal("f started no save")
			}
			saved := make(chan tea.Msg, 1)
			go func() { saved <- pressed.cmd() }()

			// The save is waiting. Nothing the UI goroutine does waits with it.
			for range 20 {
				m = favPromptly(t, "a key", func() favUpdate { return favKey(m, "j") }).m
				m = favPromptly(t, "a key", func() favUpdate { return favKey(m, "k") }).m
				favPromptly(t, "the key bar", func() string { return favKeyHint(m) })
				favPromptly(t, "the queue's stars", func() int { return m.playlistStarCount() })
				if out := favPromptly(t, "drawing", func() string { return m.View().Content }); out == "" {
					t.Fatal("nothing drawn")
				}
			}
			select {
			case msg := <-saved:
				t.Fatalf("the save did not wait for the file: %+v", msg)
			default:
			}
			if len(fx.favs.Stations()) != 0 || strings.Contains(m.status.text, "Favorited") {
				t.Errorf("starred before the save landed: %+v, status %q", fx.favs.Stations(), m.status.text)
			}

			release()
			updated, cmd := m.Update(<-saved)
			m = libRun(t, updated.(Model), cmd)
			if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.uno}) {
				t.Errorf("favorites after the save = %+v, want %+v", got, fx.uno)
			}
			if got := favKeyHint(m); got != "Unfavorite" || !strings.Contains(m.status.text, "Favorited station: Radio Uno") {
				t.Errorf("after the save: hint %q, status %q", got, m.status.text)
			}
			if view == "queue" && m.playlistStarCount() != 1 {
				t.Errorf("queue stars after the save = %d, want 1", m.playlistStarCount())
			}
		})
	}
}

// A second f on a station whose save has not landed is ignored, and the key
// bar offers none meanwhile: one press, one toggle. Another station's f goes
// ahead. Once the save lands, saved or failed, f works on it again.
func TestFavoriteKeyIgnoresARepeatWhileSaving(t *testing.T) {
	m, fx := newFavModel(t)
	m = libEnter(t, m, "Chile")

	first := favKey(m, "f")
	m = first.m
	if first.cmd == nil {
		t.Fatal("f started no save")
	}
	if got := favKeyHint(m); got != "" {
		t.Errorf("key bar offers f (%s) while its save is pending", got)
	}
	for range 3 {
		again := favKey(m, "f")
		if m = again.m; again.cmd != nil {
			t.Fatal("a repeated f started another save")
		}
	}
	other := favKey(favKey(m, "j").m, "f") // Radio Dos
	if m = other.m; other.cmd == nil {
		t.Fatal("f on another station started no save")
	}
	m = favKey(m, "k").m

	deliver := func(cmd tea.Cmd) {
		t.Helper()
		updated, next := m.Update(cmd())
		m = libRun(t, updated.(Model), next)
	}
	deliver(first.cmd)
	deliver(other.cmd)
	if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.uno, fx.dos}) {
		t.Fatalf("favorites = %+v, want each station starred once", got)
	}
	if got := favKeyHint(m); got != "Unfavorite" {
		t.Errorf("key bar hint after the save = %q, want Unfavorite", got)
	}

	// A save that fails frees the key too.
	t.Setenv("CLIAMP_CONFIG_DIR", "/dev/null/nowhere")
	m.SetRadioFavorites(radio.LoadFavorites())
	failed := favKey(m, "f")
	if m = failed.m; failed.cmd == nil {
		t.Fatal("f started no save")
	}
	deliver(failed.cmd)
	if !strings.Contains(m.status.text, "Favorite save failed") || favKeyHint(m) != "Favorite" {
		t.Errorf("after a failed save: status %q, hint %q; want the failure and f offered again", m.status.text, favKeyHint(m))
	}
}

// The queue's star marks starred directory stations and bookmarked tracks,
// and nothing else, also in a queue a provider or a saved playlist loaded.
func TestQueueStarsOnlyDirectoryStationsAndBookmarks(t *testing.T) {
	for _, loaded := range []string{"", "c:0", "My playlist"} {
		t.Run("loaded="+loaded, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = libEnter(t, m, "Mixed")
			m = libEnter(t, m, "Radio Uno [Chile · MP3 128k]")
			m.loadedPlaylist = loaded
			// Starred: Uno, and a station streaming from the HTTP track's
			// address, which that track does not become.
			for _, s := range []radio.CatalogStation{fx.uno, {Name: "Same address", URL: "https://files.example/song.mp3"}} {
				if _, err := fx.favs.Toggle(s); err != nil {
					t.Fatal(err)
				}
			}
			m.playlist.ToggleBookmark(4) // the file
			var starred []string
			for i := range m.playlist.Len() {
				if track, _ := m.playlist.Track(i); m.playlistTrackStarred(track) {
					starred = append(starred, track.Title)
				}
			}
			if want := []string{"Radio Uno [Chile · MP3 128k]", "A file"}; !slices.Equal(starred, want) || m.playlistStarCount() != 2 {
				t.Errorf("starred = %v (%d counted), want %v", starred, m.playlistStarCount(), want)
			}
		})
	}
}

// The remote stars a station (provider.favorite) while another ddsonic
// holds the favorites file. With Radio the active provider and history on,
// as ddsonic runs, starting a track lists the provider's rows on the UI
// goroutine; that, and everything else there, goes on while the save waits.
func TestPlayDoesNotWaitForAFavoriteSavedOverIPC(t *testing.T) {
	m, fx := newFavModel(t)
	prov := radio.New(radio.Options{Favorites: fx.favs, Country: radio.CountryDeclined})
	prov.AppendCatalog([]radio.CatalogStation{fx.uno})
	m.providers = []ProviderEntry{{Key: "radio", Name: "Radio", Provider: prov}}
	m.provider = prov
	m.historyStore = history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	m = favSized(t, libEnter(t, m, "Chile"))
	release := favHoldFile(t)

	req := ipc.LibraryRequestMsg{Op: "provider.favorite", Provider: "radio", Playlist: "c:0", Reply: make(chan ipc.Response, 1)}
	save := m.handleIPCLibrary(req)
	if save == nil {
		t.Fatalf("provider.favorite started nothing: %+v", <-req.Reply)
	}
	go save()
	time.Sleep(100 * time.Millisecond) // the save is at the file's lock by now

	played := favPromptly(t, "Enter", func() favUpdate { return favKey(m, "enter") })
	m = libRun(t, played.m, played.cmd)
	if cur, _ := m.playlist.Current(); cur.Path != fx.uno.URL {
		t.Fatalf("Enter played %q, want %q", cur.Path, fx.uno.URL)
	}
	for range 10 {
		m = favPromptly(t, "a key", func() favUpdate { return favKey(m, "j") }).m
		if out := favPromptly(t, "drawing", func() string { return m.View().Content }); out == "" {
			t.Fatal("nothing drawn")
		}
	}
	select {
	case reply := <-req.Reply:
		t.Fatalf("the save did not wait for the file: %+v", reply)
	default:
	}

	release()
	if reply := <-req.Reply; !reply.OK {
		t.Fatal(reply.Error)
	}
	if !fx.favs.Contains(fx.uno.URL) {
		t.Error("the station was not starred once the file was free")
	}
}

// The queue's star on a directory station, with the library on. A queue the
// library played marks the favorites, as cliamp does. In a queue a playlist
// or a provider loaded, a bookmark keeps its star and a favorite gets one.
func TestQueueStarOnAStationIsItsBookmarkOrItsFavorite(t *testing.T) {
	for _, loaded := range []string{"", "My playlist"} {
		for _, bookmarked := range []bool{false, true} {
			for _, favorited := range []bool{false, true} {
				t.Run(fmt.Sprintf("loaded=%q bookmarked=%v favorited=%v", loaded, bookmarked, favorited), func(t *testing.T) {
					m, fx := newFavModel(t)
					m = libEnter(t, m, "Mixed")
					m = libEnter(t, m, "Radio Uno [Chile · MP3 128k]")
					m.loadedPlaylist = loaded
					if bookmarked {
						m.playlist.ToggleBookmark(0)
					}
					if favorited {
						if _, err := fx.favs.Toggle(fx.uno); err != nil {
							t.Fatal(err)
						}
					}
					want := favorited || (loaded != "" && bookmarked)
					track, _ := m.playlist.Track(0)
					count := 0
					if want {
						count = 1
					}
					if got := m.playlistTrackStarred(track); got != want || m.playlistStarCount() != count {
						t.Errorf("starred = %v with %d counted, want %v with %d", got, m.playlistStarCount(), want, count)
					}
				})
			}
		}
	}
}

// Without the library the queue's star is cliamp's, unchanged: a queue a
// playlist loaded stars its bookmarks alone, a starred station included.
func TestQueueStarWithoutTheLibraryIsCliamps(t *testing.T) {
	lib, fx := newFavModel(t)
	lib = libEnter(t, lib, "Mixed")
	lib = libEnter(t, lib, "Radio Uno [Chile · MP3 128k]")
	if _, err := fx.favs.Toggle(fx.uno); err != nil {
		t.Fatal(err)
	}
	m := Model{player: &playbackFakeEngine{}, playlist: lib.playlist, plVisible: 10, loadedPlaylist: "My playlist"}
	m.SetRadioFavorites(fx.favs)
	m.playlist.ToggleBookmark(4) // the file
	uno, _ := m.playlist.Track(0)
	file, _ := m.playlist.Track(4)
	if m.playlistTrackStarred(uno) || !m.playlistTrackStarred(file) || m.playlistStarCount() != 1 {
		t.Errorf("loaded queue: station starred %v, bookmark starred %v, %d counted; want false, true, 1",
			m.playlistTrackStarred(uno), m.playlistTrackStarred(file), m.playlistStarCount())
	}
	m.loadedPlaylist = ""
	if !m.playlistTrackStarred(uno) {
		t.Error("cliamp's own queue does not star the favorite station")
	}
}
