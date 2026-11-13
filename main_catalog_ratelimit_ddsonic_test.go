package main

// ddsonic: the catalog runtime's side of the Spotify rate-limit gate: every
// block is recorded and restored, store failures err on the closed side, and
// an exhausted quota holds all automatic Spotify work.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/external/spotify"
)

// fakeKeeper stands in for the Spotify provider's gate.
type fakeKeeper struct {
	mu   sync.Mutex
	sets []keeperSet
	fn   func(until time.Time, quota bool)
}

type keeperSet struct {
	until time.Time
	quota bool
}

func (k *fakeKeeper) SetRateLimitedUntil(until time.Time, quota bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sets = append(k.sets, keeperSet{until, quota})
}

func (k *fakeKeeper) OnRateLimited(fn func(until time.Time, quota bool)) { k.fn = fn }

func (k *fakeKeeper) restored() []keeperSet {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]keeperSet(nil), k.sets...)
}

// runtimeAt is a runtime over the catalog at path, with no sources.
func runtimeAt(t *testing.T, path string) *catalogRuntime {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{},
		retryMin: 5 * time.Millisecond, retryMax: 10 * time.Millisecond}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	t.Cleanup(rt.close)
	return rt
}

func sameMilli(a, b time.Time) bool { return a.UnixMilli() == b.UnixMilli() }

// Every block is recorded, short ones included, and a later run restores the
// latest end and the quota mark before anything is sent.
func TestRateLimitsPersistAcrossARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	now := time.Now()

	rt := runtimeAt(t, path)
	k := &fakeKeeper{}
	if held := rt.keepRateLimit(k, rt.store); held || len(k.restored()) != 0 {
		t.Fatalf("a fresh catalog: held %t, restored %v; want neither", held, k.restored())
	}
	k.fn(now.Add(30*time.Second), false) // a short block
	if got, err := rt.store.RateLimitedUntil(rt.ctx, catalog.Spotify); err != nil || !sameMilli(got, now.Add(30*time.Second)) {
		t.Fatalf("recorded %v, %v; want the short block", got, err)
	}
	rt.close()

	rt = runtimeAt(t, path) // restart
	k = &fakeKeeper{}
	if held := rt.keepRateLimit(k, rt.store); held {
		t.Error("an ordinary block restored as a quota hold")
	}
	if r := k.restored(); len(r) != 1 || !sameMilli(r[0].until, now.Add(30*time.Second)) || r[0].quota {
		t.Fatalf("restored %v, want the short block", r)
	}
	k.fn(now.Add(90*time.Second), true)  // a quota block
	k.fn(now.Add(40*time.Second), false) // recorded late, shorter: kept the longer
	rt.close()

	rt = runtimeAt(t, path)
	k = &fakeKeeper{}
	if held := rt.keepRateLimit(k, rt.store); !held {
		t.Error("the quota mark was not restored")
	}
	if r := k.restored(); len(r) != 1 || !sameMilli(r[0].until, now.Add(90*time.Second)) || !r[0].quota {
		t.Errorf("restored %v, want the 90s quota block", r)
	}
}

// flakyLimits is the catalog's rate-limit store with each read and the
// writes able to fail on their own.
type flakyLimits struct {
	rateLimitStore
	failUntil, failQuota, failWrites bool
}

var errStore = errors.New("database is locked")

func (f *flakyLimits) RateLimitedUntil(ctx context.Context, p string) (time.Time, error) {
	if f.failUntil {
		return time.Time{}, errStore
	}
	return f.rateLimitStore.RateLimitedUntil(ctx, p)
}

func (f *flakyLimits) QuotaExceeded(ctx context.Context, p string) (bool, error) {
	if f.failQuota {
		return false, errStore
	}
	return f.rateLimitStore.QuotaExceeded(ctx, p)
}

func (f *flakyLimits) SetRateLimitedUntil(ctx context.Context, p string, until time.Time) error {
	if f.failWrites {
		return errStore
	}
	return f.rateLimitStore.SetRateLimitedUntil(ctx, p, until)
}

