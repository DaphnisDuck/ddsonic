package main

// ddsonic (#170): YouTube's automatic work (its sync and the enricher)
// stops at a recognized rate limit, challenge or spent quota and stays
// held, across restarts, until a sync the user asks for succeeds.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync"
)

var (
	ytChallenge = &catalog.RateLimitError{Quota: true, Reason: "YouTube asks to confirm this is not a bot"}
	ytQuota     = &catalog.RateLimitError{Quota: true, Reason: "YouTube Data API: quotaExceeded"}
)

// ytSource is a YouTube source with two collections. A collection with an
// error in errs fails with it; one fetched while ctx is done is refused
// before sending. When outstanding is set, the first collection's fetch
// waits for release after closing it.
type ytSource struct {
	mu                   sync.Mutex
	errs                 map[string]error
	fetched              []string
	outstanding, release chan struct{}
}

func (*ytSource) Provider() string      { return catalog.YouTube }
func (*ytSource) Collections() []string { return []string{"liked", "playlists"} }

func (s *ytSource) Fetch(ctx context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	if ctx.Err() != nil {
		return catalog.Snapshot{}, context.Cause(ctx) // refused before sending
	}
	s.mu.Lock()
	s.fetched = append(s.fetched, collection)
	err := s.errs[collection]
	wait := collection == "liked" && s.outstanding != nil
	s.mu.Unlock()
	if wait {
		close(s.outstanding)
		<-s.release
	}
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("sync youtube/%s: %w", collection, err)
	}
	return catalog.Snapshot{Provider: catalog.YouTube, Collection: collection}, nil
}

func (s *ytSource) set(collection string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errs == nil {
		s.errs = map[string]error{}
	}
	s.errs[collection] = err
}

func (s *ytSource) fetches() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.fetched)
}

// ytWorker stands in for the enricher. A run returns err; when entered is
// set, the first run closes it and waits for resume first; when untilDone
// is set, a run waits for its context and keeps its cause.
type ytWorker struct {
	countingWorker
	mu              sync.Mutex
	err             error
	entered, resume chan struct{}
	untilDone       bool
	cause           error
}

func (w *ytWorker) Run(ctx context.Context) error {
	w.runs.Add(1)
	if w.entered != nil {
		close(w.entered)
		<-w.resume
	}
	if w.untilDone {
		<-ctx.Done()
		w.mu.Lock()
		w.cause = context.Cause(ctx)
		w.mu.Unlock()
		return ctx.Err()
	}
	if ctx.Err() != nil {
		return ctx.Err() // nothing sent
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// ytRuntime is a runtime over the catalog at path with one YouTube source,
// its hold restored from the catalog, as openCatalog sets it up.
func ytRuntime(t *testing.T, path string, src catalogsync.Source, w worker) *catalogRuntime {
	t.Helper()
	rt := runtimeAt(t, path)
	rt.retryMin, rt.retryMax = time.Hour, time.Hour
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w, held: rt.restoreHold(catalog.YouTube), recordHold: true})
	rt.send = func(tea.Msg) {}
	return rt
}

// ytSettled waits for YouTube's sync and the runtime's goroutines.
func ytSettled(t *testing.T, rt *catalogRuntime) *providerSync {
	t.Helper()
	ps := rt.providers[catalog.YouTube]
	waitUntil(t, "the sync to finish", func() bool {
		rt.mu.Lock()
		defer rt.mu.Unlock()
		return !ps.running
	})
	workersStopped(t, rt)
	return ps
}

// ytState is the hold, the retry, and the stored mark.
func ytState(t *testing.T, rt *catalogRuntime) (held, retry, stored bool) {
	t.Helper()
	ps := rt.providers[catalog.YouTube]
	rt.mu.Lock()
	held, retry = ps.held, ps.retry != nil
	rt.mu.Unlock()
	stored, err := rt.store.QuotaExceeded(rt.ctx, catalog.YouTube)
	if err != nil {
		t.Fatal(err)
	}
	return held, retry, stored
}

