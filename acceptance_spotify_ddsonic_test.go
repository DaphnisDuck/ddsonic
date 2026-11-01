package main

// ddsonic: acceptance cases for Spotify's synced library, in-process. The
// real sync source (catalogsync/spotifysrc), engine, SQLite catalog and
// library levels run over a Spotify client that is a fake the test can
// change, fail, and block: the account, with no account.
//
// What this does not cover: the Spotify web client, the session and token
// refresh. Those are live acceptance (docs/ddsonic/acceptance/live.md).
// The cases are named by docs/ddsonic/acceptance/cases.yaml.

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/catalogsync/spotifysrc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/ui/model"
)

// spotifyAccount is a Spotify account the test edits between syncs.
type spotifyAccount struct {
	mu      sync.Mutex
	albums  []catalog.AlbumRecord // newest first, as Spotify lists them
	artists []catalog.ArtistRecord
	liked   []catalog.TrackRecord
	// failAlbums fails the saved-albums read; unavailable fails every read.
	failAlbums  error
	unavailable error
	// blockAlbums, when set, holds the saved-albums read until the sync is
	// cancelled; entered is closed when the read begins.
	blockAlbums bool
	entered     chan struct{}
	calls       []string
}

var _ spotifysrc.Client = (*spotifyAccount)(nil)

func spotifyRef(id string) catalog.Ref {
	return catalog.Ref{Provider: catalog.Spotify, ProviderID: id}
}

// savedAlbum is an album saved at the given minute: later is newer.
func savedAlbum(id, title string, minute int) catalog.AlbumRecord {
	return catalog.AlbumRecord{
		Ref: spotifyRef(id), Title: title,
		Artists: []catalog.ArtistRecord{{Ref: spotifyRef("artist-of-" + id), Name: "Artist of " + title}},
		AddedAt: time.Date(2026, 1, 1, 0, minute, 0, 0, time.UTC),
	}
}

func (a *spotifyAccount) edit(change func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	change()
}

// call records a read and reports whether the account is reachable.
func (a *spotifyAccount) call(name string) error {
	a.calls = append(a.calls, name)
	return a.unavailable
}

func (a *spotifyAccount) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *spotifyAccount) SavedAlbumRecords(ctx context.Context) ([]catalog.AlbumRecord, error) {
	a.mu.Lock()
	if err := a.call("albums"); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	block, entered, fail := a.blockAlbums, a.entered, a.failAlbums
	albums := slices.Clone(a.albums)
	a.mu.Unlock()
	if block {
		close(entered)
		<-ctx.Done()
		// A client that ignored the cancellation and answered anyway: the
		// engine must still commit nothing.
		return albums, nil
	}
	if fail != nil {
		return nil, fail
	}
	return albums, nil
}

func (a *spotifyAccount) SavedAlbumsHead(context.Context) (catalog.CollectionHead, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.call("albums head"); err != nil {
		return catalog.CollectionHead{}, err
	}
	if a.failAlbums != nil {
		return catalog.CollectionHead{}, a.failAlbums
	}
	h := catalog.CollectionHead{Total: len(a.albums)}
	if len(a.albums) > 0 {
		h.NewestID, h.NewestAt = a.albums[0].Ref.ProviderID, a.albums[0].AddedAt
	}
	return h, nil
}

func (a *spotifyAccount) FollowedArtistRecords(context.Context) ([]catalog.ArtistRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.artists), a.call("artists")
}

func (a *spotifyAccount) LikedTrackRecords(context.Context) ([]catalog.TrackRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.liked), a.call("liked")
}

func (a *spotifyAccount) LikedTracksHead(context.Context) (catalog.CollectionHead, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := catalog.CollectionHead{Total: len(a.liked)}
	if len(a.liked) > 0 {
		h.NewestID, h.NewestAt = a.liked[0].Ref.ProviderID, a.liked[0].AddedAt
	}
	return h, a.call("liked head")
}

func (a *spotifyAccount) PlaylistRecords(context.Context) ([]catalog.PlaylistRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.call("playlists")
}

func (a *spotifyAccount) PlaylistTrackRecords(context.Context, string) ([]catalog.TrackRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.call("playlist tracks")
}

func (a *spotifyAccount) AlbumTrackRecords(context.Context, string) ([]catalog.TrackRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.call("album tracks")
}

