package main

// ddsonic: tests for the catalog runtime's sync retries.

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// flakySource fails its first fails syncs, then returns an empty library.
type flakySource struct {
	mu    sync.Mutex
	fails int
	syncs int
	done  chan struct{} // closed on the first successful sync
}

func (*flakySource) Provider() string      { return catalog.Spotify }
func (*flakySource) Collections() []string { return []string{"albums"} }

func (f *flakySource) Fetch(_ context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	if f.syncs <= f.fails {
		return catalog.Snapshot{}, errors.New("login5: 503 no healthy upstream")
	}
	if f.syncs == f.fails+1 {
		close(f.done)
	}
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}, nil
}

func (*flakySource) AlbumTracks(context.Context, catalog.Ref) ([]catalog.TrackRecord, error) {
	return nil, nil
}
func (*flakySource) AlbumTracksOnce(context.Context, catalog.Ref) ([]catalog.TrackRecord, error) {
	return nil, nil
}

func testRuntime(t *testing.T, src *flakySource) *catalogRuntime {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{},
		retryMin: 5 * time.Millisecond, retryMax: 10 * time.Millisecond}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.filler = catalogsync.NewFiller(store, src, catalogsync.DefaultPacing)
	rt.setSources(source{Source: src, refresh: time.Hour, worker: rt.filler})
	t.Cleanup(rt.close)
	return rt
}

func TestFailedSyncRetriesUntilItSucceeds(t *testing.T) {
	src := &flakySource{fails: 2, done: make(chan struct{})}
	rt := testRuntime(t, src)
	rt.refresh(catalog.Spotify)
	select {
	case <-src.done:
	case <-time.After(5 * time.Second):
		t.Fatal("sync never succeeded after failures")
	}
	// Once the success lands, no further retry is pending and the delay resets.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rt.mu.Lock()
		ps := rt.providers[catalog.Spotify]
		settled := ps.retry == nil && ps.retryDelay == 0
		rt.mu.Unlock()
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retry still scheduled after a successful sync")
		}
		time.Sleep(time.Millisecond)
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.syncs != 3 {
		t.Errorf("syncs = %d, want 2 failures and 1 success", src.syncs)
	}
}

var errDown = errors.New("down")

// A sync failed by a rate limit retries when the block ends, not sooner.
func TestRetryWaitsForTheRateLimit(t *testing.T) {
	rt := testRuntime(t, &flakySource{done: make(chan struct{})})
	rt.retryMin, rt.retryMax = time.Minute, 30*time.Minute
	rl := fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: 20 * time.Hour})
	if got := rt.scheduleRetry(catalog.Spotify, errors.Join(errDown, rl)); got != 20*time.Hour {
		t.Errorf("retry in %v, want 20h", got)
	}
	if got := rt.scheduleRetry(catalog.Spotify, &catalog.RateLimitError{RetryAfter: time.Second}); got != 2*time.Minute {
		t.Errorf("short limit: retry in %v, want the 2m backoff", got)
	}
	if got := rt.scheduleRetry(catalog.Spotify, nil); got != 0 {
		t.Errorf("after a success: retry in %v, want none", got)
	}
}

func TestRetryBacksOffAndStopsOnClose(t *testing.T) {
	rt := testRuntime(t, &flakySource{done: make(chan struct{})})
	rt.retryMin, rt.retryMax = time.Hour, 4*time.Hour
	ps := rt.providers[catalog.Spotify]
	var delays []time.Duration
	for range 4 {
		rt.scheduleRetry(catalog.Spotify, errDown)
		delays = append(delays, ps.retryDelay)
	}
	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for i := range want {
		if delays[i] != want[i] {
			t.Fatalf("delays = %v, want %v", delays, want)
		}
	}
	rt.close()
	rt.scheduleRetry(catalog.Spotify, errDown)
	if ps.retry == nil {
		return
	}
	// The timer left from before close was stopped; none is armed after it.
	if ps.retry.Stop() {
		t.Error("a retry is armed after close")
	}
}

