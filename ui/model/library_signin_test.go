package model

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// signInProv is a Spotify stand-in: a player that signs in, searches and
// lists playlists only when signed in, and counts everything asked of it.
type signInProv struct {
	name           string
	authed         bool
	authErr        error
	auths          int
	searches       int
	listings       int
	adds           int
	searchUnsigned error // what a search fails with when not signed in
}

func (p *signInProv) Name() string         { return p.name }
func (p *signInProv) URISchemes() []string { return []string{"spotify:"} }
func (p *signInProv) Playlists() ([]playlist.PlaylistInfo, error) {
	p.listings++
	if !p.authed {
		return nil, fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)
	}
	return []playlist.PlaylistInfo{{ID: "pl", Name: "Mine"}}, nil
}
func (p *signInProv) Tracks(string) ([]playlist.Track, error) { return nil, nil }
func (p *signInProv) Authenticate() error {
	p.auths++
	if p.authErr != nil {
		return p.authErr
	}
	p.authed = true
	return nil
}
func (p *signInProv) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	p.searches++
	if !p.authed {
		if p.searchUnsigned != nil {
			return nil, p.searchUnsigned
		}
		return nil, fmt.Errorf("spotify: search: %w", playlist.ErrNeedsAuth)
	}
	return []playlist.Track{{Path: "spotify:track:1", Title: "Found"}}, nil
}
func (p *signInProv) AddTrackToPlaylist(context.Context, string, playlist.Track) error {
	p.adds++
	if !p.authed {
		return fmt.Errorf("spotify: add to playlist: %w", playlist.ErrNeedsAuth)
	}
	return nil
}

// signInLib is the library as main builds it for Spotify: the real synced
// menu over a real catalog file, with the sign-in registered and "r" counted.
type signInLib struct {
	t         *testing.T
	store     *sqlite.Store
	prov      *signInProv
	refreshes []string
}

func newSignInLib(t *testing.T) *signInLib {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &signInLib{t: t, store: store, prov: &signInProv{name: "Spotify"}}
}

// model is a fresh model over the catalog, as after a start: the stored
// sync status, and no sync run yet.
func (l *signInLib) model() Model {
	l.t.Helper()
	src := library.SyncedSource{Provider: catalog.Spotify, Title: "Spotify", Player: l.prov,
		Collections: []string{catalog.CollectionAlbums, catalog.CollectionArtists, catalog.CollectionPlaylists, catalog.CollectionLiked}}
	m := newLibraryModel(library.Menu("Music",
		library.Entry{Title: "Spotify", Open: library.SyncedMenu(l.store, src), Source: catalog.Spotify}))
	m = sized(m, 100, 30)
	m.SetLibrarySignIn(catalog.Spotify, l.prov)
	stored, err := l.store.SyncStatus(context.Background(), catalog.Spotify)
	if err != nil {
		l.t.Fatal(err)
	}
	st := CatalogStatus{}
	for i, c := range stored {
		if i == 0 || c.LastSuccess.Before(st.LastSuccess) {
			st.LastSuccess = c.LastSuccess
		}
		if c.LastError != "" {
			st.LastError = c.LastError
		}
	}
	m.SetCatalogSync(map[string]CatalogStatus{catalog.Spotify: st}, func(p string) { l.refreshes = append(l.refreshes, p) })
	return m
}

// saveAlbum puts a saved album in the catalog, as a sync of Albums would.
func (l *signInLib) saveAlbum(title string) {
	l.t.Helper()
	ref := func(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Spotify, ProviderID: id} }
	err := l.store.ApplySnapshot(context.Background(), catalog.Snapshot{
		Provider: catalog.Spotify, Collection: catalog.CollectionAlbums,
		Albums: []catalog.AlbumRecord{{Ref: ref("al-" + title), Title: title,
			Artists: []catalog.ArtistRecord{{Ref: ref("ar-" + title), Name: "Artist"}}, AddedAt: time.Now()}},
	})
	if err != nil {
		l.t.Fatal(err)
	}
}

// synced records every collection as synced, with whatever it holds.
func (l *signInLib) synced() {
	l.t.Helper()
	for _, c := range []string{catalog.CollectionAlbums, catalog.CollectionArtists, catalog.CollectionPlaylists, catalog.CollectionLiked} {
		if err := l.store.RecordSyncSuccess(context.Background(), catalog.Spotify, c); err != nil {
			l.t.Fatal(err)
		}
	}
}

