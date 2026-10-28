package catalogsync

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
)

var errPage = errors.New("http status 503")

// fakeSource serves each collection as pages of album IDs, the way a real
// provider pages its API. failAt makes page n (1-based) of a collection fail.
type fakeSource struct {
	mu      sync.Mutex
	pages   map[string][][]string
	failAt  map[string]int
	gate    chan struct{} // if set, Fetch waits on it
	known   Known
	fetched []string
}

func (f *fakeSource) Provider() string { return catalog.Spotify }

func (f *fakeSource) Collections() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for c := range f.pages {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func (f *fakeSource) set(collection string, pages ...[]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pages == nil {
		f.pages = map[string][][]string{}
	}
	f.pages[collection] = pages
	delete(f.failAt, collection)
}

func (f *fakeSource) fail(collection string, page int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt == nil {
		f.failAt = map[string]int{}
	}
	f.failAt[collection] = page
}

func (f *fakeSource) Fetch(ctx context.Context, collection string, known Known) (catalog.Snapshot, error) {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return catalog.Snapshot{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.known = known
	f.fetched = append(f.fetched, collection)
	snap := catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}
	for i, page := range f.pages[collection] {
		if f.failAt[collection] == i+1 {
			return catalog.Snapshot{}, errPage
		}
		for _, id := range page {
			snap.Albums = append(snap.Albums, catalog.AlbumRecord{
				Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: id}, Title: "Album " + id,
				Artists: []catalog.ArtistRecord{{Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: "ar-" + id}, Name: "Artist " + id}},
			})
		}
	}
	return snap, nil
}

func setup(t *testing.T) (*sqlite.Store, *fakeSource, *Engine, *[]Event) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	src := &fakeSource{}
	var mu sync.Mutex
	events := &[]Event{}
	eng := New(store, func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		*events = append(*events, ev)
	}, src)
	return store, src, eng, events
}

func albumIDs(t *testing.T, store *sqlite.Store) []string {
	t.Helper()
	albums, err := store.Albums(context.Background(), catalog.Spotify, catalog.ByTitle)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range albums {
		out = append(out, a.Ref.ProviderID)
	}
	slices.Sort(out)
	return out
}

func TestSyncScenarios(t *testing.T) {
	ctx := context.Background()
	store, src, eng, _ := setup(t)

	// A: empty catalog; the provider returns A B C across two pages.
	src.set("albums", []string{"A", "B"}, []string{"C"})
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatalf("A: Sync() = %v", err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Fatalf("A: albums = %v, want A B C", got)
	}
	st, _ := store.SyncStatus(ctx, catalog.Spotify)
	firstSuccess := st[0].LastSuccess

	// B: the catalog holds A B C; the provider now returns A C D.
	src.set("albums", []string{"A", "C"}, []string{"D"})
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatalf("B: Sync() = %v", err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"A", "C", "D"}) {
		t.Fatalf("B: albums = %v, want A C D", got)
	}
	st, _ = store.SyncStatus(ctx, catalog.Spotify)
	lastSuccess := st[0].LastSuccess
	if lastSuccess.Before(firstSuccess) {
		t.Errorf("B: last success went backwards")
	}

	// C: page 1 succeeds, page 2 fails. The cache must survive untouched.
	src.set("albums", []string{"A"}, []string{"C", "D"})
	src.fail("albums", 2)
	if err := eng.Sync(ctx, catalog.Spotify); !errors.Is(err, errPage) {
		t.Fatalf("C: Sync() = %v, want the page error", err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"A", "C", "D"}) {
		t.Fatalf("C: albums = %v, want A C D intact", got)
	}
	st, _ = store.SyncStatus(ctx, catalog.Spotify)
	if st[0].LastError == "" || !st[0].LastSuccess.Equal(lastSuccess) {
		t.Errorf("C: status = %+v, want the failure recorded and last success unchanged", st[0])
	}
}

// B's reconciliation drops removed albums and also their now-unreferenced
// artists, but never another collection's members.
func TestSyncSweepsOrphansAndIsolatesCollections(t *testing.T) {
	ctx := context.Background()
	store, src, eng, _ := setup(t)
	src.set("albums", []string{"A", "B"})
	src.set("other", []string{"B", "X"})
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}
	src.set("albums", []string{"A"})
	src.set("other", []string{"X"})
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"A", "X"}) {
		t.Fatalf("albums = %v, want A X", got)
	}
	// B left both collections, so it and its artist are gone.
	if albums, _ := store.ArtistAlbums(ctx, 2); len(albums) != 0 {
		t.Errorf("orphaned artist of B still has albums: %+v", albums)
	}

	// Emptying one collection leaves the other alone.
	src.set("albums")
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"X"}) {
		t.Errorf("albums = %v, want X from the other collection", got)
	}
}