// A challenge the enricher meets holds YouTube at once and is stored: no
// automatic sync, retry or enrichment runs, in this run or after a
// restart, until the user syncs ("r"); that sync's success releases the
// hold and its mark, and enrichment resumes.
func TestAYouTubeThrottleHoldsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	src, w := &ytSource{}, &ytWorker{err: fmt.Errorf("enrich youtube: %q: %w", "a video", ytChallenge)}
	rt := ytRuntime(t, path, src, w)
	rt.runWorker(catalog.YouTube, w)
	workersStopped(t, rt)
	if held, retry, stored := ytState(t, rt); !held || retry || !stored || w.holds.Load() != 1 {
		t.Fatalf("held %t, retry %t, stored %t, worker holds %d; want held, none, stored, paused", held, retry, stored, w.holds.Load())
	}
	rt.sync(catalog.YouTube)
	rt.runWorker(catalog.YouTube, w)
	rt.startSyncs()
	ytSettled(t, rt)
	if f := src.fetches(); len(f) != 0 || w.runs.Load() != 1 {
		t.Errorf("held: fetched %v, worker runs %d; want nothing more", f, w.runs.Load())
	}
	rt.close()

	// Restart: the stored mark holds the new run from the start.
	w2 := &ytWorker{}
	rt = ytRuntime(t, path, src, w2)
	rt.startSyncs()
	ytSettled(t, rt)
	if held, _, _ := ytState(t, rt); !held || len(src.fetches()) != 0 || w2.runs.Load() != 0 {
		t.Fatalf("after a restart: held %t, fetched %v, worker runs %d; want held and nothing", held, src.fetches(), w2.runs.Load())
	}

	// Recovery: the user syncs.
	rt.refresh(catalog.YouTube)
	ytSettled(t, rt)
	if held, retry, stored := ytState(t, rt); held || retry || stored {
		t.Errorf("after the user's sync: held %t, retry %t, stored %t; want none", held, retry, stored)
	}
	if f := src.fetches(); !slices.Equal(f, []string{"liked", "playlists"}) || w2.runs.Load() != 1 {
		t.Errorf("after the user's sync: fetched %v, worker runs %d; want both collections and the worker", f, w2.runs.Load())
	}
}

// A spent quota in a sync stops it before the next collection, holds
// YouTube with no retry armed, and is stored.
func TestAYouTubeQuotaInASyncHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	src, w := &ytSource{}, &ytWorker{}
	src.set("liked", ytQuota)
	rt := ytRuntime(t, path, src, w)
	rt.sync(catalog.YouTube)
	ytSettled(t, rt)
	if held, retry, stored := ytState(t, rt); !held || retry || !stored {
		t.Errorf("held %t, retry %t, stored %t; want held, none, stored", held, retry, stored)
	}
	if f := src.fetches(); !slices.Equal(f, []string{"liked"}) || w.runs.Load() != 0 {
		t.Errorf("fetched %v, worker runs %d; want liked only, no worker", f, w.runs.Load())
	}
	rt.close()
	rt = ytRuntime(t, path, src, &ytWorker{})
	if held, _, _ := ytState(t, rt); !held {
		t.Error("the hold was not restored")
	}
}

// An ordinary failure is not a hold: it is retried, and nothing is stored.
func TestAnOrdinaryYouTubeFailureDoesNotHold(t *testing.T) {
	src := &ytSource{}
	src.set("liked", errors.New("yt-dlp: HTTP Error 500"))
	rt := ytRuntime(t, filepath.Join(t.TempDir(), "library.db"), src, &ytWorker{})
	rt.sync(catalog.YouTube)
	ytSettled(t, rt)
	if held, retry, stored := ytState(t, rt); held || !retry || stored {
		t.Errorf("held %t, retry %t, stored %t; want a retry only", held, retry, stored)
	}
}

// ytHoldingSource succeeds, holding YouTube in memory (no new observation)
// while it fetches.
type ytHoldingSource struct {
	ytSource
	rt *catalogRuntime
}

func (h *ytHoldingSource) Fetch(ctx context.Context, collection string, k catalogsync.Known) (catalog.Snapshot, error) {
	h.rt.mu.Lock()
	h.rt.providers[catalog.YouTube].held = true
	h.rt.mu.Unlock()
	return h.ytSource.Fetch(ctx, collection, k)
}

