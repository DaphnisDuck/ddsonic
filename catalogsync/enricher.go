package catalogsync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// MetadataSource reads a track's real title, artists, album and year, for
// a provider whose sync knows only what its videos are called (YouTube).
type MetadataSource interface {
	// Provider is the catalog provider name, e.g. catalog.YouTube.
	Provider() string
	// TrackMetadata reads one track. It returns a zero TrackMetadata when
	// there is nothing to find, a *catalog.RateLimitError when the service
	// asks to slow down, and an error wrapping catalog.ErrForbidden when the
	// track cannot be read at all.
	TrackMetadata(ctx context.Context, track catalog.Ref) (catalog.TrackMetadata, error)
}

// EnrichStore is the part of the catalog the Enricher reads and writes.
type EnrichStore interface {
	UnenrichedTracks(ctx context.Context, provider string, limit int) ([]catalog.Track, error)
	EnrichTrack(ctx context.Context, track catalog.Ref, meta catalog.TrackMetadata) error
	// RefreshDerived rebuilds the provider's albums and artists from its
	// enriched library tracks.
	RefreshDerived(ctx context.Context, provider string) error
	// RecordEnrichFailure counts a run in which the track could not be
	// read, marking it read at giveUpAfter counted runs.
	RecordEnrichFailure(ctx context.Context, track catalog.Ref, giveUpAfter int) (gaveUp bool, err error)
	// Sweep deletes the provider's rows nothing references any more, such
	// as a derived album whose only track left the library.
	Sweep(ctx context.Context, provider string) error
}

const (
	// enrichBatch is how many tracks the Enricher reads before refreshing
	// the derived albums and artists, so the lists grow while it works.
	enrichBatch = 10
	// enrichAttempts is how often a run tries one track before skipping it.
	enrichAttempts = 2
	// enrichGiveUpRuns is how many runs may fail to read a track before it
	// is marked read, like a track that cannot be read at all.
	enrichGiveUpRuns = 3
)

// Enricher fills in the library tracks of a provider with their real
// metadata, one at a time in the background, newest first. Like the
// Filler, it pauses while held, waits out rate limits with a doubling
// backoff, and skips a track that keeps failing. A track is read once: one
// with nothing to find, or that cannot be read, is marked read, and so is
// one that failed in enrichGiveUpRuns runs.
type Enricher struct {
	store EnrichStore
	src   MetadataSource
	pace  Pacing
	sleep func(ctx context.Context, d time.Duration) error // replaced in tests
	// changed, if set, is called after the derived albums and artists are
	// refreshed, so the UI can reload.
	changed func()
	pauser
}

// NewEnricher returns an Enricher of src's tracks in store. changed may
// be nil.
func NewEnricher(store EnrichStore, src MetadataSource, pace Pacing, changed func()) *Enricher {
	return &Enricher{store: store, src: src, pace: pace, sleep: sleepCtx, changed: changed}
}

// Run enriches every unread library track, refreshing the derived lists
// as it goes and at the end. A track that fails enrichAttempts times is
// skipped for the rest of the run. Its failure is counted only when the
// same run read another track afterwards, or earlier when it failed at the
// end: a run that read nothing may be an outage, not bad tracks. MaxFailures
// skipped in a row end the run with the error, uncounted. Run returns
// ErrRunning if a run is already going (it then goes again when done), ctx's error when cancelled, a store
// failure at once, and an error naming the last failure when tracks were
// skipped.
func (e *Enricher) Run(ctx context.Context) error {
	if !e.begin() {
		return fmt.Errorf("enrich %s: %w", e.src.Provider(), ErrRunning)
	}
	return e.repeat(func() error { return e.enrichAll(ctx) })
}