func TestSyncContinuesPastAFailedCollection(t *testing.T) {
	store, src, eng, events := setup(t)
	src.set("albums", []string{"A"})
	src.set("other", []string{"X"})
	src.fail("albums", 1)
	err := eng.Sync(context.Background(), catalog.Spotify)
	if !errors.Is(err, errPage) {
		t.Fatalf("Sync() = %v, want the albums failure", err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"X"}) {
		t.Errorf("albums = %v, want X from the healthy collection", got)
	}
	var kinds []EventKind
	var failed []string
	for _, ev := range *events {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == CollectionDone && ev.Err != nil {
			failed = append(failed, ev.Collection)
		}
	}
	if !slices.Equal(kinds, []EventKind{Started, CollectionDone, CollectionDone, Finished}) || !slices.Equal(failed, []string{"albums"}) {
		t.Errorf("events = %v, failed = %v", kinds, failed)
	}
}

func TestCancelledSyncIsNotRecordedAsFailure(t *testing.T) {
	store, src, eng, _ := setup(t)
	src.set("albums", []string{"A"})
	src.gate = make(chan struct{}) // never opens
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := eng.Sync(ctx, catalog.Spotify); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync() = %v, want context.Canceled", err)
	}
	if st, _ := store.SyncStatus(context.Background(), catalog.Spotify); len(st) != 0 {
		t.Errorf("status = %+v, want nothing recorded for a cancelled sync", st)
	}
}

func TestSyncRunsOncePerProvider(t *testing.T) {
	_, src, eng, _ := setup(t)
	src.set("albums", []string{"A"})
	src.gate = make(chan struct{})
	done := make(chan error)
	go func() { done <- eng.Sync(context.Background(), catalog.Spotify) }()

	// Wait until the first sync holds the provider.
	for !func() bool { eng.mu.Lock(); defer eng.mu.Unlock(); return eng.running[catalog.Spotify] }() {
	}
	if err := eng.Sync(context.Background(), catalog.Spotify); !errors.Is(err, ErrRunning) {
		t.Errorf("concurrent Sync() = %v, want ErrRunning", err)
	}
	close(src.gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Free again once the first finishes.
	if err := eng.Sync(context.Background(), catalog.Spotify); err != nil {
		t.Errorf("Sync() after completion = %v", err)
	}
}

func TestSyncPassesKnownPlaylistSnapshots(t *testing.T) {
	store, src, eng, _ := setup(t)
	ctx := context.Background()
	if err := store.ApplySnapshot(ctx, catalog.Snapshot{Provider: catalog.Spotify, Collection: "playlists",
		Playlists: []catalog.PlaylistRecord{{Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: "pl"},
			Name: "Mine", Snapshot: "s1", TracksFetched: true}}}); err != nil {
		t.Fatal(err)
	}
	src.set("albums")
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}
	if src.known.PlaylistSnapshots["pl"] != "s1" {
		t.Errorf("source saw known = %+v, want pl's snapshot", src.known)
	}
}

func TestSourceReturningTheWrongCollectionFails(t *testing.T) {
	store, _, _, _ := setup(t)
	eng := New(store, nil, wrongSource{})
	if err := eng.Sync(context.Background(), catalog.Spotify); err == nil {
		t.Fatal("Sync() accepted a snapshot for another collection")
	}
	if st, _ := store.SyncStatus(context.Background(), catalog.Spotify); len(st) != 1 || st[0].LastError == "" {
		t.Errorf("status = %+v, want the mismatch recorded", st)
	}
}

type wrongSource struct{}

func (wrongSource) Provider() string      { return catalog.Spotify }
func (wrongSource) Collections() []string { return []string{"albums"} }
func (wrongSource) Fetch(context.Context, string, Known) (catalog.Snapshot, error) {
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: "playlists"}, nil
}

func TestUnknownProvider(t *testing.T) {
	_, _, eng, _ := setup(t)
	if err := eng.Sync(context.Background(), "tidal"); err == nil {
		t.Error("Sync(unknown) = nil")
	}
}

// A sync cancelled between collections must not report success: the later
// collections were never synced.
func TestSyncCancelledBetweenCollectionsReportsIt(t *testing.T) {
	store, src, _, _ := setup(t)
	src.set("albums", []string{"A"})
	src.set("other", []string{"X"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eng := New(store, func(ev Event) {
		if ev.Kind == CollectionDone {
			cancel()
		}
	}, src)
	if err := eng.Sync(ctx, catalog.Spotify); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sync() = %v, want context.Canceled", err)
	}
	if got := albumIDs(t, store); !slices.Equal(got, []string{"A"}) {
		t.Errorf("albums = %v, want only the collection synced before cancelling", got)
	}
}