// A background sync that succeeds while YouTube is held does not release
// the hold, nor clear its mark.
func TestABackgroundSuccessNeverClearsAYouTubeHold(t *testing.T) {
	src := &ytHoldingSource{}
	rt := ytRuntime(t, filepath.Join(t.TempDir(), "library.db"), src, nil)
	src.rt = rt
	if err := rt.store.SetQuotaExceeded(rt.ctx, catalog.YouTube, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	rt.sync(catalog.YouTube)
	ytSettled(t, rt)
	if held, _, stored := ytState(t, rt); !held || !stored {
		t.Errorf("held %t, stored %t; want both kept", held, stored)
	}
}

// The enricher meeting a challenge while a background sync runs fences
// that sync: once its outstanding read is answered, the next collection is
// not fetched.
func TestAnEnricherThrottleFencesARunningBackgroundSync(t *testing.T) {
	src := &ytSource{outstanding: make(chan struct{}), release: make(chan struct{})}
	w := &ytWorker{err: fmt.Errorf("enrich youtube: %w", ytChallenge), entered: make(chan struct{}), resume: make(chan struct{})}
	rt := ytRuntime(t, filepath.Join(t.TempDir(), "library.db"), src, w)
	rt.runWorker(catalog.YouTube, w)
	<-w.entered // its read is under way
	rt.sync(catalog.YouTube)
	<-src.outstanding // the sync's first read too
	close(w.resume)   // the enricher's read answers with a challenge
	waitUntil(t, "the hold", func() bool { held, _, _ := ytState(t, rt); return held })
	close(src.release)
	ytSettled(t, rt)
	if f := src.fetches(); !slices.Equal(f, []string{"liked"}) {
		t.Errorf("fetched %v; want liked only (none after the challenge)", f)
	}
	if held, retry, stored := ytState(t, rt); !held || retry || !stored {
		t.Errorf("held %t, retry %t, stored %t; want held, none, stored", held, retry, stored)
	}
}

// A quota from a sync stops a running enricher: its context is cancelled
// with the quota as the cause. Handed its context but not yet started, it
// starts on a cancelled context and sends nothing.
func TestAYouTubeHoldStopsTheEnricher(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		src := &ytSource{}
		w := &ytWorker{untilDone: true}
		rt := ytRuntime(t, filepath.Join(t.TempDir(), "library.db"), src, w)
		rt.runWorker(catalog.YouTube, w)
		waitUntil(t, "the enricher to run", func() bool { return w.runs.Load() == 1 })
		src.set("liked", ytQuota)
		rt.refresh(catalog.YouTube) // asked for; fails with the quota
		ytSettled(t, rt)
		w.mu.Lock()
		cause := w.cause
		w.mu.Unlock()
		if !errors.Is(cause, errQuotaFence) {
			t.Errorf("the enricher's context ended with %v; want the quota fence", cause)
		}
	})
	t.Run("not yet started", func(t *testing.T) {
		src := &ytSource{}
		var sent atomic.Bool
		w := &ytWorker{entered: make(chan struct{}), resume: make(chan struct{})}
		rt := ytRuntime(t, filepath.Join(t.TempDir(), "library.db"), src, &sendingWorker{ytWorker: w, sent: &sent})
		rt.runWorker(catalog.YouTube, rt.providers[catalog.YouTube].worker)
		<-w.entered // handed its context, nothing sent
		src.set("liked", ytQuota)
		rt.refresh(catalog.YouTube)
		waitUntil(t, "the hold", func() bool { held, _, _ := ytState(t, rt); return held })
		close(w.resume)
		ytSettled(t, rt)
		if sent.Load() {
			t.Error("the enricher sent after the hold")
		}
	})
}

// sendingWorker records whether its run got past its context check, as a
// read would be sent.
type sendingWorker struct {
	*ytWorker
	sent *atomic.Bool
}

func (s *sendingWorker) Run(ctx context.Context) error {
	err := s.ytWorker.Run(ctx)
	if err == nil {
		s.sent.Store(true)
	}
	return err
}

// A stored mark that cannot be read is taken as held, as Spotify's is.
func TestAnUnreadableYouTubeHoldIsTakenAsHeld(t *testing.T) {
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	if rt.restoreHold(catalog.YouTube) {
		t.Fatal("a fresh catalog restored a hold")
	}
	rt.store.Close()
	if !rt.restoreHold(catalog.YouTube) {
		t.Error("an unreadable mark was not taken as held")
	}
}