func (f *flakyLimits) SetQuotaExceeded(ctx context.Context, p string, at time.Time) error {
	if f.failWrites {
		return errStore
	}
	return f.rateLimitStore.SetQuotaExceeded(ctx, p, at)
}

// Each unreadable value is taken as the observation it might hide, at the
// floor, on its own: an unreadable block as a floor block (no hold); an
// unreadable quota mark as a quota observed now (held, and a floor quota
// block); both together as both.
func TestUnreadableRateLimitsAreTakenConservatively(t *testing.T) {
	tests := []struct {
		name                 string
		failUntil, failQuota bool
		storedQuota          bool
		wantHeld, wantQuota  bool
	}{
		{"the block is unreadable", true, false, false, false, false},
		{"the block is unreadable, a quota mark is stored", true, false, true, true, true},
		{"the quota mark is unreadable", false, true, false, true, true},
		{"both are unreadable", true, true, false, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
			if tt.storedQuota {
				if err := rt.store.SetQuotaExceeded(rt.ctx, catalog.Spotify, time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			k := &fakeKeeper{}
			before := time.Now()
			held := rt.keepRateLimit(k, &flakyLimits{rateLimitStore: rt.store, failUntil: tt.failUntil, failQuota: tt.failQuota})
			r := k.restored()
			if held != tt.wantHeld || len(r) != 1 || r[0].quota != tt.wantQuota ||
				r[0].until.Before(before.Add(spotify.MinCooldown)) || r[0].until.After(time.Now().Add(spotify.MinCooldown)) {
				t.Errorf("held %t, restored %v; want held %t and a %v block, quota %t", held, r, tt.wantHeld, spotify.MinCooldown, tt.wantQuota)
			}
		})
	}
	// A longer stored block is kept when only the quota mark is unreadable.
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	long := time.Now().Add(time.Hour)
	if err := rt.store.SetRateLimitedUntil(rt.ctx, catalog.Spotify, long); err != nil {
		t.Fatal(err)
	}
	k := &fakeKeeper{}
	rt.keepRateLimit(k, &flakyLimits{rateLimitStore: rt.store, failQuota: true})
	if r := k.restored(); len(r) != 1 || !sameMilli(r[0].until, long) || !r[0].quota {
		t.Errorf("restored %v, want the stored hour, as a quota block", r)
	}
}

// A failed write is logged only: the hold is taken in memory all the same,
// and the provider's gate is not touched.
func TestRateLimitWriteFailuresStillHold(t *testing.T) {
	src, w := &quotaSource{}, &countingWorker{}
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	k := &fakeKeeper{}
	rt.keepRateLimit(k, &flakyLimits{rateLimitStore: rt.store, failWrites: true})
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
	k.fn(time.Now().Add(time.Hour), true)
	ps := rt.providers[catalog.Spotify]
	rt.mu.Lock()
	held := ps.held
	rt.mu.Unlock()
	if !held || len(k.restored()) != 0 {
		t.Errorf("held %t, gate calls %v; want held and the gate untouched", held, k.restored())
	}
}

// quotaSource is a Spotify source that answers with err while it is set.
type quotaSource struct {
	mu    sync.Mutex
	err   error
	syncs int
}

func (*quotaSource) Provider() string      { return catalog.Spotify }
func (*quotaSource) Collections() []string { return []string{"albums"} }

func (q *quotaSource) Fetch(_ context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.syncs++
	if q.err != nil {
		return catalog.Snapshot{}, q.err
	}
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}, nil
}

func (q *quotaSource) set(err error) {
	q.mu.Lock()
	q.err = err
	q.mu.Unlock()
}

func (q *quotaSource) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.syncs
}

// countingWorker counts its runs and its active holds.
type countingWorker struct{ runs, holds atomic.Int32 }

func (w *countingWorker) Hold() func() {
	w.holds.Add(1)
	var once sync.Once
	return func() { once.Do(func() { w.holds.Add(-1) }) }
}
func (w *countingWorker) Run(context.Context) error {
	w.runs.Add(1)
	return nil
}