func sized(m Model, w, h int) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(Model)
}

var errSignInNeeded = fmt.Errorf("sync spotify/albums: %w", playlist.ErrNeedsAuth)

// sendAll delivers msg and then the message of every command that results,
// except the ones the test holds back.
func sendAll(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	updated, cmd := m.Update(msg)
	return runAll(t, updated.(Model), cmd)
}

func runAll(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	switch msg := cmd().(type) {
	case nil:
		return m
	case tea.BatchMsg:
		for _, c := range msg {
			m = runAll(t, m, c)
		}
		return m
	case libraryLoadedMsg, libraryAuthDoneMsg, spotSearchResultsMsg, spotPlaylistsMsg, spotAddedMsg:
		return sendAll(t, m, msg)
	}
	return m
}

func syncEnds(t *testing.T, m Model, err error) Model {
	t.Helper()
	return sendAll(t, m, CatalogSyncMsg{Provider: catalog.Spotify, Phase: CatalogSyncFinished, Err: err})
}

func pressAll(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		m = sendAll(t, m, libKey(k))
	}
	return m
}

// openRow moves the cursor to the row titled title and opens it.
func openRow(t *testing.T, m Model, title string) Model {
	t.Helper()
	for range len(m.libTop().entries) {
		if f := m.libTop(); f.entries[f.cursor].Title == title {
			return pressAll(t, m, "enter")
		}
		m = pressAll(t, m, "j")
	}
	t.Fatalf("no row %q on screen:\n%s", title, viewText(m))
	return m
}

func wantView(t *testing.T, when string, m Model, want []string, not ...string) {
	t.Helper()
	text := viewText(m)
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s: the screen lacks %q:\n%s", when, w, text)
		}
	}
	for _, n := range not {
		if strings.Contains(text, n) {
			t.Errorf("%s: the screen shows %q:\n%s", when, n, text)
		}
	}
}

const (
	signInRow    = "Sign in to Spotify"
	signInPrompt = "Sign-in required. Press Enter to sign in."
	signingIn    = "Signing in — finish in your browser…"
	syncingRow   = "Syncing your library…"
	failedRow    = "Couldn't sync Spotify. Press r to retry."
)

// The regression: Spotify configured, never signed in, nothing cached. The
// real synced menu must offer the sign-in, and every list must ask for it,
// with no row of the catalog to open first.
func TestSyncedMenuOffersSignInOnEmptyCatalog(t *testing.T) {
	l := newSignInLib(t)
	m := syncEnds(t, l.model(), errSignInNeeded)

	m = pressAll(t, m, "enter") // Music → Spotify
	wantView(t, "Spotify's menu", m, []string{"Spotify sign-in required", signInRow, "Albums", "Liked Songs"}, "cached", "sync failed")
	if e := m.libTop().entries[0]; e.Intent != library.IntentSignIn || m.libTop().cursor != 0 {
		t.Errorf("the menu's first row is %+v with the cursor on %d, want the sign-in row under the cursor", e, m.libTop().cursor)
	}
	for _, list := range []string{"Albums", "Artists", "Playlists", "Liked Songs"} {
		lm := openRow(t, m, list)
		wantView(t, list, lm, []string{signInPrompt, "Spotify sign-in required"}, syncingRow, "press r to retry")
	}
	if l.prov.auths != 0 {
		t.Errorf("signed in %d times without being asked", l.prov.auths)
	}
}