func (a *spotifyAccount) AlbumTrackRecordsOnce(context.Context, string) ([]catalog.TrackRecord, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.call("album tracks")
}

// spotifyRun is the catalog runtime over an account and a catalog file, as
// openCatalog wires it: the real source, engine, filler and store.
type spotifyRun struct {
	t        *testing.T
	rt       *catalogRuntime
	finished chan model.CatalogSyncMsg
}

// startSpotify opens the catalog at dbPath over account. The refresh window
// is long and no retry timer fires within a test, so a sync runs only when
// the test asks for one, or at startup when the stored state calls for it.
func startSpotify(t *testing.T, dbPath string, account *spotifyAccount) *spotifyRun {
	t.Helper()
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	r := &spotifyRun{t: t, finished: make(chan model.CatalogSyncMsg, 16)}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}, retryMin: time.Hour, retryMax: time.Hour}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	src := spotifysrc.New(account)
	rt.filler = catalogsync.NewFiller(store, src, catalogsync.DefaultPacing)
	rt.setSources(source{Source: src, refresh: 2 * time.Hour, worker: rt.filler})
	rt.send = func(msg tea.Msg) {
		if m, ok := msg.(model.CatalogSyncMsg); ok && m.Phase == model.CatalogSyncFinished {
			r.finished <- m
		}
	}
	r.rt = rt
	return r
}

// refresh syncs Spotify, as "r" in the library does, and returns the
// message the library is sent when the sync ends.
func (r *spotifyRun) refresh() model.CatalogSyncMsg {
	r.t.Helper()
	r.rt.refresh(catalog.Spotify)
	select {
	case msg := <-r.finished:
		return msg
	case <-time.After(10 * time.Second):
		r.t.Fatal("the sync did not finish")
		return model.CatalogSyncMsg{}
	}
}

// albums is what the catalog holds as Spotify's saved albums: title to ID.
func (r *spotifyRun) albums() map[string]int64 {
	r.t.Helper()
	albums, err := r.rt.store.Albums(context.Background(), catalog.Spotify, catalog.ByTitle)
	if err != nil {
		r.t.Fatal(err)
	}
	ids := map[string]int64{}
	for _, a := range albums {
		if _, twice := ids[a.Title]; twice {
			r.t.Fatalf("the catalog holds the album %q twice", a.Title)
		}
		ids[a.Title] = a.ID
	}
	return ids
}