func settledProvider(t *testing.T, rt *catalogRuntime) *providerSync {
	t.Helper()
	ps := rt.providers[catalog.Spotify]
	deadline := time.Now().Add(5 * time.Second)
	for {
		rt.mu.Lock()
		running := ps.running
		rt.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sync did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	rt.wg.Wait()
	return ps
}

var errQuota = fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: time.Millisecond, Quota: true})

// An exhausted quota holds every automatic Spotify sync and the worker: no
// retry is armed, the startup of a later run syncs nothing, and only a sync
// the user asks for runs; when it succeeds the hold and its mark are gone.
func TestAnExhaustedQuotaHoldsAutomaticWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	src, w := &quotaSource{}, &countingWorker{}
	rt := runtimeAt(t, path)
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
	rt.send = func(tea.Msg) {}

	src.set(errQuota)
	rt.refresh(catalog.Spotify)
	ps := settledProvider(t, rt)
	time.Sleep(50 * time.Millisecond) // ten retry delays
	rt.mu.Lock()
	held, retry := ps.held, ps.retry
	rt.mu.Unlock()
	if !held || retry != nil || src.count() != 1 || w.runs.Load() != 0 {
		t.Fatalf("after a quota failure: held %t, retry armed %t, syncs %d, worker runs %d; want held, none, 1, 0",
			held, retry != nil, src.count(), w.runs.Load())
	}
	rt.close()

	// A later run, whose catalog holds the quota mark (keepRateLimit's
	// restore is TestRateLimitsPersistAcrossARestart's): startup does nothing.
	rt = runtimeAt(t, path)
	if err := rt.store.SetQuotaExceeded(rt.ctx, catalog.Spotify, time.Now()); err != nil {
		t.Fatal(err)
	}
	src, w = &quotaSource{}, &countingWorker{}
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w, held: true})
	rt.send = func(tea.Msg) {}
	rt.startSyncs()
	time.Sleep(50 * time.Millisecond)
	rt.wg.Wait()
	if src.count() != 0 || w.runs.Load() != 0 {
		t.Fatalf("a held startup: syncs %d, worker runs %d; want none", src.count(), w.runs.Load())
	}

	// The user asks: it runs, succeeds, and the hold and mark are cleared.
	rt.refresh(catalog.Spotify)
	ps = settledProvider(t, rt)
	rt.mu.Lock()
	held = ps.held
	rt.mu.Unlock()
	if mark, err := rt.store.QuotaExceeded(rt.ctx, catalog.Spotify); src.count() != 1 || held || err != nil || mark {
		t.Errorf("after r: syncs %d, held %t, mark %t (%v); want 1, cleared, cleared", src.count(), held, mark, err)
	}
	waitUntil(t, "the worker after the sync", func() bool { return w.runs.Load() == 1 })
}

// An ordinary rate limit does not hold: its retry is armed as before.
func TestAnOrdinaryRateLimitDoesNotHold(t *testing.T) {
	src, w := &quotaSource{}, &countingWorker{}
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	rt.retryMin, rt.retryMax = time.Hour, time.Hour
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
	rt.send = func(tea.Msg) {}
	src.set(fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: time.Minute}))
	rt.refresh(catalog.Spotify)
	ps := settledProvider(t, rt)
	rt.mu.Lock()
	held, retry := ps.held, ps.retry
	rt.mu.Unlock()
	if held || retry == nil {
		t.Errorf("held %t, retry armed %t; want not held, a retry armed", held, retry != nil)
	}
}

func TestQuotaExceededFindsJoinedErrors(t *testing.T) {
	ordinary := fmt.Errorf("a: %w", &catalog.RateLimitError{RetryAfter: time.Minute})
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errDown, false},
		{ordinary, false},
		{errQuota, true},
		{errors.Join(errDown, ordinary, errQuota), true},
		{fmt.Errorf("outer: %w", errors.Join(ordinary, errQuota)), true},
	} {
		if got := quotaExceeded(tt.err); got != tt.want {
			t.Errorf("quotaExceeded(%v) = %t, want %t", tt.err, got, tt.want)
		}
	}
}

