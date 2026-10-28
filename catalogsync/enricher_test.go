package catalogsync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
)

// fakeMeta answers metadata reads; errs scripts each video's errors in
// order before it answers.
type fakeMeta struct {
	mu    sync.Mutex
	errs  map[string][]error
	reads []string
}

func (*fakeMeta) Provider() string { return catalog.YouTube }

func (f *fakeMeta) TrackMetadata(_ context.Context, t catalog.Ref) (catalog.TrackMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, t.ProviderID)
	if errs := f.errs[t.ProviderID]; len(errs) > 0 {
		err := errs[0]
		f.errs[t.ProviderID] = errs[1:]
		return catalog.TrackMetadata{}, err
	}
	artist := []catalog.ArtistRecord{{Ref: catalog.Ref{Provider: catalog.YouTube, ProviderID: "artist:a"}, Name: "Artist A"}}
	return catalog.TrackMetadata{Title: "Song " + t.ProviderID, Artists: artist,
		Album: &catalog.AlbumRecord{Ref: catalog.Ref{Provider: catalog.YouTube, ProviderID: "album:a"}, Title: "Album A", Artists: artist}}, nil
}

func enricherSetup(t *testing.T, videos ...string) (*sqlite.Store, *fakeMeta, *Enricher, *[]time.Duration, *int) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	snap := catalog.Snapshot{Provider: catalog.YouTube, Collection: "liked"}
	for _, v := range videos {
		snap.Tracks = append(snap.Tracks, catalog.TrackRecord{Ref: catalog.Ref{Provider: catalog.YouTube, ProviderID: v},
			Title: "video " + v, PlayableURI: "https://music.youtube.com/watch?v=" + v})
	}
	if err := store.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	src := &fakeMeta{errs: map[string][]error{}}
	changes := 0
	e := NewEnricher(store, src, testPacing, func() { changes++ })
	waits := &[]time.Duration{}
	e.sleep = func(ctx context.Context, d time.Duration) error { *waits = append(*waits, d); return ctx.Err() }
	return store, src, e, waits, &changes
}

func TestEnricherFillsTheLibrary(t *testing.T) {
	store, src, e, waits, changes := enricherSetup(t, "v1", "v2", "v3")
	src.errs["v2"] = []error{&catalog.RateLimitError{}, catalog.ErrForbidden}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	liked, _ := store.LikedTracks(ctx, catalog.YouTube)
	got := map[string]string{}
	for _, tr := range liked {
		got[tr.Ref.ProviderID] = tr.Title + "|" + tr.AlbumTitle
	}
	if got["v1"] != "Song v1|Album A" || got["v3"] != "Song v3|Album A" || got["v2"] != "video v2|" {
		t.Errorf("tracks = %v; want v1 and v3 enriched, unreadable v2 as it was", got)
	}
	if left, _ := store.UnenrichedTracks(ctx, catalog.YouTube, 10); len(left) != 0 {
		t.Errorf("unenriched = %d, want every track read once", len(left))
	}
	albums, _ := store.Albums(ctx, catalog.YouTube, catalog.ByTitle)
	if len(albums) != 1 || albums[0].Title != "Album A" || *changes < 2 {
		t.Errorf("albums %+v after %d refreshes", albums, *changes)
	}
	// The rate limit waited the first backoff, then v2 was read again.
	if (*waits)[0] != testPacing.Delay || (*waits)[1] != testPacing.MinBackoff {
		t.Errorf("waits = %v", *waits)
	}
	// Nothing left: a second run reads nothing.
	before := len(src.reads)
	if err := e.Run(ctx); err != nil || len(src.reads) != before {
		t.Errorf("second run: %v, %d new reads", err, len(src.reads)-before)
	}
}

// A batch that finds nothing refreshes nothing beyond the run's first
// refresh.
func TestEnricherSkipsEmptyRefresh(t *testing.T) {
	_, src, e, _, changes := enricherSetup(t, "v1", "v2")
	src.errs["v1"] = []error{catalog.ErrForbidden}
	src.errs["v2"] = []error{catalog.ErrForbidden}
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *changes != 1 {
		t.Errorf("refreshed %d times, want only at the start", *changes)
	}
}

// addTracks adds liked videos, newer than those already stored.
func addTracks(t *testing.T, store *sqlite.Store, videos ...string) {
	t.Helper()
	snap := catalog.Snapshot{Provider: catalog.YouTube, Collection: "more" + videos[0]}
	for _, v := range videos {
		snap.Tracks = append(snap.Tracks, catalog.TrackRecord{Ref: catalog.Ref{Provider: catalog.YouTube, ProviderID: v},
			Title: "video " + v, PlayableURI: "https://music.youtube.com/watch?v=" + v})
	}
	if err := store.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
}

