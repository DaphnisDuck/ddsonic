package catalogsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// AlbumSource fetches album track lists from one provider.
type AlbumSource interface {
	// Provider is the catalog provider name, e.g. catalog.Spotify.
	Provider() string
	// AlbumTracks fetches an album's complete track list, retrying rate
	// limits itself. It serves loads the user is waiting on.
	AlbumTracks(ctx context.Context, album catalog.Ref) ([]catalog.TrackRecord, error)
	// AlbumTracksOnce is AlbumTracks for background work: a rate limit
	// fails at once with a *catalog.RateLimitError.
	AlbumTracksOnce(ctx context.Context, album catalog.Ref) ([]catalog.TrackRecord, error)
}

// AlbumStore is the part of the catalog the Filler reads and writes.
type AlbumStore interface {
	// UncachedAlbums lists provider's library albums without cached
	// tracks, in the order to fill them.
	UncachedAlbums(ctx context.Context, provider string) ([]catalog.Album, error)
	// CacheAlbumTracks stores an album's complete track list and marks the
	// album cached.
	CacheAlbumTracks(ctx context.Context, album catalog.Ref, tracks []catalog.TrackRecord) error
	// AlbumTracksByRef reads an album's tracks back, and whether they are
	// cached, finding the album by ref: its listed ID may be another
	// album's by now.
	AlbumTracksByRef(ctx context.Context, album catalog.Ref) ([]catalog.Track, bool, error)
}

// Pacing is how gently the Filler uses the provider.
type Pacing struct {
	Delay       time.Duration // between albums
	MinBackoff  time.Duration // first wait after a rate limit or failure
	MaxBackoff  time.Duration // the backoff doubles up to this
	MaxFailures int           // consecutive non-rate-limit failures before giving up (Enricher: tracks skipped in a row)
}

// DefaultPacing keeps a library fill well under Spotify's rate limit. A
// development-mode client ID was blocked for hours at one album a second.
var DefaultPacing = Pacing{Delay: 10 * time.Second, MinBackoff: 5 * time.Second, MaxBackoff: 10 * time.Minute, MaxFailures: 3}

// Filler caches album track lists: on demand when the user opens an
// uncached album (FetchAlbumTracks), and in the background for every saved
// album (Run), one at a time. Background work pauses while a foreground
// load or a Hold is active, and backs off exponentially on rate limits.
type Filler struct {
	store AlbumStore
	src   AlbumSource
	pace  Pacing
	sleep func(ctx context.Context, d time.Duration) error // replaced in tests
	pauser
}

// pauser is a background worker's pause and single-run control: Hold
// pauses it, waitIdle blocks while held, and begin/end keep one run at a
// time.
type pauser struct {
	mu      sync.Mutex
	holds   int
	idle    chan struct{} // closed when holds drops to zero
	running bool
	rerun   bool // a run was asked for while one ran
}

var _ catalog.AlbumTrackFetcher = (*Filler)(nil)

// NewFiller returns a Filler caching src's albums into store.
func NewFiller(store AlbumStore, src AlbumSource, pace Pacing) *Filler {
	return &Filler{store: store, src: src, pace: pace, sleep: sleepCtx}
}

// Hold pauses the worker's background run until release is called, for
// example while a sync or a foreground load uses the provider. Holds nest.
func (f *pauser) Hold() (release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.holds == 0 {
		f.idle = make(chan struct{})
	}
	f.holds++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.holds--
			if f.holds == 0 {
				close(f.idle)
			}
		})
	}
}

