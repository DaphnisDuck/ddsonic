package catalogsync

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
)

var testPacing = Pacing{Delay: time.Millisecond, MinBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, MaxFailures: 3}

// fakeAlbums serves album track lists. errs scripts the errors an album's
// background fetches return, in order, before it succeeds.
type fakeAlbums struct {
	mu     sync.Mutex
	errs   map[string][]error
	calls  []string
	called chan string // if set, receives each call's album ID
}

func (f *fakeAlbums) Provider() string { return catalog.Spotify }

func (f *fakeAlbums) fetch(kind string, album catalog.Ref) ([]catalog.TrackRecord, error) {
	f.mu.Lock()
	f.calls = append(f.calls, kind+album.ProviderID)
	var err error
	if errs := f.errs[album.ProviderID]; len(errs) > 0 {
		err, f.errs[album.ProviderID] = errs[0], errs[1:]
	}
	f.mu.Unlock()
	if f.called != nil {
		f.called <- album.ProviderID
	}
	if err != nil {
		return nil, err
	}
	id := album.ProviderID
	return []catalog.TrackRecord{{Ref: catalog.Ref{ProviderID: id + "-t1"}, Title: id + " one", TrackNo: 1, PlayableURI: "spotify:track:" + id}}, nil
}

func (f *fakeAlbums) AlbumTracks(_ context.Context, album catalog.Ref) ([]catalog.TrackRecord, error) {
	return f.fetch("fg:", album)
}

func (f *fakeAlbums) AlbumTracksOnce(_ context.Context, album catalog.Ref) ([]catalog.TrackRecord, error) {
	return f.fetch("", album)
}

func (f *fakeAlbums) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// fillerSetup returns a store holding saved albums ids (the first saved
// most recently), a source and a filler recording its waits.
func fillerSetup(t *testing.T, ids ...string) (*sqlite.Store, *fakeAlbums, *Filler, *[]time.Duration) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snap := catalog.Snapshot{Provider: catalog.Spotify, Collection: "albums"}
	for i, id := range ids {
		snap.Albums = append(snap.Albums, catalog.AlbumRecord{
			Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: id}, Title: "Album " + id,
			AddedAt: time.Unix(int64(1000-i), 0),
		})
	}
	if err := store.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	src := &fakeAlbums{errs: map[string][]error{}}
	f := NewFiller(store, src, testPacing)
	var mu sync.Mutex
	waits := &[]time.Duration{}
	f.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		*waits = append(*waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return store, src, f, waits
}

func uncached(t *testing.T, store *sqlite.Store) []string {
	t.Helper()
	albums, err := store.UncachedAlbums(context.Background(), catalog.Spotify)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range albums {
		out = append(out, a.Ref.ProviderID)
	}
	return out
}

func TestFillerCachesEverySavedAlbum(t *testing.T) {
	store, src, f, waits := fillerSetup(t, "A", "B", "C")
	if err := f.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := src.callList(); !slices.Equal(got, []string{"A", "B", "C"}) {
		t.Errorf("fetched %v, want newest saved first", got)
	}
	if left := uncached(t, store); len(left) != 0 {
		t.Errorf("uncached after fill: %v", left)
	}
	if want := []time.Duration{testPacing.Delay, testPacing.Delay, testPacing.Delay}; !slices.Equal(*waits, want) {
		t.Errorf("waits = %v, want the delay between albums", *waits)
	}
	// Nothing left: a second run fetches nothing.
	if err := f.Run(context.Background()); err != nil || len(src.callList()) != 3 {
		t.Errorf("second run: err %v, calls %v", err, src.callList())
	}
}

