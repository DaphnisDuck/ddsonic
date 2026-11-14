package main

// ddsonic: quitting does not lose a Spotify rate limit or quota mark that a
// request in progress records: the writes do not end with the runtime's
// context, and the store stays open until the requests in progress, the
// user's included, have recorded what they got.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// pausedLimits is the rate-limit store with one kind of write paused until
// released, and the context each write got kept.
type pausedLimits struct {
	rateLimitStore
	pauseQuota bool          // pause SetQuotaExceeded, else SetRateLimitedUntil
	paused     chan struct{} // closed when the paused write has begun
	release    chan struct{}
	once       sync.Once

	mu   sync.Mutex
	errs []error // each write's context's state when it wrote; nil: live
	open []bool  // each write's context had a deadline
}

func newPausedLimits(st rateLimitStore, pauseQuota bool) *pausedLimits {
	return &pausedLimits{rateLimitStore: st, pauseQuota: pauseQuota,
		paused: make(chan struct{}), release: make(chan struct{})}
}

func (p *pausedLimits) write(ctx context.Context, pause bool) {
	if pause {
		p.once.Do(func() { close(p.paused); <-p.release })
	}
	_, deadline := ctx.Deadline()
	p.mu.Lock()
	p.errs = append(p.errs, ctx.Err())
	p.open = append(p.open, deadline)
	p.mu.Unlock()
}

func (p *pausedLimits) SetRateLimitedUntil(ctx context.Context, provider string, until time.Time) error {
	p.write(ctx, !p.pauseQuota)
	return p.rateLimitStore.SetRateLimitedUntil(ctx, provider, until)
}

func (p *pausedLimits) SetQuotaExceeded(ctx context.Context, provider string, at time.Time) error {
	p.write(ctx, p.pauseQuota)
	return p.rateLimitStore.SetQuotaExceeded(ctx, provider, at)
}

// quitting starts rt.close and returns once quitting has cancelled the
// runtime's context and stopped Spotify's requests; the channel is closed
// when close returns.
func quitting(t *testing.T, rt *catalogRuntime, k *fakeKeeper) <-chan struct{} {
	t.Helper()
	closed := make(chan struct{})
	go func() { rt.close(); close(closed) }()
	for _, ch := range []<-chan struct{}{rt.ctx.Done(), k.whenStopped()} {
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatal("quitting did not begin")
		}
	}
	return closed
}

// within waits for ch, failing the test after 10 seconds.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not happen", what)
	}
}

// restarted opens the catalog at path again and reports what keepRateLimit
// restores from it.
func restarted(t *testing.T, path string) (held bool, restored []keeperSet) {
	t.Helper()
	rt := runtimeAt(t, path)
	k := &fakeKeeper{}
	held = rt.keepRateLimit(k, rt.store)
	return held, k.restored()
}

// A rate limit or quota mark being written as quitting begins is written:
// its context is not the runtime's, and the store is closed only after it.
func TestARateLimitBeingWrittenAtQuitIsKept(t *testing.T) {
	for _, tc := range []struct {
		name  string
		quota bool
	}{
		{"cooldown", false},
		{"quota", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			rt := runtimeAt(t, path)
			st := newPausedLimits(rt.store, tc.quota)
			k := &fakeKeeper{}
			rt.keepRateLimit(k, st)
			until := time.Now().Add(time.Hour)

			if !k.begin() {
				t.Fatal("a request was refused before quitting")
			}
			answered := make(chan struct{})
			go func() { k.fn(until, tc.quota); k.end(); close(answered) }() // a foreground 429
			within(t, st.paused, "the write")
			closed := quitting(t, rt, k)
			select {
			case <-closed:
				t.Fatal("quitting closed the catalog while a rate limit was being written")
			default:
			}
			close(st.release)
			within(t, answered, "the answer's end")
			within(t, closed, "quitting")

			st.mu.Lock()
			for i, err := range st.errs {
				if err != nil {
					t.Errorf("write %d was ended with the runtime: %v", i, err)
				}
				if !st.open[i] {
					t.Errorf("write %d had no deadline", i)
				}
			}
			st.mu.Unlock()

			held, r := restarted(t, path)
			if len(r) != 1 || !sameMilli(r[0].until, until) || held != tc.quota {
				t.Errorf("after a restart: held %t, restored %v; want held %t and the block to %v", held, r, tc.quota, until)
			}
		})
	}
}