// First run, end to end in the library: the prompt, an explicit Enter, the
// sign-in, an immediate sync of Spotify, the lists filling when it ends, and
// a restart that asks for nothing.
func TestFirstRunSignInSyncsAndRestartsSignedIn(t *testing.T) {
	l := newSignInLib(t)
	m := syncEnds(t, l.model(), errSignInNeeded)
	m = pressAll(t, m, "enter", "j", "enter") // Spotify → Albums
	wantView(t, "before signing in", m, []string{signInPrompt})

	m = pressAll(t, m, "enter")
	if l.prov.auths != 1 {
		t.Fatalf("Enter on the prompt signed in %d times, want 1", l.prov.auths)
	}
	if len(l.refreshes) != 1 || l.refreshes[0] != catalog.Spotify {
		t.Fatalf("syncs requested after the sign-in = %q, want one of Spotify at once", l.refreshes)
	}
	m = sendAll(t, m, CatalogSyncMsg{Provider: catalog.Spotify, Phase: CatalogSyncStarted})
	wantView(t, "while the first sync runs", m, []string{"↻ syncing", syncingRow}, signInPrompt, "sign-in required", "press r")

	l.saveAlbum("Kind of Blue")
	l.synced()
	m = syncEnds(t, m, nil)
	wantView(t, "after the sync", m, []string{"Kind of Blue", "✓ synced"}, signInPrompt, syncingRow, "sign-in required")
	m = pressAll(t, m, "h")
	wantView(t, "Spotify's menu after the sync", m, []string{"Albums"}, signInRow)
	if m.libTop().entries[m.libTop().cursor].Title != "Albums" {
		t.Errorf("the cursor left Albums when the sign-in row went: on %q", m.libTop().entries[m.libTop().cursor].Title)
	}

	// Restart: a new model over the same catalog.
	r := pressAll(t, l.model(), "enter")
	wantView(t, "after a restart", r, []string{"Albums", "✓ synced"}, signInRow, "sign-in required")
	r = pressAll(t, r, "enter")
	wantView(t, "Albums after a restart", r, []string{"Kind of Blue"}, signInPrompt)
	if l.prov.auths != 1 {
		t.Errorf("signed in %d times in all, want 1", l.prov.auths)
	}
}

// The menu's own row signs in too, and shows the browser prompt while the
// sign-in runs.
func TestSignInRowSignsIn(t *testing.T) {
	l := newSignInLib(t)
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter")

	updated, cmd := m.Update(libKey("enter")) // the sign-in, held
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter on the sign-in row did nothing")
	}
	wantView(t, "while signing in", m, []string{signingIn}, signInRow)
	m = sendAll(t, m, ProvAuthURLMsg{ProviderName: "Spotify", URL: "https://accounts.example/authorize"})
	wantView(t, "with the URL", m, []string{"If no browser opened, visit:", "https://accounts.example/authorize"})

	m = runAll(t, m, cmd)
	if l.prov.auths != 1 || len(l.refreshes) != 1 {
		t.Errorf("sign-ins = %d, syncs requested = %d, want 1 and 1", l.prov.auths, len(l.refreshes))
	}
	wantView(t, "after signing in", m, []string{"Albums"}, signInRow, signingIn, "accounts.example")
}

// After a reset, or once Spotify has revoked the sign-in, the library still
// browses what it cached, says so, and offers the sign-in.
func TestSignInRequiredWithPopulatedCache(t *testing.T) {
	l := newSignInLib(t)
	l.saveAlbum("Kind of Blue")
	l.synced()
	m := syncEnds(t, l.model(), errSignInNeeded)

	m = pressAll(t, m, "enter")
	wantView(t, "Spotify's menu", m, []string{"Spotify sign-in required · cached", signInRow})
	m = pressAll(t, m, "j", "enter")
	wantView(t, "Albums", m, []string{"Kind of Blue", "Spotify sign-in required · cached"}, signInPrompt)

	m = pressAll(t, m, "h", "k", "enter") // back to the sign-in row
	if l.prov.auths != 1 || len(l.refreshes) != 1 {
		t.Errorf("sign-ins = %d, syncs requested = %d, want 1 and 1", l.prov.auths, len(l.refreshes))
	}
	m = syncEnds(t, m, nil)
	wantView(t, "after the sync", m, []string{"Albums", "✓ synced"}, signInRow, "sign-in required")
}