func waitUntil(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// A quota observed outside a sync, by something the user did or by the
// background fill, holds the runtime at once: the armed retry is cancelled,
// the worker is paused, and neither a firing retry nor the worker runs.
func TestAQuotaSeenOutsideASyncHoldsTheRuntime(t *testing.T) {
	for _, where := range []string{"foreground", "background fill"} {
		t.Run(where, func(t *testing.T) {
			src, w := &quotaSource{}, &countingWorker{}
			rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
			rt.retryMin, rt.retryMax = time.Hour, time.Hour
			k := &fakeKeeper{}
			rt.keepRateLimit(k, rt.store)
			rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
			rt.send = func(tea.Msg) {}

			// An ordinary rate limit arms a retry.
			src.set(fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: time.Minute}))
			rt.refresh(catalog.Spotify)
			ps := settledProvider(t, rt)
			runsBefore := w.runs.Load()
			rt.mu.Lock()
			armed := ps.retry != nil
			rt.mu.Unlock()
			if !armed {
				t.Fatal("no retry armed after an ordinary rate limit")
			}

			// The quota answer reaches the provider's gate, whose record
			// call is the runtime's only news of it.
			observe := func() { k.fn(time.Now().Add(time.Minute), true) }
			if where == "foreground" {
				observe()
			} else {
				done := make(chan struct{})
				rt.wg.Add(1)
				go func() { defer rt.wg.Done(); observe(); close(done) }() // as the fill's request would
				<-done
			}
			rt.mu.Lock()
			held, retry := ps.held, ps.retry
			rt.mu.Unlock()
			if !held || retry != nil || w.holds.Load() != 1 {
				t.Fatalf("held %t, retry armed %t, worker holds %d; want held, none, the worker paused", held, retry != nil, w.holds.Load())
			}
			if mark, _ := rt.store.QuotaExceeded(rt.ctx, catalog.Spotify); !mark {
				t.Error("the quota mark was not stored")
			}

			// Every background entry is shut: a retry firing anyway, the
			// worker started, a startup.
			syncs := src.count()
			rt.sync(catalog.Spotify)
			rt.runWorker(catalog.Spotify, w)
			rt.startSyncs()
			time.Sleep(20 * time.Millisecond)
			rt.wg.Wait()
			if src.count() != syncs || w.runs.Load() != runsBefore {
				t.Errorf("held: syncs %d -> %d, worker runs %d -> %d; want none", syncs, src.count(), runsBefore, w.runs.Load())
			}
		})
	}
}

// An older successful sync does not clear a newer quota observation: not one
// that arrives during the sync, nor one that arrives between the success and
// the store's clear.
func TestAnOlderSuccessKeepsANewerQuota(t *testing.T) {
	for _, when := range []string{"during the sync", "between the success and the clear"} {
		t.Run(when, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			rt := runtimeAt(t, path)
			if err := rt.store.SetQuotaExceeded(rt.ctx, catalog.Spotify, time.Now().Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			k := &fakeKeeper{}
			held := rt.keepRateLimit(k, rt.store)
			src, w := &duringSource{}, &countingWorker{}
			rt.setSources(source{Source: src, refresh: time.Hour, worker: w, held: held})
			rt.send = func(tea.Msg) {}
			observe := func() {
				time.Sleep(2 * time.Millisecond) // the mark is newer than the sync's start
				k.fn(time.Now().Add(time.Minute), true)
			}
			if when == "during the sync" {
				src.during = observe
			} else {
				beforeQuotaClear = observe
				t.Cleanup(func() { beforeQuotaClear = nil })
			}

			rt.refresh(catalog.Spotify) // the user asks; the sync succeeds
			ps := settledProvider(t, rt)
			rt.mu.Lock()
			held = ps.held
			rt.mu.Unlock()
			mark, err := rt.store.QuotaExceeded(rt.ctx, catalog.Spotify)
			if !held || !mark || err != nil {
				t.Errorf("after the older success: held %t, mark %t (%v); want both kept", held, mark, err)
			}
			if w.runs.Load() != 0 {
				t.Errorf("the worker ran %d times while held", w.runs.Load())
			}
		})
	}
}

// duringSource succeeds, running during inside its fetch.
type duringSource struct{ during func() }

