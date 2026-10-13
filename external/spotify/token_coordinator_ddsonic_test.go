package spotify

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/playlist"
)

// coordKit stands in for the token endpoint and the credential file under a
// coordinator, and can hold either.
type coordKit struct {
	t *testing.T
	// refreshes counts token requests; answer says what request n, made
	// with refresh token rt, gets. It runs on the flight's goroutine and may
	// block.
	refreshes atomic.Int32
	answer    func(n int, rt string) (*oauth2.Token, error)
	// writes counts file writes; failWrites makes the next ones fail;
	// beforeWrite runs inside each, before the file is touched.
	writes      atomic.Int32
	failWrites  atomic.Int32
	beforeWrite func()
	path        string
}

var errRevoked error = &oauth2.RetrieveError{ErrorCode: "invalid_grant"}

func newCoordKit(t *testing.T) *coordKit {
	t.Helper()
	k := &coordKit{t: t, path: storeCreds(t)}
	k.answer = func(n int, _ string) (*oauth2.Token, error) { return rotated(n), nil }
	origRefresh, origWrite := refreshTokens, writeCreds
	refreshTokens = func(_ context.Context, _, rt string) (*oauth2.Token, error) {
		return k.answer(int(k.refreshes.Add(1)), rt)
	}
	writeCreds = func(c *storedCreds) error {
		k.writes.Add(1)
		if k.beforeWrite != nil {
			k.beforeWrite()
		}
		if k.failWrites.Load() > 0 {
			k.failWrites.Add(-1)
			return errors.New("disk full")
		}
		return origWrite(c)
	}
	t.Cleanup(func() {
		refreshTokens, writeCreds = origRefresh, origWrite
		flightJoined, durableSnapshot, closeWaiting = nil, nil, nil
		credStore.mu.Lock()
		credStore.owner = nil
		credStore.mu.Unlock()
	})
	return k
}

// rotated is the n-th refresh's answer: a new token and a new refresh token.
func rotated(n int) *oauth2.Token {
	return &oauth2.Token{AccessToken: "access-" + string(rune('0'+n)), RefreshToken: "refresh-" + string(rune('0'+n)), Expiry: time.Now().Add(time.Hour)}
}

// coordinator is an accepted sign-in's coordinator with a seed token.
func (k *coordKit) coordinator(refresh string) *tokenCoordinator {
	c := newTokenCoordinator("client", storedCreds{Username: "u", Data: []byte("d"), DeviceID: "dev", RefreshToken: refresh},
		&oauth2.Token{AccessToken: "seed", Expiry: time.Now().Add(time.Hour)})
	k.t.Cleanup(c.close)
	return c
}

// stored is the refresh token in the credential file.
func (k *coordKit) stored() string {
	k.t.Helper()
	creds, err := loadCreds()
	if err != nil {
		k.t.Fatal(err)
	}
	return creds.RefreshToken
}

// gate is a barrier: wait blocks until open is called, and entered is closed
// when the first waiter has arrived.
type gate struct {
	entered, release chan struct{}
	once             sync.Once
}

func newGate() *gate { return &gate{entered: make(chan struct{}), release: make(chan struct{})} }
func (g *gate) wait() {
	g.once.Do(func() { close(g.entered) })
	<-g.release
}
func (g *gate) open() { close(g.release) }

// onceFunc is fn, run by its first call only.
func onceFunc(fn func()) func() {
	var once sync.Once
	return func() { once.Do(fn) }
}

func arrive(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func revOf(c *tokenCoordinator) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rev
}

