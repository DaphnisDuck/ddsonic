package spotify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/playlist"
)

// cancelSignIn cancels the provider's sign-in in flight, as its timeout or a
// quit would, without closing the provider.
func cancelSignIn(t *testing.T, p *SpotifyProvider) {
	t.Helper()
	p.mu.Lock()
	cancel := p.authCancel
	p.mu.Unlock()
	if cancel == nil {
		t.Fatal("no sign-in is in flight")
	}
	cancel()
}

func phaseOf(p *SpotifyProvider) buildPhase {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase
}

// countWrites counts the credential file's writes.
func countWrites(t *testing.T) *atomic.Int32 {
	t.Helper()
	n := new(atomic.Int32)
	orig := writeCreds
	writeCreds = func(c *storedCreds) error { n.Add(1); return orig(c) }
	t.Cleanup(func() { writeCreds = orig })
	return n
}

// hookOnce sets *hook to a function that closes the returned channel the
// first time it runs, and clears it when the test ends.
func hookOnce(t *testing.T, hook *func()) chan struct{} {
	t.Helper()
	reached := make(chan struct{})
	*hook = onceFunc(func() { close(reached) })
	t.Cleanup(func() { *hook = nil })
	return reached
}

// Startup and Authenticate build from the stored credential as one build,
// whichever asks first: the stored refresh token is refreshed once, by one
// authority. The builder is the real one, held at its real token request.
// The browser opens only after that build has settled, and only when the
// stored credential did not produce a signed-in session.
func TestStartupAndAuthenticateShareTheStoredBuild(t *testing.T) {
	type outcome struct {
		name        string
		token       func(n int) (int, string) // the stored refresh's answer
		wantBrowser int32
		wantStored  string // a substring of the credential file afterwards
		wantRefused bool   // the provider remembers a refused stored sign-in until the sign-in
		wantStartup func(error) bool
	}
	ok := func(err error) bool { return err == nil }
	outcomes := []outcome{
		{"the stored refresh rotates the token", tokenOK, 0, "silent-rotated", false, ok},
		{"the stored refresh is refused", tokenRevoked, 1, "new-refresh", true,
			func(err error) bool { return errors.Is(err, playlist.ErrNeedsAuth) }},
		{"the stored refresh cannot reach Spotify", func(int) (int, string) { return http.StatusServiceUnavailable, "no healthy upstream" },
			1, "new-refresh", false, ok},
	}
	for _, order := range []string{"startup first", "Authenticate first"} {
		for _, oc := range outcomes {
			t.Run(order+": "+oc.name, func(t *testing.T) {
				path := storeCreds(t)
				b := stubBuild(t)
				writes := countWrites(t)
				held := newGate()
				h := fakeSpotifyHTTP(t, func(n int) (int, string) {
					if n == 1 {
						held.wait()
					}
					return oc.token(n)
				}, apiOK)
				joined := hookOnce(t, &storedJoined)
				p := New(nil, "client", 320)

				startup, signIn := make(chan error, 1), make(chan error, 1)
				first, second := func() { startup <- p.ensureSession() }, func() { signIn <- p.Authenticate() }
				if order == "Authenticate first" {
					first, second = second, first
				}
				go first()
				arrive(t, "the stored build's token request", held.entered)
				go second()
				arrive(t, "the second caller to join the stored build", joined)
				if n := b.librespotSessions.Load(); n != 1 {
					t.Fatalf("%d librespot sessions were built for the stored credential, want the one both share", n)
				}
				storedRequests := h.tokenCalls.Load()
				if b.browserFlows.Load() != 0 {
					t.Fatal("the browser opened while the stored build was in flight")
				}
				held.open()

				if err := <-startup; !oc.wantStartup(err) {
					t.Errorf("the startup caller got %v", err)
				}
				if err := <-signIn; err != nil {
					t.Fatalf("Authenticate() = %v", err)
				}
				if got := b.browserFlows.Load(); got != oc.wantBrowser {
					t.Errorf("%d browser sign-ins ran, want %d", got, oc.wantBrowser)
				}
				if p.session == nil || p.session.needsSignIn() {
					t.Fatal("no signed-in session after Authenticate")
				}
				if got := mustRead(t, path); !strings.Contains(got, oc.wantStored) {
					t.Errorf("the credential file holds %q, want %q in it", got, oc.wantStored)
				}
				if got := p.authEpoch.Load(); got != 1 {
					t.Errorf("the count of sign-ins is %d, want 1: one sign-in was accepted", got)
				}
				if phaseOf(p) != phaseIdle {
					t.Errorf("the provider is left in build phase %d", phaseOf(p))
				}
				// One refresh of the stored token in all: oauth2 may ask a
				// failed one a second time, and nothing asks after that.
				if got := h.tokenCalls.Load(); got < storedRequests || got > refusedRefresh {
					t.Errorf("the token endpoint was asked %d times (%d while held), want one refresh", got, storedRequests)
				}
				if oc.wantBrowser == 0 && writes.Load() != 1 {
					t.Errorf("the credential was written %d times, want once", writes.Load())
				}
			})
		}
	}
}

