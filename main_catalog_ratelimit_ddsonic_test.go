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
	if held := rt.keepRateLimit(k); held || len(k.restored()) != 0 {
		t.Fatalf("a fresh catalog: held %t, restored %v; want neither", held, k.restored())
	}
	k.fn(now.Add(30*time.Second), false) // a short block
	if got, err := rt.store.RateLimitedUntil(rt.ctx, catalog.Spotify); err != nil || !sameMilli(got, now.Add(30*time.Second)) {
		t.Fatalf("recorded %v, %v; want the short block", got, err)
	}
	rt.close()

	rt = runtimeAt(t, path) // restart
	k = &fakeKeeper{}
	if held := rt.keepRateLimit(k); held {
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
	if held := rt.keepRateLimit(k); !held {
		t.Error("the quota mark was not restored")
	}
	if r := k.restored(); len(r) != 1 || !sameMilli(r[0].until, now.Add(90*time.Second)) || !r[0].quota {
		t.Errorf("restored %v, want the 90s quota block", r)
	}
}

// A catalog that cannot be read closes the gate for the floor and holds the
// quota; one that cannot be written leaves the provider's gate as it is.
func TestRateLimitStoreFailuresStayClosed(t *testing.T) {
	rt := runtimeAt(t, filepath.Join(t.TempDir(), "library.db"))
	if err := rt.store.Close(); err != nil {
		t.Fatal(err)
	}
	k := &fakeKeeper{}
	before := time.Now()
	held := rt.keepRateLimit(k)
	r := k.restored()
	if !held || len(r) != 1 || !r[0].quota ||
		r[0].until.Before(before.Add(spotify.MinCooldown)) || r[0].until.After(time.Now().Add(spotify.MinCooldown)) {
		t.Fatalf("an unreadable catalog: held %t, restored %v; want held and a %v block", held, r, spotify.MinCooldown)
	}
	k.fn(time.Now().Add(time.Hour), true) // the write fails: logged only
	if got := k.restored(); len(got) != 1 {
		t.Errorf("a failed write touched the gate: %v", got)
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

// countingWorker counts its runs.
type countingWorker struct{ runs atomic.Int32 }

func (*countingWorker) Hold() func() { return func() {} }
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

var quotaErr = fmt.Errorf("sync spotify/albums: %w", &catalog.RateLimitError{RetryAfter: time.Millisecond, Quota: true})

// An exhausted quota holds every automatic Spotify sync and the worker: no
// retry is armed, the startup of a later run syncs nothing, and only a sync
// the user asks for runs; when it succeeds the hold and its mark are gone.
func TestAnExhaustedQuotaHoldsAutomaticWork(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	src, w := &quotaSource{}, &countingWorker{}
	rt := runtimeAt(t, path)
	rt.setSources(source{Source: src, refresh: time.Hour, worker: w})
	rt.send = func(tea.Msg) {}

	src.set(quotaErr)
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
		{quotaErr, true},
		{errors.Join(errDown, ordinary, quotaErr), true},
		{fmt.Errorf("outer: %w", errors.Join(ordinary, quotaErr)), true},
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