func TestFillerBacksOffOnRateLimits(t *testing.T) {
	store, src, f, waits := fillerSetup(t, "A", "B")
	src.errs["A"] = []error{
		&catalog.RateLimitError{}, &catalog.RateLimitError{}, &catalog.RateLimitError{},
		&catalog.RateLimitError{RetryAfter: time.Hour},
	}
	if err := f.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Rate limits never count as failures: A is retried until it lands.
	if got := src.callList(); !slices.Equal(got, []string{"A", "A", "A", "A", "A", "B"}) {
		t.Errorf("fetched %v", got)
	}
	want := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, // doubling, capped
		time.Hour,                          // Retry-After wins when longer
		testPacing.Delay, testPacing.Delay, // then the normal pace, backoff reset
	}
	if !slices.Equal(*waits, want) {
		t.Errorf("waits = %v, want %v", *waits, want)
	}
	if left := uncached(t, store); len(left) != 0 {
		t.Errorf("uncached after fill: %v", left)
	}
}

func TestFillerSkipsRefusedAlbums(t *testing.T) {
	store, src, f, _ := fillerSetup(t, "A", "B")
	src.errs["A"] = []error{catalog.ErrForbidden}
	if err := f.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A is tried once per run, not retried in the run's re-listing.
	if got := src.callList(); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("fetched %v", got)
	}
	if left := uncached(t, store); !slices.Equal(left, []string{"A"}) {
		t.Errorf("uncached = %v, want only the refused album", left)
	}
}

func TestFillerGivesUpAfterRepeatedFailures(t *testing.T) {
	store, src, f, _ := fillerSetup(t, "A", "B")
	offline := errors.New("dial tcp: no route to host")
	src.errs["A"] = []error{offline, offline, offline}
	if err := f.Run(context.Background()); !errors.Is(err, offline) {
		t.Fatalf("Run() = %v, want the network error", err)
	}
	if got := src.callList(); !slices.Equal(got, []string{"A", "A", "A"}) {
		t.Errorf("fetched %v, want 3 tries of A and then stop", got)
	}
	if left := uncached(t, store); len(left) != 2 {
		t.Errorf("uncached = %v", left)
	}
	// A transient failure followed by success resets the count.
	src.errs["A"] = []error{offline, offline}
	src.errs["B"] = []error{offline, offline}
	if err := f.Run(context.Background()); err != nil {
		t.Errorf("Run() with recoveries = %v", err)
	}
}

func TestFillerPausesWhileHeld(t *testing.T) {
	_, src, f, _ := fillerSetup(t, "A")
	src.called = make(chan string, 4)
	release := f.Hold()
	done := make(chan error, 1)
	go func() { done <- f.Run(context.Background()) }()
	select {
	case id := <-src.called:
		t.Fatalf("fetched %s while held", id)
	case <-time.After(30 * time.Millisecond):
	}
	release()
	release() // releasing twice is harmless
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := src.callList(); !slices.Equal(got, []string{"A"}) {
		t.Errorf("fetched %v", got)
	}
}

func TestFillerStopsOnCancel(t *testing.T) {
	_, _, f, _ := fillerSetup(t, "A")
	defer f.Hold()()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run() = %v, want context.Canceled", err)
	}
}

func TestFillerRunsOnce(t *testing.T) {
	_, _, f, _ := fillerSetup(t, "A")
	release := f.Hold()
	done := make(chan error, 1)
	go func() { done <- f.Run(context.Background()) }()
	// Wait until the first run holds the running flag.
	for !func() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.running }() {
		time.Sleep(time.Millisecond)
	}
	if err := f.Run(context.Background()); !errors.Is(err, ErrRunning) {
		t.Errorf("concurrent Run() = %v, want ErrRunning", err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFetchAlbumTracksCachesAndSkipsTheFill(t *testing.T) {
	store, src, f, _ := fillerSetup(t, "A", "B")
	albums, err := store.Albums(context.Background(), catalog.Spotify, catalog.ByTitle)
	if err != nil {
		t.Fatal(err)
	}
	tracks, err := f.FetchAlbumTracks(context.Background(), albums[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].ID == 0 || tracks[0].AlbumID != albums[0].ID || tracks[0].Title != "A one" {
		t.Errorf("FetchAlbumTracks() = %+v, want the cached rows", tracks)
	}
	if err := f.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The open used the retrying fetch, and the fill did not fetch A again.
	if got := src.callList(); !slices.Equal(got, []string{"fg:A", "B"}) {
		t.Errorf("fetched %v", got)
	}
}