// Every token that is published is a new revision: the seed, a refresh on
// expiry and a forced renewal alike. A refused or failed refresh is none.
func TestEveryPublicationAdvancesRevision(t *testing.T) {
	k := newCoordKit(t)
	c := k.coordinator("old")
	claimCreds(c, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	if rev := revOf(c); rev != 1 {
		t.Fatalf("the seed is revision %d, want 1", rev)
	}
	l, _, err := c.token(context.Background())
	if err != nil || l.rev != 1 || k.refreshes.Load() != 0 {
		t.Fatalf("a usable token was not handed out as it was: lease %d, %v, %d refreshes", l.rev, err, k.refreshes.Load())
	}

	now = now.Add(2 * time.Hour) // the seed has expired
	l, access, err := c.token(context.Background())
	if err != nil || l.rev != 2 || access != "access-1" {
		t.Fatalf("the refresh on expiry gave lease %d, %q, %v, want revision 2", l.rev, access, err)
	}
	now = time.Now()
	l, _, err = c.renew(context.Background(), l) // forced: the token is usable, and renewed anyway
	if err != nil || l.rev != 3 || k.refreshes.Load() != 2 {
		t.Fatalf("the forced renewal gave lease %d, %v, after %d refreshes, want revision 3 after 2", l.rev, err, k.refreshes.Load())
	}
	if got := k.stored(); got != "refresh-2" {
		t.Errorf("the file holds %q, want the last rotated refresh token", got)
	}

	k.answer = func(int, string) (*oauth2.Token, error) { return nil, errors.New("503") }
	if _, _, err := c.renew(context.Background(), l); err == nil || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("an outage is %v, want an error that asks for no sign-in", err)
	}
	k.answer = func(int, string) (*oauth2.Token, error) { return nil, errRevoked }
	if _, _, err := c.renew(context.Background(), l); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("a refused refresh is %v, want playlist.ErrNeedsAuth", err)
	}
	if rev := revOf(c); rev != 3 {
		t.Errorf("a failed and a refused refresh moved the revision to %d", rev)
	}
	if got := k.stored(); got != "refresh-2" {
		t.Errorf("a refused refresh changed the file to %q", got)
	}
}

// Two callers that need a newer token at once share one refresh, whichever
// asked first and whatever it answers.
func TestConcurrentRenewalsShareOneFlight(t *testing.T) {
	for _, outcome := range []string{"success", "invalid_grant", "outage"} {
		t.Run(outcome, func(t *testing.T) {
			k := newCoordKit(t)
			c := k.coordinator("old")
			claimCreds(c, nil)
			held := newGate()
			k.answer = func(n int, _ string) (*oauth2.Token, error) {
				held.wait()
				switch outcome {
				case "invalid_grant":
					return nil, errRevoked
				case "outage":
					return nil, errors.New("503")
				}
				return rotated(n), nil
			}
			joined := make(chan struct{})
			flightJoined = onceFunc(func() { close(joined) })
			type result struct {
				l   lease
				err error
			}
			first, second := make(chan result, 1), make(chan result, 1)
			go func() { l, _, err := c.renew(context.Background(), lease{c, 1}); first <- result{l, err} }()
			arrive(t, "the first refresh", held.entered)
			go func() { l, _, err := c.renew(context.Background(), lease{c, 1}); second <- result{l, err} }()
			arrive(t, "the second caller to join", joined)
			if n := k.refreshes.Load(); n != 1 {
				t.Fatalf("%d refreshes are in the air, want the one both share", n)
			}
			held.open()
			a, b := <-first, <-second
			if k.refreshes.Load() != 1 {
				t.Errorf("%d refreshes were made, want 1", k.refreshes.Load())
			}
			switch outcome {
			case "success":
				if a.err != nil || b.err != nil || a.l.rev != 2 || b.l.rev != 2 {
					t.Errorf("the callers got leases %d (%v) and %d (%v), want revision 2 for both", a.l.rev, a.err, b.l.rev, b.err)
				}
				if got := k.stored(); got != "refresh-1" {
					t.Errorf("the file holds %q, want the one answer's refresh token", got)
				}
			case "invalid_grant":
				if !errors.Is(a.err, playlist.ErrNeedsAuth) || !errors.Is(b.err, playlist.ErrNeedsAuth) || !c.needsSignIn() {
					t.Errorf("the callers got %v and %v, needs sign-in %t; want the refusal for both", a.err, b.err, c.needsSignIn())
				}
			case "outage":
				if a.err == nil || b.err == nil || errors.Is(a.err, playlist.ErrNeedsAuth) || c.needsSignIn() {
					t.Errorf("the callers got %v and %v, needs sign-in %t; want an outage that refuses nothing", a.err, b.err, c.needsSignIn())
				}
			}
			if outcome != "success" && k.stored() != "old" {
				t.Errorf("the file was changed to %q by a refresh that produced no token", k.stored())
			}
		})
	}
}