// Each state of an empty Spotify list says what it is: an ordinary failure
// is not a sign-in and not a sync still running, a running sync is not a
// failure, and an account with nothing saved is just empty.
func TestSpotifyListStates(t *testing.T) {
	outage := errors.New("sync spotify/albums: Get \"https://api.spotify.com/v1/me/albums\": dial tcp: no route to host")
	rateLimited := fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: time.Hour})
	timeout := fmt.Errorf("sync spotify/albums: %w", context.DeadlineExceeded)

	t.Run("ordinary failure, nothing cached", func(t *testing.T) {
		for _, err := range []error{outage, rateLimited, timeout} {
			l := newSignInLib(t)
			m := pressAll(t, syncEnds(t, l.model(), err), "enter")
			wantView(t, "Spotify's menu", m, []string{"sync failed"}, signInRow, "sign-in required", "cached")
			m = pressAll(t, m, "enter")
			wantView(t, "Albums", m, []string{failedRow, "sync failed"}, signInPrompt, syncingRow, "cached")
			if m = pressAll(t, m, "enter"); l.prov.auths != 0 {
				t.Errorf("Enter on a failed list signed in after %v", err)
			}
		}
	})
	t.Run("syncing", func(t *testing.T) {
		l := newSignInLib(t)
		m := sendAll(t, l.model(), CatalogSyncMsg{Provider: catalog.Spotify, Phase: CatalogSyncStarted})
		m = pressAll(t, m, "enter", "enter")
		wantView(t, "Albums", m, []string{"↻ syncing", syncingRow}, signInPrompt, failedRow, "press r")
	})
	t.Run("empty and healthy", func(t *testing.T) {
		l := newSignInLib(t)
		l.synced()
		m := pressAll(t, syncEnds(t, l.model(), nil), "enter", "enter")
		wantView(t, "Albums", m, []string{"Nothing here.", "✓ synced"}, signInPrompt, failedRow, syncingRow)
	})
	t.Run("cached and offline", func(t *testing.T) {
		l := newSignInLib(t)
		l.saveAlbum("Kind of Blue")
		l.synced()
		m := pressAll(t, syncEnds(t, l.model(), outage), "enter")
		wantView(t, "Spotify's menu", m, []string{"sync failed · cached"}, signInRow, "sign-in required")
		m = pressAll(t, m, "enter")
		wantView(t, "Albums", m, []string{"Kind of Blue", "sync failed · cached"}, failedRow, signInPrompt)
	})
}

// A second Enter while the browser is open starts nothing.
func TestDuplicateSignInIsIgnored(t *testing.T) {
	l := newSignInLib(t)
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter")

	updated, first := m.Update(libKey("enter"))
	m = updated.(Model)
	updated, second := m.Update(libKey("enter"))
	m = updated.(Model)
	if first == nil || second != nil {
		t.Fatalf("first Enter started a sign-in: %v; second started another: %v", first != nil, second != nil)
	}
	m = runAll(t, m, first)
	if l.prov.auths != 1 || len(l.refreshes) != 1 {
		t.Errorf("sign-ins = %d, syncs requested = %d, want 1 and 1", l.prov.auths, len(l.refreshes))
	}
}

// Leaving the screen while the browser is open neither cancels the sign-in
// nor loses its result, and the rest of the library stays usable.
func TestSignInSurvivesNavigation(t *testing.T) {
	l := newSignInLib(t)
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter")
	updated, cmd := m.Update(libKey("enter"))
	m = updated.(Model)

	m = pressAll(t, m, "h") // back to Music
	wantView(t, "Music while signing in", m, []string{"Spotify"}, signingIn)
	if len(m.lib.stack) != 1 {
		t.Fatalf("h did not leave Spotify's menu: %d levels", len(m.lib.stack))
	}

	m = runAll(t, m, cmd) // the sign-in ends while Music is on screen
	if len(l.refreshes) != 1 {
		t.Errorf("syncs requested = %d after a sign-in that ended elsewhere, want 1", len(l.refreshes))
	}
	m = pressAll(t, m, "enter")
	wantView(t, "Spotify's menu afterwards", m, []string{"Albums"}, signInRow, signingIn)
}

// A sign-in's result is dropped when a newer request has replaced it.
func TestStaleSignInCompletionIgnored(t *testing.T) {
	l := newSignInLib(t)
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter")
	updated, _ := m.Update(libKey("enter"))
	m = updated.(Model)

	m = sendAll(t, m, libraryAuthDoneMsg{gen: m.lib.authGen - 1, provider: catalog.Spotify})
	if len(l.refreshes) != 0 {
		t.Error("a stale sign-in result started a sync")
	}
	wantView(t, "after a stale result", m, []string{signingIn})
	if !m.libSyncState(catalog.Spotify).needsAuth {
		t.Error("a stale sign-in result cleared sign-in required")
	}
}