// listed is the Albums list the library shows for Spotify: the real menu
// and level, loaded from the catalog.
func (r *spotifyRun) listed() []string {
	r.t.Helper()
	src := librarySources([]model.ProviderEntry{{Key: "spotify", Provider: fakePlayer{"Spotify"}}}, "", r.rt)
	if len(src.Synced) != 1 {
		r.t.Fatalf("the library offers %d synced sources, want Spotify", len(src.Synced))
	}
	menu, err := library.SyncedMenu(src.Catalog, src.Synced[0]).Load(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	for _, entry := range menu {
		if entry.Title != "Albums" {
			continue
		}
		rows, err := entry.Open.Load(context.Background())
		if err != nil {
			r.t.Fatal(err)
		}
		titles := make([]string, len(rows))
		for i, row := range rows {
			titles[i] = row.Title
		}
		slices.Sort(titles)
		return titles
	}
	r.t.Fatalf("Spotify's menu has no Albums list: %+v", menu)
	return nil
}

func (r *spotifyRun) likedTitles() []string {
	r.t.Helper()
	tracks, err := r.rt.store.LikedTracks(context.Background(), catalog.Spotify)
	if err != nil {
		r.t.Fatal(err)
	}
	titles := make([]string, len(tracks))
	for i, tr := range tracks {
		titles[i] = tr.Title
	}
	slices.Sort(titles)
	return titles
}

// albumsError is the error stored for the saved-albums collection.
func (r *spotifyRun) albumsError() string {
	r.t.Helper()
	status, err := r.rt.store.SyncStatus(context.Background(), catalog.Spotify)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, c := range status {
		if c.Collection == spotifysrc.Albums {
			return c.LastError
		}
	}
	return ""
}

func wantAlbums(t *testing.T, when string, r *spotifyRun, want ...string) map[string]int64 {
	t.Helper()
	got := r.albums()
	titles := make([]string, 0, len(got))
	for title := range got {
		titles = append(titles, title)
	}
	slices.Sort(titles)
	slices.Sort(want)
	if !slices.Equal(titles, want) {
		t.Fatalf("%s: the catalog's saved albums are %q, want %q", when, titles, want)
	}
	if listed := r.listed(); !slices.Equal(listed, want) {
		t.Fatalf("%s: the library's Albums list is %q, want %q", when, listed, want)
	}
	return got
}

func sameIdentity(t *testing.T, when string, got, was map[string]int64, titles ...string) {
	t.Helper()
	for _, title := range titles {
		if got[title] != was[title] {
			t.Errorf("%s: %q is catalog album %d, was %d: a surviving album must keep its identity", when, title, got[title], was[title])
		}
	}
}

func likedTrack(id, title string) catalog.TrackRecord {
	return catalog.TrackRecord{Ref: spotifyRef(id), Title: title, PlayableURI: "spotify:track:" + id}
}

// Spotify's Albums are the account's saved albums: saving one shows it
// after a refresh and not before, a second refresh changes nothing, a
// restart shows the cached list before the account is read again, and
// unsaving removes it, with the other albums and collections untouched.
func TestAcceptanceSpotifySavedAlbumTransition(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "library.db")
	account := &spotifyAccount{
		albums: []catalog.AlbumRecord{savedAlbum("al-b", "Beta Album", 2), savedAlbum("al-a", "Alpha Album", 1)},
		liked:  []catalog.TrackRecord{likedTrack("tr-1", "Liked One")},
	}
	run := startSpotify(t, dbPath, account)

	// Quiescent baseline.
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("baseline sync: %v", msg.Err)
	}
	baseline := wantAlbums(t, "baseline", run, "Alpha Album", "Beta Album")

	// Mutate the account: nothing changes until a refresh.
	account.edit(func() { account.albums = slices.Insert(account.albums, 0, savedAlbum("al-c", "Gamma Album", 3)) })
	reads := account.callCount()
	wantAlbums(t, "after saving, before a refresh", run, "Alpha Album", "Beta Album")
	if account.callCount() != reads {
		t.Fatal("the account was read without a refresh")
	}

	// Refresh: the saved album appears; the others are the same entities.
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("refresh after saving: %v", msg.Err)
	}
	afterSave := wantAlbums(t, "after the refresh", run, "Alpha Album", "Beta Album", "Gamma Album")
	sameIdentity(t, "after the refresh", afterSave, baseline, "Alpha Album", "Beta Album")

	// Idempotence: a second refresh with nothing changed.
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("second refresh: %v", msg.Err)
	}
	again := wantAlbums(t, "after a second refresh", run, "Alpha Album", "Beta Album", "Gamma Album")
	sameIdentity(t, "after a second refresh", again, afterSave, "Alpha Album", "Beta Album", "Gamma Album")

	// Restart with the account unreachable: the cached list is shown, and
	// the account is not read at all.
	run.rt.close()
	gone := &spotifyAccount{unavailable: errors.New("spotify is unreachable")}
	restarted := startSpotify(t, dbPath, gone)
	if ps := restarted.rt.providers[catalog.Spotify]; ps.stale {
		t.Fatal("a library synced moments ago is due for a sync at startup")
	}
	persisted := wantAlbums(t, "after a restart, before any read", restarted, "Alpha Album", "Beta Album", "Gamma Album")
	sameIdentity(t, "after a restart", persisted, afterSave, "Alpha Album", "Beta Album", "Gamma Album")
	if n := gone.callCount(); n != 0 {
		t.Fatalf("the account was read %d times before the cached library was shown: %q", n, gone.calls)
	}
	restarted.rt.close()

	// Reverse the mutation, on a third start with the account back.
	account.edit(func() { account.albums = slices.Delete(account.albums, 0, 1) })
	final := startSpotify(t, dbPath, account)
	t.Cleanup(final.rt.close)
	if msg := final.refresh(); msg.Err != nil {
		t.Fatalf("refresh after unsaving: %v", msg.Err)
	}
	end := wantAlbums(t, "after unsaving and a refresh", final, "Alpha Album", "Beta Album")
	sameIdentity(t, "after unsaving", end, baseline, "Alpha Album", "Beta Album")
	// Isolation: the liked tracks were never part of this.
	if liked := final.likedTitles(); !slices.Equal(liked, []string{"Liked One"}) {
		t.Errorf("liked tracks are %q after the album changes, want them untouched", liked)
	}
}