func (*duringSource) Provider() string      { return catalog.Spotify }
func (*duringSource) Collections() []string { return []string{"albums"} }
func (d *duringSource) Fetch(_ context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	if d.during != nil {
		d.during()
	}
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}, nil
}

// pagedSource reads two collections of two pages each. A page is "sent"
// only while the run's context allows it, as the provider's admit does, and
// the first page of the first collection stays outstanding until released.
type pagedSource struct {
	sends, fetches       atomic.Int32
	outstanding, release chan struct{}
}

func (*pagedSource) Provider() string      { return catalog.Spotify }
func (*pagedSource) Collections() []string { return []string{"albums", "artists"} }

func (s *pagedSource) Fetch(ctx context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	s.fetches.Add(1)
	for page := 1; page <= 2; page++ {
		if ctx.Err() != nil {
			return catalog.Snapshot{}, context.Cause(ctx) // refused before sending
		}
		s.sends.Add(1)
		if collection == "albums" && page == 1 && s.outstanding != nil {
			close(s.outstanding)
			<-s.release // the answer is still on its way
		}
	}
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}, nil
}

// A quota observed while a background sync is running fences it: once its
// outstanding request is answered (after a short pause has ended), it sends
// no further page and reads no further collection. A sync the user asked
// for is the way out of a hold and is not fenced.
func TestAQuotaFencesARunningBackgroundSync(t *testing.T) {
	for _, kind := range []string{"background", "asked for"} {
		t.Run(kind, func(t *testing.T) {
			src := &pagedSource{outstanding: make(chan struct{}), release: make(chan struct{})}
			rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
			k := &fakeKeeper{}
			rt.keepRateLimit(k, rt.store)
			rt.setSources(source{Source: src, refresh: time.Hour})
			rt.send = func(tea.Msg) {}
			if kind == "background" {
				rt.sync(catalog.Spotify)
			} else {
				rt.refresh(catalog.Spotify)
			}
			<-src.outstanding
			k.fn(time.Now().Add(5*time.Second), true) // a quota answer elsewhere, a short pause
			close(src.release)                        // the outstanding answer lands
			ps := settledProvider(t, rt)

			wantSends, wantFetches := int32(1), int32(1)
			if kind == "asked for" {
				wantSends, wantFetches = 4, 2
			}
			if src.sends.Load() != wantSends || src.fetches.Load() != wantFetches {
				t.Errorf("sends %d, collections read %d; want %d and %d", src.sends.Load(), src.fetches.Load(), wantSends, wantFetches)
			}
			rt.mu.Lock()
			retry := ps.retry
			rt.mu.Unlock()
			if retry != nil {
				t.Error("a retry was armed after the fenced run")
			}
		})
	}
}

// gatedWorker's first Hold (the one a sync takes as it starts) waits until
// released, so a test can act between a sync's admission and its fence.
// Later Holds (the quota hold's own) return at once: sync.Once would make
// them wait for the first.
type gatedWorker struct {
	countingWorker
	first           atomic.Bool
	entered, resume chan struct{}
}

func (w *gatedWorker) Hold() func() {
	if w.first.CompareAndSwap(false, true) {
		close(w.entered)
		<-w.resume
	}
	return w.countingWorker.Hold()
}

// A quota observed after a background sync was admitted but before it
// installed its fence (nothing to cancel yet) still stops it: it reads and
// sends nothing, and the hold stays.
func TestAQuotaBeforeTheFenceStopsTheBackgroundRun(t *testing.T) {
	src := &pagedSource{}
	w := &gatedWorker{entered: make(chan struct{}), resume: make(chan struct{})}
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	k := &fakeKeeper{}
	rt.keepRateLimit(k, rt.store)
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
	rt.send = func(tea.Msg) {}

	rt.sync(catalog.Spotify) // admitted: not held yet
	<-w.entered              // its goroutine has not installed its fence
	k.fn(time.Now().Add(5*time.Second), true)
	close(w.resume)
	ps := settledProvider(t, rt)

	rt.mu.Lock()
	held, retry := ps.held, ps.retry
	rt.mu.Unlock()
	if src.fetches.Load() != 0 || src.sends.Load() != 0 {
		t.Errorf("the run read %d collections, sent %d pages; want none", src.fetches.Load(), src.sends.Load())
	}
	if !held || retry != nil {
		t.Errorf("held %t, retry armed %t; want held and none", held, retry != nil)
	}
	if mark, _ := rt.store.QuotaExceeded(rt.ctx, catalog.Spotify); !mark {
		t.Error("the quota mark is gone")
	}
}