// A sign-in that fails keeps the prompt, says so, and syncs nothing.
func TestFailedSignInKeepsPrompt(t *testing.T) {
	l := newSignInLib(t)
	l.prov.authErr = errors.New("oauth: access_denied")
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter", "enter")

	if l.prov.auths != 1 || len(l.refreshes) != 0 {
		t.Errorf("sign-ins = %d, syncs requested = %d, want 1 and 0", l.prov.auths, len(l.refreshes))
	}
	wantView(t, "after a failed sign-in", m, []string{signInRow, "Spotify sign-in required", "access_denied"}, signingIn)

	l.prov.authErr = nil
	m = pressAll(t, m, "enter")
	if l.prov.auths != 2 || len(l.refreshes) != 1 {
		t.Errorf("after trying again: sign-ins = %d, syncs requested = %d, want 2 and 1", l.prov.auths, len(l.refreshes))
	}
}

// Sign-in belongs to the source that asked: another synced source that
// cannot sign in from the library (YouTube Music) gets no row, keeps its own
// wording, and is untouched by Spotify's sign-in.
func TestSignInTargetsItsProvider(t *testing.T) {
	l := newSignInLib(t)
	yt := library.SyncedSource{Provider: catalog.YouTube, Title: "YouTube Music", Player: &signInProv{name: "YouTube Music"},
		Collections: []string{catalog.CollectionPlaylists, catalog.CollectionLiked}, LikedTitle: "Liked Music"}
	sp := library.SyncedSource{Provider: catalog.Spotify, Title: "Spotify", Player: l.prov,
		Collections: []string{catalog.CollectionAlbums, catalog.CollectionLiked}}
	m := newLibraryModel(library.Menu("Music",
		library.Entry{Title: "Spotify", Open: library.SyncedMenu(l.store, sp), Source: catalog.Spotify},
		library.Entry{Title: "YouTube Music", Open: library.SyncedMenu(l.store, yt), Source: catalog.YouTube}))
	m = sized(m, 120, 30)
	m.SetLibrarySignIn(catalog.Spotify, l.prov)
	m.SetCatalogSync(map[string]CatalogStatus{catalog.Spotify: {}, catalog.YouTube: {}},
		func(p string) { l.refreshes = append(l.refreshes, p) })
	m = syncEnds(t, m, errSignInNeeded)
	m = sendAll(t, m, CatalogSyncMsg{Provider: catalog.YouTube, Phase: CatalogSyncFinished,
		Err: fmt.Errorf("sync youtube/liked: %w", playlist.ErrNeedsAuth)})

	m = pressAll(t, m, "j", "enter")
	wantView(t, "YouTube Music's menu", m, []string{"Playlists", "YouTube sync failed · cached"}, "Sign in to", "YouTube sign-in required")
	m = pressAll(t, m, "enter")
	wantView(t, "a YouTube Music list", m, []string{"Syncing your library… (press r to retry if this persists)"}, signInPrompt)
	if m = pressAll(t, m, "enter"); l.prov.auths != 0 {
		t.Error("Enter on a YouTube Music list signed in to Spotify")
	}

	m = pressAll(t, m, "h", "h", "k", "enter", "enter") // Spotify's sign-in row
	if l.prov.auths != 1 || len(l.refreshes) != 1 || l.refreshes[0] != catalog.Spotify {
		t.Errorf("sign-ins = %d, syncs requested = %q, want Spotify's only", l.prov.auths, l.refreshes)
	}
	if m.libSyncState(catalog.Spotify).needsAuth || m.libSyncState(catalog.YouTube).lastErr == "" {
		t.Error("Spotify's sign-in changed the wrong source's state")
	}
}