// Every configured source gets its status and startup decision: a source
// syncs when older than its refresh, and a zero refresh syncs every time.
func TestSourcesStartupPolicy(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, p := range []string{catalog.Spotify, catalog.Local} {
		if err := store.RecordSyncSuccess(context.Background(), p, "albums"); err != nil {
			t.Fatal(err)
		}
	}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	defer rt.cancel()
	rt.setSources(source{Source: &namedSource{catalog.Spotify}, refresh: time.Hour},
		source{Source: &namedSource{catalog.Local}, lists: []string{catalog.CollectionAlbums}})
	if sp := rt.providers[catalog.Spotify]; sp == nil || sp.stale || sp.startup.LastSuccess.IsZero() {
		t.Errorf("fresh Spotify = %+v, want not stale", sp)
	}
	// A source's menu offers its sync's collections and the lists it adds.
	if got := rt.collections(catalog.Local); !slices.Equal(got, []string{"albums", "albums"}) {
		t.Errorf("local collections = %v, want its sync's and its extra list", got)
	}
	if lp := rt.providers[catalog.Local]; lp == nil || !lp.stale {
		t.Errorf("local = %+v, want indexed at every startup", lp)
	}
	if _, ok := rt.catalog().(catalog.AlbumTrackFetcher); ok {
		t.Error("a catalog without a filler offers album fetching")
	}
}