// A caller whose token has been renewed since it was handed out gets the
// current one, and Spotify is not asked.
func TestRenewAfterNewerLeaseSendsNothing(t *testing.T) {
	k := newCoordKit(t)
	c := k.coordinator("old")
	l2, _, err := c.renew(context.Background(), lease{c, 1})
	if err != nil {
		t.Fatal(err)
	}
	l, _, err := c.renew(context.Background(), lease{c, 1}) // still holding the first lease
	if err != nil || l != l2 || k.refreshes.Load() != 1 {
		t.Errorf("a caller with an old lease got revision %d (%v) after %d refreshes, want the current %d after 1", l.rev, err, k.refreshes.Load(), l2.rev)
	}
}

// A refusal is about one token. It is remembered, so Spotify is not asked
// again; one about a token that has been replaced changes nothing; and a
// refresh that was already in the air when it was recorded supersedes it.
func TestRefusalsBelongToALease(t *testing.T) {
	t.Run("remembered", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("old")
		k.answer = func(int, string) (*oauth2.Token, error) { return nil, errRevoked }
		if _, _, err := c.renew(context.Background(), lease{c, 1}); !errors.Is(err, playlist.ErrNeedsAuth) {
			t.Fatalf("renew() = %v, want the refusal", err)
		}
		for range 3 {
			if _, _, err := c.token(context.Background()); !errors.Is(err, playlist.ErrNeedsAuth) {
				t.Errorf("token() = %v after a refusal, want playlist.ErrNeedsAuth", err)
			}
		}
		if n := k.refreshes.Load(); n != 1 {
			t.Errorf("Spotify was asked %d times, want once", n)
		}
	})
	t.Run("stale", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("old")
		old := lease{c, 1}
		if _, _, err := c.renew(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		if c.refuse(old) || c.needsSignIn() {
			t.Error("a refusal of a replaced token was taken for the current one")
		}
		if !c.refuse(lease{c, 2}) || !c.needsSignIn() {
			t.Error("a refusal of the current token was not recorded")
		}
	})
	t.Run("superseded by a refresh in the air", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("old")
		held := newGate()
		k.answer = func(n int, _ string) (*oauth2.Token, error) { held.wait(); return rotated(n), nil }
		done := make(chan error, 1)
		go func() { _, _, err := c.renew(context.Background(), lease{c, 1}); done <- err }()
		arrive(t, "the refresh", held.entered)
		if !c.refuse(lease{c, 1}) {
			t.Fatal("the current token's refusal was not recorded")
		}
		held.open()
		if err := <-done; err != nil {
			t.Fatalf("the refresh in the air failed: %v", err)
		}
		if c.needsSignIn() {
			t.Error("the refusal of the old token outlived the token that replaced it")
		}
	})
}

// A caller that stops waiting does not cancel the refresh: its answer may
// carry a rotated refresh token, and is settled and written with no waiter.
func TestFlightOutlivesCancelledCaller(t *testing.T) {
	k := newCoordKit(t)
	c := k.coordinator("old")
	claimCreds(c, nil)
	held := newGate()
	k.answer = func(n int, _ string) (*oauth2.Token, error) { held.wait(); return rotated(n), nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, _, err := c.renew(ctx, lease{c, 1}); done <- err }()
	arrive(t, "the refresh", held.entered)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled caller got %v", err)
	}
	held.open()
	l, _, err := c.renew(context.Background(), lease{c, 1}) // joins it, or finds it done
	if err != nil || l.rev != 2 || k.refreshes.Load() != 1 {
		t.Errorf("after the caller gave up: lease %d, %v, %d refreshes; want revision 2 from the one refresh", l.rev, err, k.refreshes.Load())
	}
	if got := k.stored(); got != "refresh-1" {
		t.Errorf("the file holds %q, want the abandoned refresh's token", got)
	}
}