// A level beneath the lists that reads Spotify live (an album not cached
// yet) and is refused asks for the sign-in itself. Its Enter runs the same
// sign-in of the source: the browser prompt shows there, Spotify syncs, and
// the level loads.
func TestLiveLevelSignInIsTheSourcesSignIn(t *testing.T) {
	l := newSignInLib(t)
	album := library.TrackLevel("Kind of Blue", l.prov, func(context.Context) ([]playlist.Track, error) {
		if !l.prov.authed {
			return nil, fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)
		}
		return tracksOf("So What"), nil
	})
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Kind of Blue", Open: album, Source: catalog.Spotify}))
	m = sized(m, 100, 30)
	m.SetLibrarySignIn(catalog.Spotify, l.prov)
	m.SetCatalogSync(map[string]CatalogStatus{catalog.Spotify: {}}, func(p string) { l.refreshes = append(l.refreshes, p) })

	m = pressAll(t, m, "enter")
	wantView(t, "the album, not signed in", m, []string{signInPrompt})
	updated, cmd := m.Update(libKey("enter"))
	m = updated.(Model)
	wantView(t, "while signing in", m, []string{signingIn}, signInPrompt)
	m = runAll(t, m, cmd)
	if l.prov.auths != 1 || len(l.refreshes) != 1 || l.refreshes[0] != catalog.Spotify {
		t.Errorf("sign-ins = %d, syncs requested = %q, want 1 and Spotify's", l.prov.auths, l.refreshes)
	}
	wantView(t, "the album after signing in", m, []string{"So What"}, signInPrompt, signingIn)
}

func spotifyTrackPlaying(t *testing.T, m Model) (Model, playlist.Track) {
	t.Helper()
	track := playlist.Track{Path: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Title: "Blue in Green", Stream: true}
	m.playlist.Add(track)
	m.playlist.SetIndex(0)
	m.requests.stream = 7
	return m, track
}

// A track Spotify will not play for the sign-in puts the library in
// sign-in required, where the sign-in can be reached; a track that fails
// for the network does not.
func TestPlaybackAuthFailureOffersSignIn(t *testing.T) {
	l := newSignInLib(t)
	l.saveAlbum("Kind of Blue")
	l.synced()
	m, track := spotifyTrackPlaying(t, l.model())

	n := sendAll(t, m, streamPlayedMsg{path: track.Path, gen: 7, err: errors.New("spotify: new stream: connection reset by peer")})
	if n.libSyncState(catalog.Spotify).needsAuth {
		t.Error("a network failure asked for sign-in")
	}

	m = sendAll(t, m, streamPlayedMsg{path: track.Path, gen: 7,
		err: fmt.Errorf("spotify: stream auth error, silent reconnect failed: %w", playlist.ErrNeedsAuth)})
	wantView(t, "after the refused stream", m, []string{"Spotify sign-in required · cached"})
	m = pressAll(t, m, "enter")
	wantView(t, "Spotify's menu", m, []string{signInRow})
	m = pressAll(t, m, "enter")
	if l.prov.auths != 1 || len(l.refreshes) != 1 {
		t.Errorf("sign-ins = %d, syncs requested = %d, want 1 and 1", l.prov.auths, len(l.refreshes))
	}
}

// Spotify's own search, not signed in: the overlay asks for the sign-in,
// Enter signs in, and the search (a read) runs again by itself. An outage is
// shown as what it is.
func TestProviderSearchAuthFailureOffersSignIn(t *testing.T) {
	l := newSignInLib(t)
	m := l.model()
	m = runAll(t, m, m.openProviderSearchQuery(l.prov, "kind of blue"))
	wantView(t, "search, not signed in", m, []string{signInPrompt})
	if l.prov.searches != 1 || l.prov.auths != 0 {
		t.Fatalf("searches = %d, sign-ins = %d, want 1 and 0", l.prov.searches, l.prov.auths)
	}
	if !m.libSyncState(catalog.Spotify).needsAuth {
		t.Error("the refused search did not mark Spotify sign-in required")
	}

	m = pressAll(t, m, "enter")
	if l.prov.auths != 1 || l.prov.searches != 2 {
		t.Errorf("after Enter: sign-ins = %d, searches = %d, want 1 and 2", l.prov.auths, l.prov.searches)
	}
	wantView(t, "search after signing in", m, []string{"Found"}, signInPrompt)
	if len(l.refreshes) != 1 {
		t.Errorf("syncs requested = %d, want the library synced once too", len(l.refreshes))
	}

	o := newSignInLib(t)
	o.prov.searchUnsigned = errors.New("spotify: search: dial tcp: no route to host")
	om := o.model()
	om = runAll(t, om, om.openProviderSearchQuery(o.prov, "kind of blue"))
	wantView(t, "search during an outage", om, []string{"no route to host"}, signInPrompt)
	if om = pressAll(t, om, "enter"); o.prov.auths != 0 {
		t.Error("Enter after an outage signed in")
	}
}