func unenriched(t *testing.T, store *sqlite.Store) []string {
	t.Helper()
	left, err := store.UnenrichedTracks(context.Background(), catalog.YouTube, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tr := range left {
		out = append(out, tr.Ref.ProviderID)
	}
	return out
}

// A track that keeps failing is skipped, so the tracks after it are still
// enriched. Each run that read another track counts against it; after
// enrichGiveUpRuns of them it is marked read.
func TestEnricherSkipsABadTrack(t *testing.T) {
	store, src, e, _, _ := enricherSetup(t, "v1", "v2", "v3")
	bad := errors.New("yt-dlp: some new refusal")
	for range 10 {
		src.errs["v2"] = append(src.errs["v2"], bad)
	}
	ctx := context.Background()
	for run := 1; run <= enrichGiveUpRuns; run++ {
		if run > 1 {
			addTracks(t, store, fmt.Sprintf("new%d", run)) // read before v2
		}
		if err := e.Run(ctx); !errors.Is(err, bad) {
			t.Fatalf("run %d: %v, want the skipped track's error", run, err)
		}
		want := []string{"v2"}
		if run == enrichGiveUpRuns {
			want = nil // marked read
		}
		if got := unenriched(t, store); !slices.Equal(got, want) {
			t.Fatalf("run %d: unenriched %v, want %v", run, got, want)
		}
	}
	if err := e.Run(ctx); err != nil {
		t.Errorf("after giving up: %v", err)
	}
}

// A run that reads nothing cannot tell bad tracks from an outage, so it
// counts nothing: one or two new tracks met while offline stay unread.
func TestEnricherCountsNothingWithoutASuccess(t *testing.T) {
	store, src, e, _, _ := enricherSetup(t, "v1", "v2")
	down := errors.New("dial tcp: no route to host")
	for _, v := range []string{"v1", "v2"} {
		for range 10 * enrichAttempts {
			src.errs[v] = append(src.errs[v], down)
		}
	}
	for run := 1; run <= enrichGiveUpRuns+1; run++ {
		if err := e.Run(context.Background()); !errors.Is(err, down) {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if got := unenriched(t, store); !slices.Equal(got, []string{"v2", "v1"}) {
		t.Errorf("unenriched %v, want both kept for a later read", got)
	}
}

// A failure to write what was read is the catalog's: the run ends and the
// track is not counted.
func TestEnricherStopsOnStoreFailure(t *testing.T) {
	store, _, e, _, _ := enricherSetup(t, "v1")
	e.store = failingEnrichStore{store}
	if err := e.Run(context.Background()); !errors.Is(err, errDiskFull) {
		t.Fatalf("Run = %v, want the store error", err)
	}
	if got := unenriched(t, store); !slices.Equal(got, []string{"v1"}) {
		t.Errorf("unenriched %v", got)
	}
}

// Cancelling a run counts nothing, not even a track that failed first.
func TestEnricherCancelledCountsNothing(t *testing.T) {
	store, src, e, _, _ := enricherSetup(t, "v1", "v2")
	for range 10 {
		src.errs["v2"] = append(src.errs["v2"], errors.New("refused"))
	}
	for run := 1; run <= enrichGiveUpRuns; run++ {
		ctx, cancel := context.WithCancel(context.Background())
		e.sleep = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
		if err := e.Run(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("run %d: %v, want cancelled", run, err)
		}
	}
	if got := unenriched(t, store); !slices.Equal(got, []string{"v2", "v1"}) {
		t.Errorf("unenriched %v, want both", got)
	}
}

var errDiskFull = errors.New("disk full")

type failingEnrichStore struct{ *sqlite.Store }

func (failingEnrichStore) EnrichTrack(context.Context, catalog.Ref, catalog.TrackMetadata) error {
	return errDiskFull
}
func (failingEnrichStore) RecordEnrichFailure(context.Context, catalog.Ref, int) (bool, error) {
	return false, errors.New("counted a store failure")
}

// MaxFailures tracks failing in a row is an outage: the run ends, and the
// failures are not counted against the tracks.
func TestEnricherStopsOnOutage(t *testing.T) {
	store, src, e, _, _ := enricherSetup(t, "v1", "v2", "v3", "v4")
	down := errors.New("dial tcp: no route to host")
	for _, v := range []string{"v4", "v3", "v2"} { // newest first
		for range enrichGiveUpRuns * enrichAttempts {
			src.errs[v] = append(src.errs[v], down)
		}
	}
	ctx := context.Background()
	for run := 1; run <= enrichGiveUpRuns; run++ {
		if err := e.Run(ctx); !errors.Is(err, down) {
			t.Fatalf("run %d: %v, want the network error", run, err)
		}
	}
	left, _ := store.UnenrichedTracks(ctx, catalog.YouTube, 10)
	if len(left) != 4 || slices.Contains(src.reads, "v1") {
		t.Errorf("unenriched %d, reads %v; want every track kept and v1 never reached", len(left), src.reads)
	}
}

// An album known only from an enriched track leaves the catalog, and search,
// at the next enrichment once that track is no longer in the library.
func TestEnricherDropsOrphanedDerivedAlbums(t *testing.T) {
	store, _, e, _, _ := enricherSetup(t, "v1")
	ctx := context.Background()
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if albums, _ := store.Albums(ctx, catalog.YouTube, catalog.ByTitle); len(albums) != 1 {
		t.Fatalf("albums after enrichment = %d, want the derived Album A", len(albums))
	}
	// The track is unliked: the sync drops it and sweeps.
	if err := store.ApplySnapshot(ctx, catalog.Snapshot{Provider: catalog.YouTube, Collection: "liked"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Sweep(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if albums, _ := store.Albums(ctx, catalog.YouTube, catalog.ByTitle); len(albums) != 0 {
		t.Errorf("albums = %+v, want the orphaned album gone", albums)
	}
	found, err := store.Search(ctx, catalog.ParseQuery("album a"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(found[catalog.SearchAlbum]); n != 0 {
		t.Errorf("search finds %d albums, want none", n)
	}
}
