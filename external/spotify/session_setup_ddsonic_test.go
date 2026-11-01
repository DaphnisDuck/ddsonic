package spotify

// ddsonic: ensureSession bounds session setup (upstream deb2f447). A setup that
// stalls must end with an error and give the session lock up, not hold every
// Spotify call behind it.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// stubSessionSetup replaces the session creator and the setup timeout for
// one test.
func stubSessionSetup(t *testing.T, timeout time.Duration, create func(context.Context, string) (*Session, error)) {
	t.Helper()
	quiesce(t) // a setup still running reads these
	oldCreate, oldTimeout := newSessionSilent, sessionSetupTimeout
	newSessionSilent, sessionSetupTimeout = create, timeout
	t.Cleanup(func() { newSessionSilent, sessionSetupTimeout = oldCreate, oldTimeout })
	settleBackground(t)
}

// returnsWithin fails the test when fn has not returned after limit: the
// indefinite wait this fix removes.
func returnsWithin(t *testing.T, limit time.Duration, what string, fn func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("%s did not return within %v", what, limit)
		return nil
	}
}

func TestSessionSetupTimeoutIsThirtySeconds(t *testing.T) {
	if sessionSetupTimeout != 30*time.Second {
		t.Fatalf("sessionSetupTimeout = %v, want 30s", sessionSetupTimeout)
	}
}