// The playlist picker, not signed in: the overlay asks for the sign-in, and
// signing in adds nothing and lists nothing by itself. The user asks again.
func TestPlaylistPickerAuthFailureOffersSignInWithoutReplay(t *testing.T) {
	l := newSignInLib(t)
	l.prov.authed = true
	m := l.model()
	m = runAll(t, m, m.openProviderSearchQuery(l.prov, "kind of blue"))
	wantView(t, "results", m, []string{"Found"})

	l.prov.authed = false // Spotify revokes the sign-in
	m = pressAll(t, m, "p")
	wantView(t, "the picker, not signed in", m, []string{signInPrompt})
	if l.prov.listings != 1 {
		t.Fatalf("playlist listings = %d, want 1", l.prov.listings)
	}

	m = pressAll(t, m, "enter")
	if l.prov.auths != 1 {
		t.Fatalf("sign-ins = %d, want 1", l.prov.auths)
	}
	if l.prov.listings != 1 || l.prov.adds != 0 || l.prov.searches != 1 {
		t.Errorf("after signing in: listings = %d, adds = %d, searches = %d; want nothing replayed (1, 0, 1)",
			l.prov.listings, l.prov.adds, l.prov.searches)
	}
	wantView(t, "results after signing in", m, []string{"Found"}, signInPrompt)
	if m.spotSearch.screen != spotSearchResults {
		t.Errorf("the overlay is on screen %v, want the results", m.spotSearch.screen)
	}
}

// Adding to a playlist, the sign-in revoked in between: the add is refused,
// the overlay asks for the sign-in, and signing in adds nothing. The track is
// added when the user asks again, once.
func TestPlaylistAddAuthFailureIsNotReplayed(t *testing.T) {
	l := newSignInLib(t)
	l.prov.authed = true
	m := l.model()
	m = runAll(t, m, m.openProviderSearchQuery(l.prov, "kind of blue"))
	m = pressAll(t, m, "p") // the picker, listing the playlists
	if m.spotSearch.screen != spotSearchPlaylist {
		t.Fatalf("the picker did not open: screen %v\n%s", m.spotSearch.screen, viewText(m))
	}

	l.prov.authed = false // Spotify revokes the sign-in
	for m.spotSearch.cursor < len(m.spotSearch.playlists) && !strings.Contains(viewText(m), "> Mine") {
		m = pressAll(t, m, "j")
	}
	m = pressAll(t, m, "enter") // add: refused
	if l.prov.adds != 1 || m.spotSearch.err != libSignInPrompt {
		t.Fatalf("adds = %d, overlay says %q; want one refused add and the sign-in prompt", l.prov.adds, m.spotSearch.err)
	}
	if !m.libSyncState(catalog.Spotify).needsAuth {
		t.Error("the refused add did not mark Spotify sign-in required")
	}
	wantView(t, "the picker after the refused add", m, []string{signInPrompt})

	m = pressAll(t, m, "enter") // sign in
	if l.prov.auths != 1 || l.prov.adds != 1 {
		t.Fatalf("after signing in: sign-ins = %d, adds = %d; want 1 and still 1", l.prov.auths, l.prov.adds)
	}
	if m.spotSearch.err != "" || !m.spotSearch.visible {
		t.Errorf("after signing in the overlay says %q (visible %v), want it open and clear", m.spotSearch.err, m.spotSearch.visible)
	}
	m = pressAll(t, m, "enter") // the user adds again
	if l.prov.adds != 2 {
		t.Errorf("adds = %d after asking again, want 2", l.prov.adds)
	}
}

// Quitting with the browser open: the sign-in's late result starts nothing.
func TestQuitDuringSignIn(t *testing.T) {
	l := newSignInLib(t)
	m := pressAll(t, syncEnds(t, l.model(), errSignInNeeded), "enter")
	updated, cmd := m.Update(libKey("enter"))
	m = updated.(Model)

	updated, _ = m.Update(libKey("q"))
	m = updated.(Model)
	l.prov.authErr = context.Canceled // what the provider returns once closed
	m = runAll(t, m, cmd)
	if len(l.refreshes) != 0 {
		t.Error("a sign-in that ended after quit started a sync")
	}
}