// A coordinator that was closed, or replaced by a new sign-in, while its
// refresh was in the air takes nothing from the answer: no token, no refusal
// and no write.
func TestClosedCoordinatorCommitsNothing(t *testing.T) {
	for _, how := range []string{"closed", "replaced"} {
		for _, outcome := range []string{"success", "invalid_grant"} {
			t.Run(how+"/"+outcome, func(t *testing.T) {
				k := newCoordKit(t)
				c := k.coordinator("old")
				claimCreds(c, nil)
				held := newGate()
				k.answer = func(n int, _ string) (*oauth2.Token, error) {
					held.wait()
					if outcome == "invalid_grant" {
						return nil, errRevoked
					}
					return rotated(n), nil
				}
				done := make(chan error, 1)
				go func() { _, _, err := c.renew(context.Background(), lease{c, 1}); done <- err }()
				arrive(t, "the refresh", held.entered)
				want := "old"
				if how == "closed" {
					c.close()
				} else {
					next := k.coordinator("new-sign-in")
					claimCreds(next, c)
					want = "new-sign-in"
				}
				writes := k.writes.Load()
				held.open()
				if err := <-done; !errors.Is(err, errSuperseded) {
					t.Errorf("the waiter got %v, want to be told the sign-in was replaced", err)
				}
				if got := k.stored(); got != want || k.writes.Load() != writes {
					t.Errorf("the file holds %q after %d more writes, want %q and none", got, k.writes.Load()-writes, want)
				}
				if c.refused || revOf(c) != 1 {
					t.Errorf("the late answer changed the closed coordinator: refused %t, revision %d", c.refused, revOf(c))
				}
			})
		}
	}
}

// Acceptance, the file write and closing have one boundary. A close that
// arrives while an accepted credential is being written waits for the write;
// one that arrives after the answer was accepted finds it written; one that
// arrives first leaves the answer unaccepted.
func TestCloseAndTheDurableSection(t *testing.T) {
	// rotate starts a renewal whose answer is a rotation and returns once
	// it has finished.
	rotate := func(c *tokenCoordinator) chan error {
		done := make(chan error, 1)
		go func() { _, _, err := c.renew(context.Background(), lease{c, 1}); done <- err }()
		return done
	}
	for _, how := range []string{"close", "replacement"} {
		// end closes c, or replaces it by a new sign-in, as the case says.
		end := func(k *coordKit, c *tokenCoordinator) (wantFile string) {
			if how == "close" {
				c.close()
				return "refresh-1"
			}
			claimCreds(k.coordinator("new-sign-in"), c)
			return "new-sign-in"
		}
		t.Run(how+" during the write", func(t *testing.T) {
			k := newCoordKit(t)
			c := k.coordinator("old")
			claimCreds(c, nil)
			writing, parked := newGate(), make(chan struct{})
			k.beforeWrite = func() {
				if k.writes.Load() == 2 { // the rotation's write
					writing.wait()
				}
			}
			closeWaiting = onceFunc(func() { close(parked) })
			done := rotate(c)
			arrive(t, "the rotation's write", writing.entered)
			ended := make(chan string, 1)
			go func() { ended <- end(k, c) }()
			arrive(t, "the close to reach the file's lock", parked)
			select {
			case <-ended:
				t.Fatal("the close finished while the write was in progress")
			case <-time.After(20 * time.Millisecond):
			}
			if c.closed {
				t.Fatal("the coordinator was marked closed while its write was in progress")
			}
			writing.open()
			if err := <-done; err != nil {
				t.Fatalf("the renewal failed: %v", err)
			}
			if want := <-ended; k.stored() != want {
				t.Errorf("the file holds %q, want %q", k.stored(), want)
			}
			if !c.closed {
				t.Error("the coordinator is not closed")
			}
		})
		t.Run(how+" after acceptance, before the write", func(t *testing.T) {
			k := newCoordKit(t)
			c := k.coordinator("old")
			claimCreds(c, nil)
			accepted, parked := newGate(), make(chan struct{})
			durableSnapshot = func() {
				if revOf(c) == 2 {
					accepted.wait()
				}
			}
			closeWaiting = onceFunc(func() { close(parked) })
			done := rotate(c)
			arrive(t, "the answer to be accepted", accepted.entered)
			ended := make(chan string, 1)
			go func() { ended <- end(k, c) }()
			arrive(t, "the close to reach the file's lock", parked)
			var sawRotation atomic.Bool
			k.beforeWrite = func() {
				if creds, err := loadCreds(); err == nil && creds.RefreshToken == "refresh-1" {
					sawRotation.Store(true)
				}
			}
			accepted.open()
			if err := <-done; err != nil {
				t.Fatalf("the renewal failed: %v", err)
			}
			want := <-ended
			if k.stored() != want {
				t.Errorf("the file holds %q, want %q", k.stored(), want)
			}
			// A close leaves the accepted rotation on disk; a replacement
			// writes over it, after it.
			if how == "replacement" && !sawRotation.Load() {
				t.Error("the accepted rotation was not written before the new sign-in's credential")
			}
		})
	}
	t.Run("close before the answer", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("old")
		claimCreds(c, nil)
		held := newGate()
		k.answer = func(n int, _ string) (*oauth2.Token, error) { held.wait(); return rotated(n), nil }
		done := rotate(c)
		arrive(t, "the refresh", held.entered)
		c.close()
		writes := k.writes.Load()
		held.open()
		<-done
		if k.stored() != "old" || k.writes.Load() != writes {
			t.Errorf("an answer that was never accepted reached the file: %q, %d more writes", k.stored(), k.writes.Load()-writes)
		}
	})
}