// A sync that failed after a recent success runs again at the next startup.
func TestSourcesStartupRetriesAFailedSync(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.RecordSyncSuccess(ctx, catalog.Spotify, "albums"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordSyncFailure(ctx, catalog.Spotify, "albums", errors.New("offline")); err != nil {
		t.Fatal(err)
	}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(ctx)
	defer rt.cancel()
	rt.setSources(source{Source: &namedSource{catalog.Spotify}, refresh: time.Hour})
	if sp := rt.providers[catalog.Spotify]; sp == nil || !sp.stale {
		t.Errorf("failed Spotify = %+v, want synced at startup", sp)
	}
}

type namedSource struct{ name string }

func (s *namedSource) Provider() string    { return s.name }
func (*namedSource) Collections() []string { return []string{"albums"} }
func (*namedSource) Fetch(context.Context, string, catalogsync.Known) (catalog.Snapshot, error) {
	return catalog.Snapshot{}, catalogsync.ErrUnchanged
}

// A quiet source (radio) syncs without reporting status to the UI: it says
// once per sync that its rows changed, so lists reading them are reread.
func TestQuietSourceReportsOnlyItsChange(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	defer rt.cancel()
	rt.setSources(source{Source: &namedSource{catalog.Spotify}}, source{Source: &namedSource{catalog.Radio}, quiet: true})
	var sent []tea.Msg
	rt.send = func(m tea.Msg) { sent = append(sent, m) }
	for _, p := range []string{catalog.Radio, catalog.Spotify} {
		if err := rt.engine.Sync(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	changes, status := 0, 0
	for _, m := range sent {
		switch msg := m.(type) {
		case model.CatalogChangedMsg:
			if msg.Provider != catalog.Radio {
				t.Errorf("sent %+v, want only radio's change", m)
			}
			changes++
		case model.CatalogSyncMsg:
			if msg.Provider != catalog.Spotify {
				t.Errorf("sent %+v, want only Spotify's status", m)
			}
			status++
		default:
			t.Errorf("sent %+v", m)
		}
	}
	if changes != 1 {
		t.Errorf("radio's sync reported %d changes, want 1", changes)
	}
	if status == 0 {
		t.Error("Spotify's events were not sent")
	}
}

// A station starred or unstarred through the favorites store, as the
// library's f does it, is in or out of the catalog search finds it in by
// the time the UI is told the catalog changed.
func TestStarredStationReachesSearchAndTheUI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_MUSIC_DIR", filepath.Join(home, "none"))
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(home, "config"))
	favs := radio.LoadFavorites()
	rp := radio.New(radio.Options{Favorites: favs, Country: radio.CountryDeclined})
	rt := openCatalog(nil, rp, config.Config{})
	t.Cleanup(rt.close)
	if rt.engine == nil {
		t.Fatal("the catalog did not open")
	}
	sent := make(chan tea.Msg, 16)
	rt.send = func(m tea.Msg) { sent <- m }

	station := radio.CatalogStation{Name: "Zebra Jazz 88.3", URL: "https://zebra.example/stream", Country: "Norway"}
	found := func() []string {
		res, err := rt.store.Search(context.Background(), catalog.ParseQuery("zebra"), 5)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, r := range res[catalog.SearchStation] {
			names = append(names, r.Track.Title+" "+r.Track.PlayableURI)
		}
		return names
	}
	for _, want := range [][]string{{"Zebra Jazz 88.3 https://zebra.example/stream"}, nil} {
		if _, err := favs.Toggle(station); err != nil {
			t.Fatal(err)
		}
		select {
		case m := <-sent:
			if msg, ok := m.(model.CatalogChangedMsg); !ok || msg.Provider != catalog.Radio {
				t.Fatalf("sent %+v, want radio's change", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the UI was not told the catalog changed")
		}
		if got := found(); !slices.Equal(got, want) {
			t.Fatalf("search finds %v, want %v", got, want)
		}
	}
}

// gatedSource blocks each Fetch until released and counts the runs.
type gatedSource struct {
	namedSource
	mu      sync.Mutex
	runs    int
	started chan struct{}
	release chan struct{}
}

func (g *gatedSource) Fetch(ctx context.Context, c string, k catalogsync.Known) (catalog.Snapshot, error) {
	g.mu.Lock()
	g.runs++
	g.mu.Unlock()
	g.started <- struct{}{}
	<-g.release
	return g.namedSource.Fetch(ctx, c, k)
}

// A quiet source's sync requested while one runs (a favorite toggled
// mid-sync) is served by one more run, not dropped.
func TestQuietSyncRequestedMidRunRunsAgain(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	src := &gatedSource{namedSource: namedSource{catalog.Radio}, started: make(chan struct{}, 4), release: make(chan struct{})}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.setSources(source{Source: src, quiet: true})
	t.Cleanup(rt.close)

	rt.sync(catalog.Radio)
	<-src.started          // the first run is fetching
	rt.sync(catalog.Radio) // requested mid-run: finds it running
	close(src.release)
	<-src.started // the running sync ran again
	rt.wg.Wait()
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.runs != 2 {
		t.Errorf("runs = %d, want 2", src.runs)
	}
}

// lateWorker is a worker whose Hold blocks from its second call on, until
// late is closed, and which reports each Run.
type lateWorker struct {
	mu    sync.Mutex
	holds int
	late  chan struct{}
	ran   chan struct{}
}

func (w *lateWorker) Hold() func() {
	w.mu.Lock()
	w.holds++
	wait := w.holds > 1
	w.mu.Unlock()
	if wait {
		<-w.late
	}
	return func() {}
}

func (w *lateWorker) Run(context.Context) error {
	w.ran <- struct{}{}
	return nil
}

// A request made while a quiet sync runs is counted by that sync and starts
// nothing of its own. Were it to start its own, and get there only after the
// running sync had finished, both would serve it: a third run.
func TestQuietSyncMidRunRequestStartsNoSecondSync(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	src := &gatedSource{namedSource: namedSource{catalog.Radio}, started: make(chan struct{}, 4), release: make(chan struct{})}
	w := &lateWorker{late: make(chan struct{}), ran: make(chan struct{}, 4)}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.setSources(source{Source: src, quiet: true, worker: w})
	t.Cleanup(rt.close)

	rt.sync(catalog.Radio)
	<-src.started          // the first run is fetching
	rt.sync(catalog.Radio) // requested mid-run
	src.release <- struct{}{}
	<-src.started // the running sync ran again
	src.release <- struct{}{}
	<-w.ran // the running sync is done: it started its worker

	// Anything the mid-run request started gets to its sync only now.
	close(w.late)
	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-src.started:
		src.release <- struct{}{}
		<-done
		t.Fatal("the mid-run request ran a sync of its own: 3 runs, want 2")
	case <-done:
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if src.runs != 2 {
		t.Errorf("runs = %d, want 2", src.runs)
	}
}

// failingSecondSource succeeds on its first Fetch and fails from then on.
type failingSecondSource struct {
	namedSource
	mu    sync.Mutex
	calls int
}

func (s *failingSecondSource) Fetch(context.Context, string, catalogsync.Known) (catalog.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return catalog.Snapshot{}, catalogsync.ErrUnchanged
	}
	return catalog.Snapshot{}, errors.New("second sync failed")
}

// slowReleaseWorker is a worker whose first Hold's release announces itself
// and then blocks until unblock is closed: the first sync, done syncing,
// pauses there before it finishes.
type slowReleaseWorker struct {
	mu        sync.Mutex
	holds     int
	releasing chan struct{}
	unblock   chan struct{}
	ran       chan struct{}
}

func (w *slowReleaseWorker) Hold() func() {
	w.mu.Lock()
	w.holds++
	first := w.holds == 1
	w.mu.Unlock()
	return func() {
		if first {
			close(w.releasing)
			<-w.unblock
		}
	}
}

func (w *slowReleaseWorker) Run(context.Context) error {
	w.ran <- struct{}{}
	return nil
}

// A sync that succeeded and is still finishing must not cancel the retry of
// a newer sync that failed meanwhile: the provider would stay failed until
// its next refresh or a restart.
func TestOlderSyncKeepsNewerSyncsRetry(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	await := func(what string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}
	retryArmed := func(rt *catalogRuntime) bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return rt.providers[catalog.Spotify].retry != nil
	}
	w := &slowReleaseWorker{releasing: make(chan struct{}), unblock: make(chan struct{}), ran: make(chan struct{}, 4)}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}, retryMin: time.Hour, retryMax: time.Hour}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.setSources(source{Source: &failingSecondSource{namedSource: namedSource{catalog.Spotify}}, worker: w})
	t.Cleanup(rt.close)
	var unblock sync.Once
	defer unblock.Do(func() { close(w.unblock) })

	rt.sync(catalog.Spotify)
	await("the first sync to finish syncing", w.releasing)
	rt.sync(catalog.Spotify) // the provider is free: a second sync, which fails
	await("the second sync to finish", w.ran)
	if !retryArmed(rt) {
		t.Fatal("the failed second sync armed no retry")
	}

	unblock.Do(func() { close(w.unblock) }) // the first sync finishes now
	rt.wg.Wait()
	if !retryArmed(rt) {
		t.Fatal("the older, successful sync cancelled the newer failed sync's retry")
	}
}