// A 429 the user's request gets after quitting began, while its answer was
// on its way, is recorded: quitting waits for that request, and closes the
// store after its write. The block is recorded as the provider hands it
// on: first the header's, then (a quota body) the same block as a quota one.
func TestALateForegroundAnswerAtQuitIsKept(t *testing.T) {
	for _, tc := range []struct {
		name string
		// before is how much of the answer was recorded before quitting
		// began: nothing, or the header's block (the quota still unread).
		before bool
	}{
		{"whole answer late", false},
		{"quota read late", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "library.db")
			rt := runtimeAt(t, path)
			k := &fakeKeeper{}
			rt.keepRateLimit(k, rt.store)
			until := time.Now().Add(time.Hour)

			if !k.begin() { // the user's request is on its way
				t.Fatal("a request was refused before quitting")
			}
			if tc.before {
				k.fn(until, false)
			}
			closed := quitting(t, rt, k)
			if k.begin() {
				t.Error("a request began after quitting stopped them")
				k.end()
			}
			// Give a close that did not wait the time to close the store.
			select {
			case <-closed:
				t.Fatal("quitting closed the catalog with a request in progress")
			case <-time.After(100 * time.Millisecond):
			}
			if !tc.before {
				k.fn(until, false)
			}
			k.fn(until, true)
			k.end()
			within(t, closed, "quitting")

			held, r := restarted(t, path)
			if !held || len(r) != 1 || !sameMilli(r[0].until, until) || !r[0].quota {
				t.Errorf("after a restart: held %t, restored %v; want the quota block to %v held", held, r, until)
			}
		})
	}
}

// Quitting waits for a request no longer than catalogStopTimeout. It then
// leaves the store open, so a request still in progress can record its
// block until the process exits.
func TestQuittingWaitsForARequestOnlySoLong(t *testing.T) {
	defer func(d time.Duration) { catalogStopTimeout = d }(catalogStopTimeout)
	catalogStopTimeout = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "library.db")
	rt := runtimeAt(t, path)
	k := &fakeKeeper{}
	rt.keepRateLimit(k, rt.store)

	if !k.begin() {
		t.Fatal("a request was refused before quitting")
	}
	var once sync.Once
	finish := func() { once.Do(k.end) } // the request ends, also when the test fails
	t.Cleanup(finish)
	select {
	case <-quitting(t, rt, k):
	case <-time.After(5 * time.Second):
		t.Fatal("quitting waited past its bound")
	}
	until := time.Now().Add(time.Hour)
	k.fn(until, true) // answered after quitting gave up waiting
	finish()
	if got, err := rt.store.RateLimitedUntil(context.Background(), catalog.Spotify); err != nil || !sameMilli(got, until) {
		t.Errorf("recorded after the bound: %v, %v; want the block (the store left open)", got, err)
	}
	rt.close() // the test's own cleanup: nothing in progress now
}

// timedLimits fails each write of a block the way a stuck store would: it
// waits for its context to end. Quota marks are written.
type timedLimits struct {
	rateLimitStore
	err chan error
}

func (s *timedLimits) SetRateLimitedUntil(ctx context.Context, _ string, _ time.Time) error {
	<-ctx.Done()
	s.err <- ctx.Err()
	return ctx.Err()
}

// A write that does not finish ends at its own bound, not when quitting
// cancels the runtime, and the writes after it still run.
func TestARateLimitWriteAtQuitEndsAtItsBound(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for holdWriteTimeout")
	}
	path := filepath.Join(t.TempDir(), "library.db")
	rt := runtimeAt(t, path)
	st := &timedLimits{rateLimitStore: rt.store, err: make(chan error, 1)}
	k := &fakeKeeper{}
	rt.keepRateLimit(k, st)

	if !k.begin() {
		t.Fatal("a request was refused before quitting")
	}
	answered := make(chan struct{})
	start := time.Now()
	go func() { k.fn(time.Now().Add(time.Hour), true); k.end(); close(answered) }()
	closed := quitting(t, rt, k)
	var err error
	select {
	case err = <-st.err:
	case <-time.After(10 * time.Second):
		t.Fatal("the stuck write never ended")
	}
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < holdWriteTimeout {
		t.Errorf("the stuck write ended with %v after %v, want its own deadline of %v", err, time.Since(start), holdWriteTimeout)
	}
	within(t, answered, "the answer's end")
	within(t, closed, "quitting")
	if held, _ := restarted(t, path); !held {
		t.Error("the quota mark after the stuck write was not kept")
	}
}

// Writes that fail at quit are logged, and quitting completes.
func TestARateLimitWriteFailingAtQuit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	rt := runtimeAt(t, path)
	k := &fakeKeeper{}
	rt.keepRateLimit(k, &flakyLimits{rateLimitStore: rt.store})
	f := rt.limits.(*flakyLimits)
	f.failWrites = true

	if !k.begin() {
		t.Fatal("a request was refused before quitting")
	}
	closed := quitting(t, rt, k)
	k.fn(time.Now().Add(time.Hour), true)
	k.end()
	within(t, closed, "quitting")
	if held, r := restarted(t, path); held || len(r) != 0 {
		t.Errorf("held %t, restored %v after failed writes; want neither (nothing stored)", held, r)
	}
}

// Quitting stops Spotify's requests at once, not after the syncs have
// stopped: nothing new is sent while a sync is still ending.
func TestQuittingStopsRequestsWhileASyncEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	rt := runtimeAt(t, path)
	k := &fakeKeeper{}
	rt.keepRateLimit(k, rt.store)

	rt.wg.Add(1) // a sync still ending
	var once sync.Once
	ended := func() { once.Do(rt.wg.Done) }
	t.Cleanup(ended)
	closed := quitting(t, rt, k)
	if k.begin() {
		t.Error("a request began while a sync was still ending")
		k.end()
	}
	ended()
	within(t, closed, "quitting")
}