// A caller that joined a refresh gets its answer only once the write it
// called for has returned.
func TestJoinedFlightSeesTheWrite(t *testing.T) {
	k := newCoordKit(t)
	c := k.coordinator("old")
	claimCreds(c, nil)
	held := newGate()
	k.answer = func(n int, _ string) (*oauth2.Token, error) { held.wait(); return rotated(n), nil }
	joined := make(chan struct{})
	flightJoined = onceFunc(func() { close(joined) })
	go c.renew(context.Background(), lease{c, 1})
	arrive(t, "the refresh", held.entered)
	got := make(chan string, 1)
	go func() {
		if _, _, err := c.renew(context.Background(), lease{c, 1}); err != nil {
			got <- err.Error()
			return
		}
		creds, _ := loadCreds()
		got <- creds.RefreshToken
	}()
	arrive(t, "the second caller to join", joined)
	held.open()
	if file := <-got; file != "refresh-1" {
		t.Errorf("the joined caller returned with %q in the file, want the rotated refresh token", file)
	}
}

// A write that fails does not fail the token. The credential stays to be
// written, and the next rotation, or closing, writes it.
func TestWriteFailureKeepsTokenAndRetries(t *testing.T) {
	for _, retry := range []string{"next rotation", "close"} {
		t.Run(retry, func(t *testing.T) {
			k := newCoordKit(t)
			c := k.coordinator("old")
			claimCreds(c, nil)
			k.failWrites.Store(1)
			l, access, err := c.renew(context.Background(), lease{c, 1})
			if err != nil || access != "access-1" {
				t.Fatalf("a failed write failed the token: %q, %v", access, err)
			}
			if k.stored() != "old" {
				t.Fatalf("the file holds %q after a failed write", k.stored())
			}
			want := "refresh-1"
			if retry == "close" {
				c.close()
			} else {
				if _, _, err := c.renew(context.Background(), l); err != nil {
					t.Fatal(err)
				}
				want = "refresh-2"
			}
			if got := k.stored(); got != want {
				t.Errorf("the file holds %q, want %q", got, want)
			}
		})
	}
}