// fakePlayer is a minimal provider for wiring tests.
type fakePlayer struct{ name string }

func (p fakePlayer) Name() string                              { return p.name }
func (fakePlayer) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (fakePlayer) Tracks(string) ([]playlist.Track, error)     { return nil, nil }

// Spotify's menu is built from what its sync covers; without a catalog it
// browses live.
func TestLibrarySourcesSyncedMenus(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	defer rt.cancel()
	rt.setSources(source{Source: &namedSource{catalog.Spotify}})
	providers := []model.ProviderEntry{{Key: "spotify", Provider: fakePlayer{"Spotify"}}}

	src := librarySources(providers, "", rt)
	if len(src.Synced) != 1 || src.Synced[0].Provider != catalog.Spotify || !slices.Equal(src.Synced[0].Collections, []string{"albums"}) {
		t.Errorf("synced = %+v, want Spotify with its sync's collections", src.Synced)
	}
	if src := librarySources(providers, "", &catalogRuntime{providers: map[string]*providerSync{}}); len(src.Synced) != 0 || src.Catalog != nil {
		t.Errorf("without a catalog: synced %+v, catalog %v", src.Synced, src.Catalog)
	}
}

// YouTube Music's saved albums and followed artists are not synced, so its
// lists must not be titled "Albums" and "Artists" as Spotify's are: they are
// "Albums from tracks" and "Artists from tracks".
func TestYouTubeListsAreFromTracks(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	defer rt.cancel()
	rt.setSources(source{Source: &namedSource{catalog.Spotify}, lists: []string{catalog.CollectionArtists}},
		source{Source: &namedSource{catalog.YouTube}, lists: []string{catalog.CollectionArtists}})
	src := librarySources([]model.ProviderEntry{
		{Key: "spotify", Provider: fakePlayer{"Spotify"}}, {Key: "ytmusic", Provider: fakePlayer{"YouTube Music"}}}, "", rt)
	want := map[string][]string{
		catalog.Spotify: {"Albums", "Artists"},
		catalog.YouTube: {library.AlbumsFromTracks, library.ArtistsFromTracks},
	}
	if len(src.Synced) != len(want) {
		t.Fatalf("synced = %+v", src.Synced)
	}
	for _, s := range src.Synced {
		entries, err := library.SyncedMenu(src.Catalog, s).Load(context.Background())
		if err != nil || len(entries) != 2 {
			t.Errorf("%s menu = %+v (err %v)", s.Provider, entries, err)
			continue
		}
		if s.ListsFromTracks != (s.Provider == catalog.YouTube) {
			t.Errorf("%s: lists from tracks %v", s.Provider, s.ListsFromTracks)
		}
		for i, e := range entries {
			note, _ := e.Open.(library.NotedLevel)
			hasNote := note != nil && len(note.Note()) > 0
			if e.Title != want[s.Provider][i] || hasNote != s.ListsFromTracks {
				t.Errorf("%s list %d is %q with note %v, want %q with note %v", s.Provider, i, e.Title, hasNote, want[s.Provider][i], s.ListsFromTracks)
			}
		}
	}
}