// A sign-in that is cancelled while it waits for the stored build stops
// waiting, and the build goes on: it is the startup build too. When it is
// taken afterwards it is nobody's sign-in: the count of sign-ins does not
// move, and no browser opens.
func TestCancelledSignInLeavesTheStoredBuildRunning(t *testing.T) {
	for _, order := range []string{"Authenticate alone", "startup first", "startup joins afterwards"} {
		t.Run(order, func(t *testing.T) {
			path := storeCreds(t)
			b := stubBuild(t)
			writes := countWrites(t)
			held := newGate()
			h := fakeSpotifyHTTP(t, func(n int) (int, string) {
				if n == 1 {
					held.wait()
				}
				return tokenOK(n)
			}, apiOK)
			p := New(nil, "client", 320)

			startup := make(chan error, 1)
			if order == "startup first" {
				go func() { startup <- p.ensureSession() }()
				arrive(t, "the stored build's token request", held.entered)
			}
			signIn := make(chan error, 1)
			go func() { signIn <- p.Authenticate() }()
			if order != "startup first" {
				arrive(t, "the stored build's token request", held.entered)
			} else {
				waitFor(t, "the sign-in to register", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.signingIn != nil })
			}

			cancelSignIn(t, p)
			if err := <-signIn; !errors.Is(err, context.Canceled) {
				t.Fatalf("the cancelled Authenticate() = %v, want the cancellation", err)
			}
			p.mu.Lock()
			registered, phase := p.signingIn != nil, p.phase
			p.mu.Unlock()
			if registered || phase != phaseStoredBuilding {
				t.Fatalf("after the cancellation: sign-in registered %t, phase %d; want it gone and the stored build still running", registered, phase)
			}
			if order == "startup joins afterwards" {
				joined := hookOnce(t, &storedJoined)
				go func() { startup <- p.ensureSession() }()
				arrive(t, "the startup caller to join", joined)
			}
			held.open()
			if order != "Authenticate alone" {
				if err := <-startup; err != nil {
					t.Fatalf("the startup caller got %v", err)
				}
			} else {
				waitFor(t, "the stored build to be taken", func() bool { return p.sessionNow() != nil })
			}
			if p.session == nil || p.session.needsSignIn() {
				t.Fatal("the stored build was not taken after the sign-in gave up")
			}
			if got := p.authEpoch.Load(); got != 0 {
				t.Errorf("the count of sign-ins moved to %d for a build no sign-in was waiting for", got)
			}
			if b.browserFlows.Load() != 0 || h.tokenCalls.Load() != 1 || b.librespotSessions.Load() != 1 {
				t.Errorf("browser sign-ins %d, token requests %d, stored builds %d; want 0, 1 and 1",
					b.browserFlows.Load(), h.tokenCalls.Load(), b.librespotSessions.Load())
			}
			if !strings.Contains(mustRead(t, path), "silent-rotated") || writes.Load() != 1 {
				t.Errorf("the credential was written %d times and holds %q", writes.Load(), mustRead(t, path))
			}
		})
	}
}