// enrichAll is one Run's work.
func (e *Enricher) enrichAll(ctx context.Context) error {
	provider := e.src.Provider()
	// The library may have changed since the last run: refresh first, and
	// drop the albums and artists the refresh left without a member, so a
	// removed track's album leaves search now rather than at the next sync.
	if err := e.refresh(ctx); err != nil {
		return err
	}
	if err := e.store.Sweep(ctx, provider); err != nil {
		return fmt.Errorf("enrich %s: %w", provider, err)
	}
	var (
		st      fillState
		skip    = make(map[catalog.Ref]bool)
		failed  []catalog.Ref // skipped before a success: counted
		streak  []catalog.Ref // skipped since the last success
		read    bool          // a track was read this run
		lastErr error
	)
	for {
		tracks, err := e.store.UnenrichedTracks(ctx, provider, enrichBatch+len(skip))
		if err != nil {
			return fmt.Errorf("enrich %s: %w", provider, err)
		}
		tracks = slices.DeleteFunc(tracks, func(t catalog.Track) bool { return skip[t.Ref] })
		if len(tracks) == 0 {
			if read { // the tracks after the last success failed on their own
				failed = append(failed, streak...)
			}
			return e.countFailures(ctx, failed, len(skip), lastErr)
		}
		found := false
		for _, t := range tracks {
			ok, err := e.enrich(ctx, t, &st)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, errStore) {
				return fmt.Errorf("enrich %s: %w", provider, err)
			}
			if err != nil {
				skip[t.Ref] = true
				streak = append(streak, t.Ref)
				lastErr = fmt.Errorf("enrich %s: %q: %w", provider, t.Title, err)
				if len(streak) >= e.pace.MaxFailures {
					if cerr := e.countFailures(ctx, failed, 0, nil); cerr != nil {
						return errors.Join(lastErr, cerr)
					}
					return lastErr
				}
				continue
			}
			failed, streak, read = append(failed, streak...), nil, true
			found = found || ok
		}
		// A batch that found nothing changed no album or artist.
		if found {
			if err := e.refresh(ctx); err != nil {
				return err
			}
		}
	}
}

// countFailures records a failed run for each of tracks and returns
// lastErr, so the run's end reports that skipped tracks were left unread.
func (e *Enricher) countFailures(ctx context.Context, tracks []catalog.Ref, skipped int, lastErr error) error {
	for _, t := range tracks {
		if _, err := e.store.RecordEnrichFailure(ctx, t, enrichGiveUpRuns); err != nil && !errors.Is(err, catalog.ErrNotFound) {
			return errors.Join(lastErr, err)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("skipped %d tracks: %w", skipped, lastErr)
	}
	return nil
}

// errStore marks a failure to write a read track: the catalog's, not the
// track's, so it ends the run instead of counting against the track.
var errStore = errors.New("store")

func (e *Enricher) refresh(ctx context.Context) error {
	if err := e.store.RefreshDerived(ctx, e.src.Provider()); err != nil {
		return fmt.Errorf("enrich %s: %w", e.src.Provider(), err)
	}
	if e.changed != nil {
		e.changed()
	}
	return nil
}

// enrich reads one track, retrying it through rate limits and up to
// enrichAttempts other failures, and reports whether it found music
// details. After its last failure it waits the backoff, so a run meeting
// an outage slows down before the next track.
func (e *Enricher) enrich(ctx context.Context, t catalog.Track, st *fillState) (bool, error) {
	for attempt := 1; ; {
		if err := e.waitIdle(ctx); err != nil {
			return false, err
		}
		meta, err := e.src.TrackMetadata(ctx, t.Ref)
		if errors.Is(err, catalog.ErrForbidden) {
			meta, err = catalog.TrackMetadata{}, nil // unreadable: marked read
		}
		if err == nil {
			err = e.store.EnrichTrack(ctx, t.Ref, meta)
			if err != nil && !errors.Is(err, catalog.ErrNotFound) && ctx.Err() == nil {
				return false, fmt.Errorf("%w: %w", errStore, err)
			}
		}
		var rl *catalog.RateLimitError
		switch {
		case err == nil, errors.Is(err, catalog.ErrNotFound): // gone since listing
			st.backoff = 0
			return err == nil && meta.Found(), e.sleep(ctx, e.pace.Delay)
		case ctx.Err() != nil:
			return false, ctx.Err()
		case errors.As(err, &rl):
			if err := e.sleep(ctx, max(st.next(e.pace), rl.RetryAfter)); err != nil {
				return false, err
			}
		default:
			if serr := e.sleep(ctx, st.next(e.pace)); serr != nil || attempt >= enrichAttempts {
				return false, cmp.Or(serr, err)
			}
			attempt++
		}
	}
}