// The YouTube sync reads the account the way it is signed in.
func TestYouTubeClientFollowsSignIn(t *testing.T) {
	orig := ytdlpAvailable
	t.Cleanup(func() { ytdlpAvailable = orig })
	ytdlpAvailable = func() bool { return true }
	cookies := config.YouTubeMusicConfig{CookiesFrom: "brave+gnomekeyring"}
	oauth := config.YouTubeMusicConfig{ClientID: "id", ClientSecret: "secret"}
	both := config.YouTubeMusicConfig{CookiesFrom: "brave", ClientID: "id", ClientSecret: "secret"}
	disabled := both
	disabled.Disabled = true
	tests := []struct {
		name string
		yt   config.YouTubeMusicConfig
		want string
	}{
		{"cookies", cookies, "*ytmusic.CookieCatalog"},
		{"oauth", oauth, "*ytmusic.OAuthCatalog"},
		{"both", both, "youtubesrc.Mixed"},
		{"disabled", disabled, "<nil>"},
		{"not signed in", config.YouTubeMusicConfig{}, "<nil>"},
		{"half an oauth client", config.YouTubeMusicConfig{ClientID: "id"}, "<nil>"},
	}
	for _, tt := range tests {
		if got := fmt.Sprintf("%T", youtubeClient(tt.yt)); got != tt.want {
			t.Errorf("%s: client = %s, want %s", tt.name, got, tt.want)
		}
	}
	ytdlpAvailable = func() bool { return false }
	if c := youtubeClient(both); c != nil {
		t.Errorf("without yt-dlp: client = %T, want none", c)
	}
}