// A sign-in whose context ends just as the stored build is taken has one of
// two outcomes, decided under the provider's lock: it succeeded and the
// count of sign-ins moved, or it was cancelled and the count did not.
func TestCancelRacesTheStoredBuildsCommit(t *testing.T) {
	for _, first := range []string{"the commit", "the cancellation"} {
		t.Run(first+" first", func(t *testing.T) {
			storeCreds(t)
			stubBuild(t)
			fakeSpotifyHTTP(t, tokenOK, apiOK)
			atCommit, release := make(chan struct{}), make(chan struct{})
			buildCommit = onceFunc(func() { close(atCommit); <-release })
			t.Cleanup(func() { buildCommit = nil })
			p := New(nil, "client", 320)

			signIn := make(chan error, 1)
			go func() { signIn <- p.Authenticate() }()
			arrive(t, "the stored build to reach its commit", atCommit)
			var err error
			if first == "the cancellation" {
				cancelSignIn(t, p)
				err = <-signIn // it deregisters before the commit runs
				close(release)
				waitFor(t, "the stored build to be taken", func() bool { return p.sessionNow() != nil })
			} else {
				close(release)
				err = <-signIn
			}
			epoch := p.authEpoch.Load()
			switch {
			case err == nil && epoch == 1:
			case errors.Is(err, context.Canceled) && epoch == 0:
			default:
				t.Errorf("Authenticate() = %v with %d sign-ins counted: neither a sign-in that succeeded nor one that was cancelled", err, epoch)
			}
			if (first == "the cancellation") != (err != nil) {
				t.Errorf("with %s first, Authenticate() = %v", first, err)
			}
		})
	}
}

// While the user signs in through the browser, nothing is built from the
// stored credential: a caller that has no session is told a sign-in is in
// progress, at once, and nothing is remembered against the credential. That
// holds after a stored build that failed for a passing reason, which left
// neither a session nor a refusal behind.
func TestBrowserPhaseNeverOverlapsStoredBuild(t *testing.T) {
	for _, ending := range []string{"succeeds", "fails", "is cancelled"} {
		t.Run("the browser sign-in "+ending, func(t *testing.T) {
			storeCreds(t)
			b := stubBuild(t)
			b.initErr = errors.New("spotify: player init: connection reset") // the stored build fails, with no verdict on the sign-in
			h := fakeSpotifyHTTP(t, tokenOK, apiOK)
			browser := newGate()
			tokens := interactiveTokens
			interactiveTokens = func(ctx context.Context, flows []oauthFlow) ([]*oauth2.Token, error) {
				b.initErr = nil // the browser's own session starts
				browser.wait()
				if ending == "fails" {
					return nil, errors.New("user closed the tab")
				}
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return tokens(ctx, flows)
			}
			p := New(nil, "client", 320)

			signIn := make(chan error, 1)
			go func() { signIn <- p.Authenticate() }()
			arrive(t, "the browser sign-in", browser.entered)
			if got := phaseOf(p); got != phaseBrowserSignIn {
				t.Fatalf("the provider is in build phase %d while the browser is open", got)
			}
			storedBuilds, storedRequests := b.librespotSessions.Load(), h.tokenCalls.Load()
			for i := range 3 {
				err := returnsWithin(t, 2*time.Second, "ensureSession during the browser sign-in", p.ensureSession)
				if !errors.Is(err, playlist.ErrNeedsAuth) || !errors.Is(err, errSignInInProgress) {
					t.Fatalf("ensureSession() #%d during the browser sign-in = %v, want sign-in in progress", i+1, err)
				}
			}
			p.mu.Lock()
			refused, stored := p.storedRefused, p.stored
			p.mu.Unlock()
			if refused || stored != nil || b.librespotSessions.Load() != storedBuilds || h.tokenCalls.Load() != storedRequests {
				t.Fatalf("a caller during the browser sign-in changed something: refused %t, stored build %t, builds %d->%d, token requests %d->%d",
					refused, stored != nil, storedBuilds, b.librespotSessions.Load(), storedRequests, h.tokenCalls.Load())
			}

			if ending == "is cancelled" {
				cancelSignIn(t, p)
			}
			browser.open()
			err := <-signIn
			if (ending == "succeeds") != (err == nil) {
				t.Fatalf("Authenticate() = %v when the browser sign-in %s", err, ending)
			}
			if got := phaseOf(p); got != phaseIdle {
				t.Errorf("the provider is left in build phase %d", got)
			}
			// Afterwards a caller sees the session, or builds from the stored
			// credential again, and only then.
			err = returnsWithin(t, 5*time.Second, "ensureSession after the browser sign-in", p.ensureSession)
			if ending == "succeeds" {
				if err != nil || b.librespotSessions.Load() != storedBuilds+1 {
					t.Errorf("after the sign-in: ensureSession() = %v, %d builds since; want the session and only the browser's build", err, b.librespotSessions.Load()-storedBuilds)
				}
			} else if err != nil || p.sessionNow() == nil {
				t.Errorf("after the sign-in ended: ensureSession() = %v, session %t; want a new stored build to be taken", err, p.sessionNow() != nil)
			}
		})
	}
}