// A setup that never answers, as an access point that accepts the connection
// and goes quiet: it returns only when its context ends.
func TestEnsureSessionEndsAStalledSetup(t *testing.T) {
	var calls atomic.Int32
	var hadDeadline atomic.Bool
	stubSessionSetup(t, 50*time.Millisecond, func(ctx context.Context, _ string) (*Session, error) {
		calls.Add(1)
		if _, ok := ctx.Deadline(); ok {
			hadDeadline.Store(true)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	p := &SpotifyProvider{clientID: "client"}

	err := returnsWithin(t, 5*time.Second, "ensureSession with a stalled setup", p.ensureSession)
	if !hadDeadline.Load() {
		t.Fatal("session setup got a context without a deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ensureSession error = %v, want the setup's deadline error", err)
	}
	// A timeout is an outage, not a rejected sign-in: nothing may tell the
	// user to sign in again.
	if errors.Is(err, playlist.ErrNeedsAuth) || needsSignIn(err) {
		t.Fatalf("a setup timeout was reported as needing sign-in: %v", err)
	}

	// The lock is free: the next call reaches session setup again.
	wantErr := errors.New("still down")
	stubSessionSetup(t, 50*time.Millisecond, func(context.Context, string) (*Session, error) {
		calls.Add(1)
		return nil, wantErr
	})
	err = returnsWithin(t, 5*time.Second, "the next ensureSession", p.ensureSession)
	if !errors.Is(err, wantErr) || calls.Load() != 2 {
		t.Fatalf("the next ensureSession: error = %v, setups = %d; want it to reach setup again", err, calls.Load())
	}
}

// A caller that arrives while a setup is stalled shares it: one build from
// the stored credential runs at a time. It is not made to wait forever: it
// gets the stalled setup's own end, and the call after that sets up again.
func TestEnsureSessionCallerBehindAStalledSetupGetsItsEnd(t *testing.T) {
	entered := make(chan struct{}, 2)
	var calls atomic.Int32
	stubSessionSetup(t, 50*time.Millisecond, func(ctx context.Context, _ string) (*Session, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	p := &SpotifyProvider{clientID: "client"}

	first := make(chan error, 1)
	go func() { first <- p.ensureSession() }()
	<-entered // the first setup is stalled

	err := returnsWithin(t, 5*time.Second, "a second ensureSession behind a stalled one", p.ensureSession)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("the second caller: error = %v, setups = %d; want the one stalled setup's deadline error", err, calls.Load())
	}
	if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the first caller's error = %v, want the deadline error", err)
	}
	if err := returnsWithin(t, 5*time.Second, "the call after the stalled setup", p.ensureSession); err == nil || calls.Load() != 2 {
		t.Fatalf("the next caller: error = %v, setups = %d; want it to set up again", err, calls.Load())
	}
}

// A setup that succeeds is kept, and later calls don't set up again.
func TestEnsureSessionKeepsASessionThatCameUp(t *testing.T) {
	var calls atomic.Int32
	sess := &Session{}
	stubSessionSetup(t, time.Second, func(context.Context, string) (*Session, error) {
		calls.Add(1)
		return sess, nil
	})
	p := &SpotifyProvider{clientID: "client"}
	for range 2 {
		if err := p.ensureSession(); err != nil {
			t.Fatal(err)
		}
	}
	if p.session != sess || calls.Load() != 1 {
		t.Fatalf("session kept = %v, setups = %d; want the session kept after one setup", p.session == sess, calls.Load())
	}
}

// stalledTokenEndpoint makes every token request wait until its context
// ends, with a client timeout far longer than the setup's deadline: only the
// setup's context can end the refresh in time.
func stalledTokenEndpoint(t *testing.T, clientTimeout time.Duration) {
	t.Helper()
	old := tokenHTTPClient
	tokenHTTPClient = &http.Client{Timeout: clientTimeout, Transport: stallTransport{}}
	t.Cleanup(func() { tokenHTTPClient = old })
}

// The refresh made during session setup ends with the setup's deadline, not
// with its own client's timeout.
func TestSilentTokenRefreshEndsWithItsContext(t *testing.T) {
	stalledTokenEndpoint(t, 30*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var refreshErr error
	_ = returnsWithin(t, 5*time.Second, "a refresh under an expired setup deadline", func() error {
		_, refreshErr = silentTokenRefresh(ctx, "id", "refresh-token")
		return refreshErr
	})
	if refreshErr == nil {
		t.Fatal("a stalled refresh succeeded")
	}
	err := setupExpired(ctx, refreshErr)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("setupExpired = %v, want the setup's deadline error", err)
	}
	if errors.Is(err, playlist.ErrNeedsAuth) || needsSignIn(err) || isInvalidGrant(refreshErr) {
		t.Fatalf("a refresh cut short by the deadline was treated as a sign-in problem: %v (refresh: %v)", err, refreshErr)
	}
}

// setupExpired leaves alone a refresh that failed for a reason of its own
// while the setup still had time.
func TestSetupExpiredOnlyWhenTheSetupRanOut(t *testing.T) {
	live, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := setupExpired(live, errors.New("refused")); err != nil {
		t.Fatalf("setupExpired with time left = %v, want nil", err)
	}
	done, cancelDone := context.WithCancel(context.Background())
	cancelDone()
	if err := setupExpired(done, nil); err != nil {
		t.Fatalf("setupExpired with no failed step = %v, want nil", err)
	}
	if err := setupExpired(done, errors.New("cut short")); !errors.Is(err, context.Canceled) {
		t.Fatalf("setupExpired after cancellation = %v, want the context's error", err)
	}
}

// The whole setup has one budget. The access point connects (the stub does
// that part at once); the token endpoint then stalls. ensureSession must end
// at the setup's deadline, release the lock, and let the next call try again,
// instead of waiting out the token client's own, much longer, timeout.
func TestEnsureSessionEndsAStalledTokenRefresh(t *testing.T) {
	stalledTokenEndpoint(t, 30*time.Second)
	var calls atomic.Int32
	// The two production steps newSessionFromStored takes after connecting.
	refreshThenCheck := func(ctx context.Context, clientID string) (*Session, error) {
		calls.Add(1)
		_, refreshErr := silentTokenRefresh(ctx, clientID, "refresh-token")
		if err := setupExpired(ctx, refreshErr); err != nil {
			return nil, err
		}
		return nil, refreshErr
	}
	stubSessionSetup(t, 50*time.Millisecond, refreshThenCheck)
	p := &SpotifyProvider{clientID: "client"}

	start := time.Now()
	err := returnsWithin(t, 5*time.Second, "ensureSession with a stalled token endpoint", p.ensureSession)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("ensureSession took %v: the refresh outlasted the 50ms setup deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ensureSession error = %v, want the setup's deadline error", err)
	}
	if errors.Is(err, playlist.ErrNeedsAuth) || needsSignIn(err) {
		t.Fatalf("a stalled refresh was reported as needing sign-in: %v", err)
	}

	// The lock is free and the next attempt reaches setup again.
	err = returnsWithin(t, 5*time.Second, "the next ensureSession", p.ensureSession)
	if err == nil || calls.Load() != 2 {
		t.Fatalf("the next ensureSession: error = %v, setups = %d; want a second setup", err, calls.Load())
	}
}

// The page a browser shows after sign-in is titled ddsonic, not cliamp.
func TestOAuthCallbackPageNamesDdsonic(t *testing.T) {
	callbacks := make(chan oauthCallback, 1)
	handler := oauthCallbackHandler([]pendingOAuthFlow{{state: "s"}}, callbacks)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login?state=s&code=c", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>ddsonic</title>") || strings.Contains(body, "cliamp") {
		t.Errorf("callback page does not name ddsonic alone:\n%s", body)
	}
}