// holdingSource succeeds, holding the provider (as a quota restored or
// observed without a new count would) while it fetches.
type holdingSource struct {
	rt *catalogRuntime
}

func (*holdingSource) Provider() string      { return catalog.Spotify }
func (*holdingSource) Collections() []string { return []string{"albums"} }
func (h *holdingSource) Fetch(_ context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	h.rt.mu.Lock()
	h.rt.providers[catalog.Spotify].held = true
	h.rt.mu.Unlock()
	return catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}, nil
}

// Only a run the user asked for releases a hold: a background run that
// succeeds while the provider is held leaves it held.
func TestABackgroundSuccessNeverClearsAHold(t *testing.T) {
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	src := &holdingSource{rt: rt}
	rt.setSources(source{Source: src, refresh: time.Hour})
	rt.send = func(tea.Msg) {}
	rt.sync(catalog.Spotify)
	ps := settledProvider(t, rt)
	rt.mu.Lock()
	held := ps.held
	rt.mu.Unlock()
	if !held {
		t.Error("a background success released the hold")
	}
}

// pagedAlbums is an album source whose albums take two pages. Each page is
// sent only if ctx is live, as admit does; the timed gate is left out, as
// if a short pause had already ended. outstanding closes when the first
// background page is sent, which then waits for release.
type pagedAlbums struct {
	sends, foreground    atomic.Int32
	outstanding, release chan struct{}
}

func (*pagedAlbums) Provider() string { return catalog.Spotify }

func (s *pagedAlbums) AlbumTracks(ctx context.Context, _ catalog.Ref) ([]catalog.TrackRecord, error) {
	for range 2 {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		s.foreground.Add(1)
	}
	return nil, nil
}

func (s *pagedAlbums) AlbumTracksOnce(ctx context.Context, _ catalog.Ref) ([]catalog.TrackRecord, error) {
	for page := 1; page <= 2; page++ {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx) // refused before sending
		}
		s.sends.Add(1)
		if page == 1 && s.outstanding != nil {
			close(s.outstanding)
			<-s.release // the answer is still on its way
		}
	}
	return nil, nil
}

// oneAlbum is an album store with one uncached album.
type oneAlbum struct {
	mu     sync.Mutex
	cached bool
}

var theAlbum = catalog.Album{ID: 1, Title: "a", Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: "a"}}

func (s *oneAlbum) UncachedAlbums(context.Context, string) ([]catalog.Album, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached {
		return nil, nil
	}
	return []catalog.Album{theAlbum}, nil
}

func (s *oneAlbum) CacheAlbumTracks(context.Context, catalog.Ref, []catalog.TrackRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached = true
	return nil
}

func (s *oneAlbum) AlbumTracksByRef(context.Context, catalog.Ref) ([]catalog.Track, bool, error) {
	return nil, false, nil
}

// startedWorker lets a test act after runWorker has handed the worker its
// context and before the worker sends anything.
type startedWorker struct {
	*catalogsync.Filler
	entered, resume chan struct{}
}

func (w *startedWorker) Run(ctx context.Context) error {
	close(w.entered)
	<-w.resume
	return w.Filler.Run(ctx)
}

// workersStopped waits for the runtime's goroutines, failing if a worker
// is still running (as a held fill paused mid-run would be).
func workersStopped(t *testing.T, rt *catalogRuntime) {
	t.Helper()
	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the background fill did not stop")
	}
}