// A failed saved-albums read keeps the cached albums, stores and reports
// the failure, and leaves the collections that did sync updated; the next
// refresh succeeds with no restart.
func TestAcceptanceSpotifyFailedSyncKeepsCacheAndRecovers(t *testing.T) {
	account := &spotifyAccount{
		albums: []catalog.AlbumRecord{savedAlbum("al-b", "Beta Album", 2), savedAlbum("al-a", "Alpha Album", 1)},
		liked:  []catalog.TrackRecord{likedTrack("tr-1", "Liked One")},
	}
	run := startSpotify(t, filepath.Join(t.TempDir(), "library.db"), account)
	t.Cleanup(run.rt.close)
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("baseline sync: %v", msg.Err)
	}
	baseline := wantAlbums(t, "baseline", run, "Alpha Album", "Beta Album")

	// The albums read fails while the account has changed in two places.
	down := errors.New("spotify: saved albums: 503 service unavailable")
	account.edit(func() {
		account.albums = slices.Insert(account.albums, 0, savedAlbum("al-c", "Gamma Album", 3))
		account.liked = slices.Insert(account.liked, 0, likedTrack("tr-2", "Liked Two"))
		account.failAlbums = down
	})
	msg := run.refresh()
	if msg.Err == nil {
		t.Fatal("the library was told the sync succeeded while the albums read failed")
	}
	kept := wantAlbums(t, "after the failed sync", run, "Alpha Album", "Beta Album")
	sameIdentity(t, "after the failed sync", kept, baseline, "Alpha Album", "Beta Album")
	if stored := run.albumsError(); stored == "" {
		t.Error("no error is stored for the saved albums after the failed read")
	}
	if st, _ := run.rt.status(catalog.Spotify, len(spotifysrc.New(account).Collections())); st.LastError == "" {
		t.Error("the status the library reads at startup carries no error after the failed sync")
	}
	// Collections commit one by one: the liked tracks did sync.
	if liked := run.likedTitles(); !slices.Equal(liked, []string{"Liked One", "Liked Two"}) {
		t.Errorf("liked tracks are %q: a collection that synced must not be held back by one that failed", liked)
	}

	// The account is back. Same run, no restart.
	account.edit(func() { account.failAlbums = nil })
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("the refresh after the outage failed: %v", msg.Err)
	}
	recovered := wantAlbums(t, "after recovery", run, "Alpha Album", "Beta Album", "Gamma Album")
	sameIdentity(t, "after recovery", recovered, baseline, "Alpha Album", "Beta Album")
	if stored := run.albumsError(); stored != "" {
		t.Errorf("the saved albums still carry the error %q after a successful sync", stored)
	}
}

// A sync cancelled while a collection is being read commits nothing of it,
// even when the read returns its answer after the cancellation.
func TestAcceptanceSpotifyCancelledSyncCommitsNothing(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "library.db")
	account := &spotifyAccount{albums: []catalog.AlbumRecord{savedAlbum("al-a", "Alpha Album", 1)}}
	run := startSpotify(t, dbPath, account)
	if msg := run.refresh(); msg.Err != nil {
		t.Fatalf("baseline sync: %v", msg.Err)
	}
	baseline := wantAlbums(t, "baseline", run, "Alpha Album")

	account.edit(func() {
		account.albums = slices.Insert(account.albums, 0, savedAlbum("al-c", "Gamma Album", 3))
		account.blockAlbums, account.entered = true, make(chan struct{})
	})
	run.rt.refresh(catalog.Spotify)
	select {
	case <-account.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the sync never read the saved albums")
	}
	run.rt.close() // quitting mid-sync: cancels, waits, closes the catalog

	account.edit(func() { account.blockAlbums = false })
	reopened := startSpotify(t, dbPath, &spotifyAccount{unavailable: errors.New("not read")})
	t.Cleanup(reopened.rt.close)
	after := wantAlbums(t, "after the cancelled sync", reopened, "Alpha Album")
	sameIdentity(t, "after the cancelled sync", after, baseline, "Alpha Album")
}