// waitIdle blocks while any Hold is active.
func (f *pauser) waitIdle(ctx context.Context) error {
	f.mu.Lock()
	if f.holds == 0 {
		f.mu.Unlock()
		return ctx.Err()
	}
	idle := f.idle
	f.mu.Unlock()
	select {
	case <-idle:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// FetchAlbumTracks implements catalog.AlbumTrackFetcher. It pauses the
// background fill while it runs.
func (f *Filler) FetchAlbumTracks(ctx context.Context, album catalog.Album) ([]catalog.Track, error) {
	defer f.Hold()()
	recs, err := f.src.AlbumTracks(ctx, album.Ref)
	if err != nil {
		return nil, err
	}
	if err := f.store.CacheAlbumTracks(ctx, album.Ref, recs); err != nil {
		return nil, err
	}
	tracks, _, err := f.store.AlbumTracksByRef(ctx, album.Ref)
	return tracks, err
}

// Run caches every uncached library album, then lists again to pick up
// albums saved meanwhile, until nothing new is left. An album the provider
// refuses is skipped for this run. Run returns ErrRunning if a fill is
// already running (that fill then goes again when it is done), ctx's error when cancelled, and an error after
// MaxFailures consecutive failures other than rate limits.
func (f *Filler) Run(ctx context.Context) error {
	if !f.begin() {
		return fmt.Errorf("fill %s: %w", f.src.Provider(), ErrRunning)
	}
	return f.repeat(func() error { return f.fillAll(ctx) })
}

// fillAll is one Run's work.
func (f *Filler) fillAll(ctx context.Context) error {
	provider := f.src.Provider()
	tried := map[int64]bool{}
	var st fillState
	for {
		albums, err := f.store.UncachedAlbums(ctx, provider)
		if err != nil {
			return fmt.Errorf("fill %s: %w", provider, err)
		}
		albums = slices.DeleteFunc(albums, func(a catalog.Album) bool { return tried[a.ID] })
		if len(albums) == 0 {
			return nil
		}
		for _, a := range albums {
			tried[a.ID] = true
			if err := f.fill(ctx, a, &st); err != nil {
				return fmt.Errorf("fill %s: album %q: %w", provider, a.Title, err)
			}
		}
	}
}

// fillState carries backoff across the albums of one run.
type fillState struct {
	backoff  time.Duration
	failures int
}

func (st *fillState) next(p Pacing) time.Duration {
	st.backoff = NextBackoff(st.backoff, p.MinBackoff, p.MaxBackoff)
	return st.backoff
}

// NextBackoff is the wait after cur: double it, at least lo and at most hi.
// Zero cur starts at lo.
func NextBackoff(cur, lo, hi time.Duration) time.Duration {
	return min(max(cur*2, lo), hi)
}

// fill caches one album, retrying it through rate limits and transient
// failures.
func (f *Filler) fill(ctx context.Context, a catalog.Album, st *fillState) error {
	for {
		if err := f.waitIdle(ctx); err != nil {
			return err
		}
		// A foreground open may have cached it while this run waited.
		if _, cached, err := f.store.AlbumTracksByRef(ctx, a.Ref); errors.Is(err, catalog.ErrNotFound) || (err == nil && cached) {
			return nil
		}
		recs, err := f.src.AlbumTracksOnce(ctx, a.Ref)
		if err == nil {
			err = f.store.CacheAlbumTracks(ctx, a.Ref, recs)
		}
		var rl *catalog.RateLimitError
		switch {
		case err == nil:
			st.backoff, st.failures = 0, 0
			return f.sleep(ctx, f.pace.Delay)
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &rl):
			// Not a failure: wait as long as asked, and at least the backoff.
			if err := f.sleep(ctx, max(st.next(f.pace), rl.RetryAfter)); err != nil {
				return err
			}
		case errors.Is(err, catalog.ErrForbidden), errors.Is(err, catalog.ErrNotFound):
			return f.sleep(ctx, f.pace.Delay)
		default:
			st.failures++
			if st.failures >= f.pace.MaxFailures {
				return err
			}
			if err := f.sleep(ctx, st.next(f.pace)); err != nil {
				return err
			}
		}
	}
}

func (f *pauser) begin() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		f.rerun = true // the running one goes again when it is done
		return false
	}
	f.running = true
	return true
}

// repeat runs work until it fails, or it succeeds with no run asked for
// meanwhile, and then ends the run. The check and the end are one step, so
// a request refused with ErrRunning is never lost between them.
func (f *pauser) repeat(work func() error) error {
	for {
		err := work()
		f.mu.Lock()
		if err == nil && f.rerun {
			f.rerun = false
			f.mu.Unlock()
			continue
		}
		f.running, f.rerun = false, false
		f.mu.Unlock()
		return err
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