// A build ends on its own deadline, whether or not its builder returns:
// librespot can keep a builder past its context. The call completes, its
// waiters are released, the provider can build again, and what the held
// builder returns later, a success included, is retired: not taken, not
// written, and no refresh is sent for it.
func TestStoredBuildCompletesOnItsDeadline(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	writes := countWrites(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	var setups atomic.Int32
	late := tokenSession(nil) // what the held builder reports, after its deadline
	setupTimeout := sessionSetupTimeout
	stubSessionSetup(t, 50*time.Millisecond, func(ctx context.Context, _ string) (*Session, error) {
		if setups.Add(1) == 1 {
			<-release // ignores its context, as a builder inside librespot can
			return late, nil
		}
		return goodSession(), nil
	})
	// However the test ends, the held builder is let go, so its worker ends:
	// a worker left waiting would hold builds, and the next test's wait, up
	// to the package's timeout. (Registered after the stub's own cleanup, so
	// it runs first.)
	t.Cleanup(letGo)
	p := New(nil, "client", 320)

	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- p.ensureSession() }()
	waitFor(t, "the first setup", func() bool { return setups.Load() == 1 })
	go func() { second <- p.ensureSession() }()
	for _, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, playlist.ErrNeedsAuth) {
				t.Fatalf("a waiter got %v, want the build's deadline, and no sign-in request", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a waiter is still waiting for a builder that ignores its deadline")
		}
	}
	p.mu.Lock()
	phase, refused, stored := p.phase, p.storedRefused, p.stored
	p.mu.Unlock()
	if phase != phaseIdle || refused || stored != nil || p.sessionNow() != nil {
		t.Fatalf("after the deadline: phase %d, refused %t, stored build %t, session %t; want the provider free", phase, refused, stored != nil, p.sessionNow() != nil)
	}

	// The provider builds again while the first builder is still held. That
	// build has the production deadline: the 50 ms one is the first build's,
	// which this test is about, and the next build must not race it.
	sessionSetupTimeout = setupTimeout
	if err := returnsWithin(t, 5*time.Second, "the next ensureSession", p.ensureSession); err != nil || setups.Load() != 2 {
		t.Fatalf("the next ensureSession() = %v after %d setups, want a new build to be taken", err, setups.Load())
	}
	taken := p.sessionNow()
	written := writes.Load()

	letGo() // the held builder reports success now
	waitBuilds(t)
	if p.sessionNow() != taken {
		t.Error("the late build replaced the session")
	}
	if !late.viewNow().closed {
		t.Error("the late build was not retired")
	}
	if writes.Load() != written {
		t.Errorf("the late build wrote the credential (%d more writes)", writes.Load()-written)
	}
	_ = before
}

// The stored build's own refresh is sent on the build's context: a builder
// that librespot released only after the build's deadline sends none.
func TestLateStoredBuilderSendsNoRefresh(t *testing.T) {
	storeCreds(t)
	b := stubBuild(t)
	h := fakeSpotifyHTTP(t, tokenOK, apiOK)
	orig := sessionSetupTimeout
	sessionSetupTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sessionSetupTimeout = orig })
	b.storedGate, b.storedEntered = make(chan struct{}), make(chan struct{}) // held in librespot, before the refresh
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(b.storedGate) }) }
	t.Cleanup(openGate) // however the test ends, the held builder is let go
	p := New(nil, "client", 320)

	err := returnsWithin(t, 5*time.Second, "ensureSession behind a held builder", p.ensureSession)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ensureSession() = %v, want the build's deadline", err)
	}
	openGate()
	waitBuilds(t)
	if n := h.tokenCalls.Load(); n != 0 {
		t.Errorf("the late builder sent %d token requests after its build had ended", n)
	}
	if p.sessionNow() != nil {
		t.Error("the late build was taken")
	}
}