// fillRuntime is a runtime whose Spotify worker is a filler over src and
// store.
func fillRuntime(t *testing.T, src *pagedAlbums, store *oneAlbum, wrap func(*catalogsync.Filler) worker) (*catalogRuntime, *fakeKeeper, *catalogsync.Filler) {
	t.Helper()
	f := catalogsync.NewFiller(store, src, catalogsync.Pacing{Delay: time.Millisecond, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond, MaxFailures: 3})
	var w worker = f
	if wrap != nil {
		w = wrap(f)
	}
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	k := &fakeKeeper{}
	rt.keepRateLimit(k, rt.store)
	rt.setSources(source{Source: &quotaSource{}, refresh: time.Hour, worker: w})
	rt.send = func(tea.Msg) {}
	return rt, k, f
}

// A quota observed while the background fill has an album page outstanding
// stops the fill there: once that page is answered (after a short pause has
// ended), the album's next page is not sent. A quota observed after the
// worker was handed its context but before it sent anything stops it too.
// After the hold is released, the next run gets a live context again.
func TestAQuotaStopsTheBackgroundFillMidAlbum(t *testing.T) {
	t.Run("page outstanding", func(t *testing.T) {
		src := &pagedAlbums{outstanding: make(chan struct{}), release: make(chan struct{})}
		rt, k, _ := fillRuntime(t, src, &oneAlbum{}, nil)
		rt.runWorker(catalog.Spotify, rt.providers[catalog.Spotify].worker)
		<-src.outstanding
		k.fn(time.Now().Add(5*time.Second), true) // a quota answer elsewhere, a short pause
		close(src.release)                        // page 1's answer lands
		workersStopped(t, rt)
		if n := src.sends.Load(); n != 1 {
			t.Errorf("the fill sent %d pages; want 1 (none after the quota)", n)
		}
	})
	t.Run("before the first send", func(t *testing.T) {
		src := &pagedAlbums{}
		entered, resume := make(chan struct{}), make(chan struct{})
		rt, k, _ := fillRuntime(t, src, &oneAlbum{}, func(f *catalogsync.Filler) worker {
			return &startedWorker{Filler: f, entered: entered, resume: resume}
		})
		rt.runWorker(catalog.Spotify, rt.providers[catalog.Spotify].worker)
		<-entered // the worker has its context and has sent nothing
		k.fn(time.Now().Add(5*time.Second), true)
		close(resume)
		workersStopped(t, rt)
		if n := src.sends.Load(); n != 0 {
			t.Errorf("the fill sent %d pages; want none", n)
		}
	})
	t.Run("after the hold is released", func(t *testing.T) {
		src, store := &pagedAlbums{}, &oneAlbum{}
		rt, k, _ := fillRuntime(t, src, store, nil)
		ps := rt.providers[catalog.Spotify]
		rt.runWorker(catalog.Spotify, ps.worker) // a run before the hold
		workersStopped(t, rt)
		k.fn(time.Now().Add(5*time.Second), true)
		rt.mu.Lock()
		rt.releaseHoldLocked(ps) // as a successful sync the user asked for does
		rt.mu.Unlock()
		store.mu.Lock()
		store.cached = false // another album to fill
		store.mu.Unlock()
		rt.runWorker(catalog.Spotify, ps.worker)
		workersStopped(t, rt)
		if n := src.sends.Load(); n != 4 {
			t.Errorf("the fill sent %d pages in two runs around a released hold; want 4", n)
		}
	})
}

// An album the user opens while the provider is quota-held is still
// fetched: the hold stops background work only. (The timed gate still
// applies to it, in the provider.)
func TestAForegroundAlbumOpenRunsWhileHeld(t *testing.T) {
	src := &pagedAlbums{}
	rt, k, f := fillRuntime(t, src, &oneAlbum{}, nil)
	k.fn(time.Now().Add(5*time.Second), true)
	if !rt.providers[catalog.Spotify].held {
		t.Fatal("not held")
	}
	if _, err := f.FetchAlbumTracks(context.Background(), theAlbum); err != nil {
		t.Fatalf("FetchAlbumTracks: %v", err)
	}
	if n := src.foreground.Load(); n != 2 || src.sends.Load() != 0 {
		t.Errorf("foreground pages %d, background pages %d; want 2 and 0", n, src.sends.Load())
	}
}