// Whose credential the file holds is decided by who claimed it, and nothing
// is compared between two coordinators.
func TestClaimWritesTheOwnersCredential(t *testing.T) {
	t.Run("the first claim writes the seed", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("seed-refresh")
		claimCreds(c, nil)
		if k.stored() != "seed-refresh" || k.writes.Load() != 1 {
			t.Errorf("the file holds %q after %d writes, want the seed after 1", k.stored(), k.writes.Load())
		}
		claimCreds(c, nil) // nothing has changed: nothing is written
		if k.writes.Load() != 1 {
			t.Errorf("a second claim with nothing new wrote the file again")
		}
	})
	t.Run("a new owner with the same history still writes", func(t *testing.T) {
		k := newCoordKit(t)
		a, b := k.coordinator("a-0"), k.coordinator("b-0")
		claimCreds(a, nil)
		for _, c := range []*tokenCoordinator{a, b} { // one rotation each
			if _, _, err := c.renew(context.Background(), lease{c, 1}); err != nil {
				t.Fatal(err)
			}
		}
		writes := k.writes.Load()
		claimCreds(b, a)
		if got := k.stored(); got != "refresh-2" || k.writes.Load() != writes+1 {
			t.Errorf("the file holds %q after %d more writes, want the new owner's credential after 1", got, k.writes.Load()-writes)
		}
	})
	t.Run("an unwritten credential is not written once its owner is replaced", func(t *testing.T) {
		k := newCoordKit(t)
		a := k.coordinator("a-0")
		claimCreds(a, nil)
		k.failWrites.Store(1)
		if _, _, err := a.renew(context.Background(), lease{a, 1}); err != nil {
			t.Fatal(err)
		}
		var seen []string
		k.beforeWrite = func() {} // set below, once the replacement begins
		b := k.coordinator("b-0")
		orig := writeCreds
		writeCreds = func(c *storedCreds) error { seen = append(seen, c.RefreshToken); return orig(c) }
		claimCreds(b, a)
		a.close()
		if got := k.stored(); got != "b-0" || len(seen) != 1 || seen[0] != "b-0" {
			t.Errorf("the file holds %q after writes of %q, want only the new owner's", got, seen)
		}
	})
	t.Run("a coordinator that was never claimed writes nothing", func(t *testing.T) {
		k := newCoordKit(t)
		c := k.coordinator("unclaimed")
		if _, _, err := c.renew(context.Background(), lease{c, 1}); err != nil {
			t.Fatal(err)
		}
		c.setIdentity("someone", []byte("else"), "dev2")
		c.close()
		if k.stored() != "old" || k.writes.Load() != 0 {
			t.Errorf("an unclaimed coordinator changed the file to %q in %d writes", k.stored(), k.writes.Load())
		}
	})
}

// Two changes of one owner's credential are written in order, each as it
// stood when it was made.
func TestCredentialChangesWriteInOrder(t *testing.T) {
	k := newCoordKit(t)
	c := k.coordinator("old")
	claimCreds(c, nil)
	l, _, err := c.renew(context.Background(), lease{c, 1})
	if err != nil {
		t.Fatal(err)
	}
	c.setIdentity("renamed", []byte("blob-2"), "dev")
	if _, _, err := c.renew(context.Background(), l); err != nil {
		t.Fatal(err)
	}
	creds, err := loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	if creds.RefreshToken != "refresh-2" || creds.Username != "renamed" || string(creds.Data) != "blob-2" {
		t.Errorf("the file holds %+v, want the last of each half", creds)
	}
	info, err := os.Stat(k.path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the credential file's mode is %v (%v), want 0600", info.Mode().Perm(), err)
	}
	if k.writes.Load() != 4 {
		t.Errorf("%d writes, want one per change and the claim", k.writes.Load())
	}
}

// A coordinator without a token and without a refresh token asks for
// sign-in; one without a token but with a refresh token gets one.
func TestCoordinatorWithoutAToken(t *testing.T) {
	k := newCoordKit(t)
	bare := newTokenCoordinator("client", storedCreds{}, nil)
	t.Cleanup(bare.close)
	if _, _, err := bare.token(context.Background()); !errors.Is(err, playlist.ErrNeedsAuth) || !strings.Contains(err.Error(), "spotify reset") {
		t.Errorf("token() = %v, want playlist.ErrNeedsAuth saying how to sign in again", err)
	}
	restorable := newTokenCoordinator("client", storedCreds{RefreshToken: "kept"}, nil)
	t.Cleanup(restorable.close)
	if !restorable.needsSignIn() {
		t.Error("a coordinator that never had a token does not ask for sign-in")
	}
	l, _, err := restorable.token(context.Background())
	if err != nil || l.rev != 1 || restorable.needsSignIn() || k.refreshes.Load() != 1 {
		t.Errorf("token() = lease %d, %v after %d refreshes; want the first token from one refresh", l.rev, err, k.refreshes.Load())
	}
}