// Only the build's own context ends a build: a caller that stops waiting
// does not.
func TestWaiterCancelDoesNotEndTheStoredBuild(t *testing.T) {
	storeCreds(t)
	b := stubBuild(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	b.storedGate, b.storedEntered = make(chan struct{}), make(chan struct{})
	p := New(nil, "client", 320)

	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() { waiter <- p.storedBuild(ctx) }()
	<-b.storedEntered
	cancel()
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatalf("the cancelled waiter got %v", err)
	}
	close(b.storedGate)
	waitFor(t, "the build to be taken", func() bool { return p.sessionNow() != nil })
	if b.librespotSessions.Load() != 1 {
		t.Errorf("%d builds ran, want the one the cancelled caller started", b.librespotSessions.Load())
	}
}

// Quit while the stored build runs: the provider takes nothing, and nothing
// is written.
func TestCloseDuringTheStoredBuild(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	b := stubBuild(t)
	writes := countWrites(t)
	held := newGate()
	fakeSpotifyHTTP(t, func(n int) (int, string) {
		if n == 1 {
			held.wait()
		}
		return tokenOK(n)
	}, apiOK)
	p := New(nil, "client", 320)

	startup, signIn := make(chan error, 1), make(chan error, 1)
	go func() { startup <- p.ensureSession() }()
	arrive(t, "the stored build's token request", held.entered)
	joined := hookOnce(t, &storedJoined)
	go func() { signIn <- p.Authenticate() }()
	arrive(t, "the sign-in to join", joined)

	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	arrive(t, "Close, which waits for no build", closed)
	for name, ch := range map[string]chan error{"the startup caller": startup, "Authenticate": signIn} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("%s got %v after Close, want a cancellation", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s is still waiting after Close", name)
		}
	}
	held.open()
	if got := b.disposed(); got != 1 {
		t.Errorf("the build that arrived after Close was closed %d times, want once", got)
	}
	if p.sessionNow() != nil || mustRead(t, path) != before || writes.Load() != 0 {
		t.Errorf("after Close: session %t, credential changed %t, writes %d; want nothing taken or written",
			p.sessionNow() != nil, mustRead(t, path) != before, writes.Load())
	}
}

// A browser sign-in ends on its own timeout even when its builder never
// returns: librespot can hold one. The sign-in is deregistered, the provider
// is free again, and what the held builder returns later, a success
// included, is retired: not installed and not written. (Adapted from the
// reviewer's round-9 probe.)
func TestSignInEndsOnItsOwnTimeout(t *testing.T) {
	for _, have := range []string{"no session", "a session that needs sign-in"} {
		t.Run(have, func(t *testing.T) {
			path := storeCreds(t)
			held := newGate()
			fresh := builtWith("synthetic-late")
			stubSignIn(t, func(context.Context) (*Session, error) {
				held.wait() // deaf to its context
				return fresh, nil
			})
			orig := signInTimeout
			signInTimeout = 30 * time.Millisecond
			t.Cleanup(func() { signInTimeout = orig })
			var sess *Session
			if have != "no session" {
				sess = brokenSession()
			}
			p := New(sess, "client", 320)
			p.storedRefused = true // the browser is the only way in
			before := mustRead(t, path)

			done := make(chan error, 1)
			go func() { done <- p.Authenticate() }()
			arrive(t, "the browser build", held.entered)
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Authenticate() = %v, want its own deadline", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the sign-in is still waiting for a builder that ignores its deadline")
			}
			p.mu.Lock()
			registered, phase := p.signingIn != nil, p.phase
			p.mu.Unlock()
			if registered || phase != phaseIdle || p.sessionNow() != sess {
				t.Fatalf("the ended sign-in kept something: registered %t, phase %d, session changed %t", registered, phase, p.sessionNow() != sess)
			}

			held.open() // the builder reports success now
			builds.Wait()
			tails.Wait()
			if !fresh.viewNow().closed {
				t.Error("the late build was not retired")
			}
			if p.sessionNow() != sess || mustRead(t, path) != before {
				t.Error("the late build was installed or written")
			}
			p.Close()
		})
	}
}
