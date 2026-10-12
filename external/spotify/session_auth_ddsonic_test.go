package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

func goodToken() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "access", Expiry: time.Now().Add(time.Hour)}, nil
}

// storeCreds points the config directory at a temporary one holding a stored
// sign-in, and returns the credential file's path.
func storeCreds(t *testing.T) string {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	if err := saveCreds(&storedCreds{Username: "u", Data: []byte("d"), DeviceID: "dev", RefreshToken: "old"}); err != nil {
		t.Fatal(err)
	}
	path, err := CredsPath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fakeSignIn stands in for the interactive sign-in and for librespot's
// teardown, and counts both.
type fakeSignIn struct {
	builds, closes atomic.Int32
	build          func(ctx context.Context) (*Session, error)
}

func stubSignIn(t *testing.T, build func(ctx context.Context) (*Session, error)) *fakeSignIn {
	t.Helper()
	f := &fakeSignIn{build: build}
	if f.build == nil {
		f.build = func(context.Context) (*Session, error) {
			return &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}, nil
		}
	}
	origNew, origBuild, origClose := newSessionInteractive, buildInteractiveSession, closeLibrespot
	run := func(ctx context.Context, _ string) (*Session, error) {
		f.builds.Add(1)
		return f.build(ctx)
	}
	newSessionInteractive, buildInteractiveSession = run, run
	closeLibrespot = func(*librespotPlayer.Player, *session.Session) { f.closes.Add(1) }
	t.Cleanup(func() {
		newSessionInteractive, buildInteractiveSession, closeLibrespot = origNew, origBuild, origClose
	})
	return f
}

// A refresh that Spotify refuses with invalid_grant means the sign-in is
// gone: the call reports ErrNeedsAuth, the session remembers it, and the
// stored credential stays. Every other refresh failure is an outage.
func TestWebAPIRefreshFailureClassification(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		needsAuth bool
	}{
		{"invalid_grant", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}, true},
		{"wrapped invalid_grant", fmt.Errorf("oauth2: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}), true},
		{"token endpoint down", &oauth2.RetrieveError{Response: &http.Response{StatusCode: 503}}, false},
		{"another oauth error", &oauth2.RetrieveError{ErrorCode: "server_error"}, false},
		{"timeout", context.DeadlineExceeded, false},
		{"network", errors.New("dial tcp: connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := storeCreds(t)
			before := mustRead(t, path)
			calls := 0
			sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(func() (*oauth2.Token, error) {
				calls++
				return nil, tt.err
			})}

			_, err := sess.webApiWithBody(context.Background(), "GET", "/v1/me", nil, nil, "")
			if err == nil || errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth {
				t.Fatalf("webApiWithBody() = %v, want an error, needing sign-in %v", err, tt.needsAuth)
			}
			if sess.authDead.Load() != tt.needsAuth {
				t.Errorf("session marked auth-dead = %v, want %v", sess.authDead.Load(), tt.needsAuth)
			}
			if got := mustRead(t, path); got != before {
				t.Errorf("stored credential changed after a failed refresh")
			}

			// A dead sign-in is not asked again; an outage is.
			_, err = sess.webApiWithBody(context.Background(), "GET", "/v1/me", nil, nil, "")
			if errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth {
				t.Errorf("second call = %v, want needing sign-in %v", err, tt.needsAuth)
			}
			if want := map[bool]int{true: 1, false: 2}[tt.needsAuth]; calls != want {
				t.Errorf("token source asked %d times, want %d", calls, want)
			}
		})
	}
}

// The unattended Web API callers (the catalog sync) see a dead sign-in as
// ErrNeedsAuth without asking Spotify again.
func TestEnsureWebAPIReportsDeadSession(t *testing.T) {
	storeCreds(t)
	calls := fakeTokenEndpoint(t, http.StatusOK, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`)
	sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
	sess.authDead.Store(true)
	p := New(sess, "client", 320)

	if err := p.ensureWebAPI(); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("ensureWebAPI() = %v, want ErrNeedsAuth", err)
	}
	if *calls != 0 {
		t.Errorf("token endpoint asked %d times for a dead sign-in, want 0", *calls)
	}
}

// A session with no Web API token and no stored credential (deleted, or
// reset from another terminal) needs sign-in.
func TestMissingWebAPITokenAndCredentialNeedsAuth(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New(&Session{clientID: "client"}, "client", 320)

	if _, err := p.SearchTracks(context.Background(), "q", 5); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("SearchTracks() = %v, want ErrNeedsAuth", err)
	}
}

// A session whose token refresh failed for a passing reason is not a
// sign-in problem for the provider's own search either: it tries the stored
// refresh token again, and an outage stays an outage.
func TestSearchWithoutWebTokenDuringOutageIsNotSignIn(t *testing.T) {
	storeCreds(t)
	fakeTokenEndpoint(t, http.StatusServiceUnavailable, `no healthy upstream`)
	p := New(&Session{clientID: "client"}, "client", 320)

	_, err := p.SearchTracks(context.Background(), "q", 5)
	if err == nil || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("SearchTracks() = %v, want an outage error, not ErrNeedsAuth", err)
	}
}

// A healthy refresh is used as before, and a rate limit is a rate limit.
func TestHealthyAndRateLimitedCallsAreNotSignIn(t *testing.T) {
	storeCreds(t)
	status, header := http.StatusOK, http.Header{}
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
			Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
	p := New(sess, "client", 320)

	resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
	if err != nil {
		t.Fatalf("webAPI() with a healthy token = %v", err)
	}
	resp.Body.Close()

	status, header = http.StatusTooManyRequests, http.Header{"Retry-After": {"3600"}}
	_, err = p.webAPI(context.Background(), "GET", "/v1/me", nil)
	if _, ok := errors.AsType[*catalog.RateLimitError](err); !ok || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("webAPI() when rate limited = %v, want a RateLimitError and no sign-in", err)
	}
	if sess.authDead.Load() {
		t.Error("session marked auth-dead by a healthy call or a rate limit")
	}
}

// Authenticate signs in when there is no session (first run, after a reset),
// leaves a healthy session alone, and replaces a broken one in place, closing
// what it replaced.
func TestAuthenticate(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		f := stubSignIn(t, nil)
		p := New(nil, "client", 320)
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
		if p.session == nil || f.builds.Load() != 1 {
			t.Errorf("session = %v after %d sign-ins, want one sign-in and a session", p.session, f.builds.Load())
		}
	})
	t.Run("healthy session", func(t *testing.T) {
		f := stubSignIn(t, nil)
		p := New(&Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}, "client", 320)
		if err := p.Authenticate(); err != nil || f.builds.Load() != 0 {
			t.Errorf("Authenticate() = %v after %d sign-ins, want none for a healthy session", err, f.builds.Load())
		}
	})
	for _, broken := range []string{"refused sign-in", "no web api token"} {
		t.Run("broken session: "+broken, func(t *testing.T) {
			f := stubSignIn(t, nil)
			old := &Session{clientID: "client"}
			if broken == "refused sign-in" {
				old.tokenSource = tokenSourceFunc(func() (*oauth2.Token, error) { return nil, errors.New("old source used") })
				old.authDead.Store(true)
			}
			p := New(old, "client", 320)
			p.userID, p.meFetched = "someone", true

			if err := p.Authenticate(); err != nil {
				t.Fatal(err)
			}
			if f.builds.Load() != 1 || f.closes.Load() != 1 {
				t.Errorf("sign-ins = %d, old sessions closed = %d, want 1 and 1", f.builds.Load(), f.closes.Load())
			}
			if p.session != old || old.authDead.Load() {
				t.Errorf("session replaced in place = %v, still auth-dead = %v", p.session == old, old.authDead.Load())
			}
			if old.tokenSource == nil {
				t.Fatal("no web api token after the sign-in")
			}
			if tok, err := old.tokenSource.Token(); err != nil || tok.AccessToken != "access" {
				t.Errorf("token after sign-in = %v, %v, want the new session's", tok, err)
			}
			if p.userID != "" || p.meFetched {
				t.Error("the old account's cached identity survived the sign-in")
			}
		})
	}
}

// A sign-in that fails or is cancelled changes nothing: the session, its
// auth-dead mark and the stored credential all stay.
func TestFailedSignInKeepsSessionAndCredentials(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	f := stubSignIn(t, func(context.Context) (*Session, error) { return nil, errors.New("user closed the tab") })
	oldSource := tokenSourceFunc(goodToken)
	old := &Session{clientID: "client", tokenSource: oldSource}
	old.authDead.Store(true)
	p := New(old, "client", 320)

	if err := p.Authenticate(); err == nil {
		t.Fatal("Authenticate() = nil after a failed sign-in")
	}
	if p.session != old || !old.authDead.Load() || old.tokenSource == nil {
		t.Error("a failed sign-in changed the session")
	}
	if f.closes.Load() != 0 {
		t.Error("a failed sign-in closed the old session")
	}
	if got := mustRead(t, path); got != before {
		t.Error("a failed sign-in changed the stored credential")
	}
	// Still broken, so the next explicit sign-in runs again.
	f.build = func(context.Context) (*Session, error) {
		return &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}, nil
	}
	if err := p.Authenticate(); err != nil || old.authDead.Load() {
		t.Errorf("second Authenticate() = %v, auth-dead %v, want a working session", err, old.authDead.Load())
	}
}

// Two sign-in requests at once share one browser flow.
func TestDuplicateSignInRequestsRunOneFlow(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	f := stubSignIn(t, func(ctx context.Context) (*Session, error) {
		started <- struct{}{}
		select {
		case <-release:
			return &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	p := New(nil, "client", 320)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Go(func() { errs[0] = p.Authenticate() })
	<-started
	wg.Go(func() { errs[1] = p.Authenticate() })
	// Give the second request time to start a flow of its own, if it would.
	select {
	case <-started:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	wg.Wait()

	if errs[0] != nil || errs[1] != nil {
		t.Errorf("Authenticate() = %v and %v, want both to succeed", errs[0], errs[1])
	}
	if f.builds.Load() != 1 {
		t.Errorf("sign-in flows = %d, want 1", f.builds.Load())
	}
}

// Quitting ends a sign-in in flight and installs no session afterwards.
func TestCloseCancelsSignIn(t *testing.T) {
	started := make(chan struct{})
	stubSignIn(t, func(ctx context.Context) (*Session, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	p := New(nil, "client", 320)

	done := make(chan error, 1)
	go func() { done <- p.Authenticate() }()
	<-started
	p.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Authenticate() = %v after Close, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Authenticate() still running after Close")
	}
	if p.session != nil {
		t.Error("a session was installed after Close")
	}
}

// fakeSource is an open stream's audio: it reads until closed.
type fakeSource struct{ reads atomic.Int32 }

func (s *fakeSource) SetPositionMs(int64) error { return nil }
func (s *fakeSource) PositionMs() int64         { return 0 }
func (s *fakeSource) Read(p []float32) (int, error) {
	s.reads.Add(1)
	return len(p), nil
}

const testTrackURI = "spotify:track:4uLU6hMCjMI75M1A2tKUQC"

// stubStreams makes every stream open fail with openErr, or succeed when it
// is nil, and the silent reconnect return reconnErr.
func stubStreams(t *testing.T, openErr func() error, reconnErr error) (opens, reconnects *atomic.Int32) {
	t.Helper()
	opens, reconnects = new(atomic.Int32), new(atomic.Int32)
	origOpen, origReconn := sessionNewStream, sessionReconnect
	sessionNewStream = func(*Session, context.Context, librespot.SpotifyId, int) (*librespotPlayer.Stream, context.CancelFunc, error) {
		opens.Add(1)
		if err := openErr(); err != nil {
			return nil, nil, err
		}
		return &librespotPlayer.Stream{Source: &fakeSource{}}, func() {}, nil
	}
	sessionReconnect = func(*Session, context.Context) error {
		reconnects.Add(1)
		return reconnErr
	}
	t.Cleanup(func() { sessionNewStream, sessionReconnect = origOpen, origReconn })
	return opens, reconnects
}

// A stream that Spotify refuses for the sign-in asks for sign-in only when
// the silent reconnect was refused too, or changed nothing. A reconnect that
// failed because Spotify could not be reached is an outage.
func TestPlaybackReconnectClassification(t *testing.T) {
	keyErr := func() error { return fmt.Errorf("spotify: %w", &audio.KeyProviderError{Code: 1}) }
	tests := []struct {
		name      string
		reconnErr error
		needsAuth bool
	}{
		{"reconnect refused: no stored sign-in", fmt.Errorf("spotify: reconnect: %w", errNoStoredCreds), true},
		{"reconnect refused: sign-in revoked", fmt.Errorf("spotify: reconnect: %w", fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)), true},
		{"reconnect worked, stream still refused", nil, true},
		{"reconnect could not reach Spotify", fmt.Errorf("spotify: reconnect: %w", errors.New("dial tcp: no route to host")), false},
		{"reconnect timed out", fmt.Errorf("spotify: reconnect: %w", context.DeadlineExceeded), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, reconnects := stubStreams(t, keyErr, tt.reconnErr)
			sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
			p := New(sess, "client", 320)

			_, _, _, err := p.NewStreamer(testTrackURI)
			if err == nil || errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth {
				t.Fatalf("NewStreamer() = %v, want an error, needing sign-in %v", err, tt.needsAuth)
			}
			if sess.authDead.Load() != tt.needsAuth {
				t.Errorf("session marked auth-dead = %v, want %v", sess.authDead.Load(), tt.needsAuth)
			}
			if reconnects.Load() != 1 {
				t.Errorf("silent reconnects = %d, want 1", reconnects.Load())
			}
		})
	}
}

// A stream cut short by a skip, or failing for the network, never marks the
// sign-in dead and never reconnects.
func TestPlaybackNetworkFailureIsNotAuth(t *testing.T) {
	for _, openErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("connection reset by peer")} {
		_, reconnects := stubStreams(t, func() error { return openErr }, nil)
		sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
		p := New(sess, "client", 320)

		_, _, _, err := p.NewStreamer(testTrackURI)
		if err == nil || errors.Is(err, playlist.ErrNeedsAuth) || sess.authDead.Load() || reconnects.Load() != 0 {
			t.Errorf("NewStreamer() after %v = %v (auth-dead %v, reconnects %d), want a plain error",
				openErr, err, sess.authDead.Load(), reconnects.Load())
		}
	}
}

// Signing in again while a track plays: the stream opened under the broken
// session keeps reading its own audio, is not cancelled by the swap, and
// closes normally; what the swap closes is the old session, once; and the
// next stream opens under the new sign-in.
func TestSessionReplacementUnderOpenStream(t *testing.T) {
	f := stubSignIn(t, nil)
	opens, _ := stubStreams(t, func() error { return nil }, nil)
	sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
	p := New(sess, "client", 320)

	playing, _, _, err := p.NewStreamer(testTrackURI)
	if err != nil {
		t.Fatal(err)
	}
	st := playing.(*spotifyStreamer)
	var cancelled atomic.Int32
	st.cancel = func() { cancelled.Add(1) }
	samples := make([][2]float64, 64)
	if n, ok := playing.Stream(samples); !ok || n != len(samples) {
		t.Fatalf("Stream() before the sign-in = %d, %v", n, ok)
	}

	sess.authDead.Store(true) // Spotify refuses the sign-in mid-track
	// Reading goes on while the sign-in replaces the session.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
				playing.Stream(samples)
			}
		}
	})
	if err := p.Authenticate(); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	if f.builds.Load() != 1 || f.closes.Load() != 1 {
		t.Errorf("sign-ins = %d, old sessions closed = %d, want 1 and 1", f.builds.Load(), f.closes.Load())
	}
	if cancelled.Load() != 0 {
		t.Error("the sign-in cancelled the playing stream")
	}
	if n, ok := playing.Stream(samples); !ok || n != len(samples) || playing.Err() != nil {
		t.Errorf("Stream() after the sign-in = %d, %v, err %v, want it to keep playing", n, ok, playing.Err())
	}
	if p.session != sess || sess.authDead.Load() {
		t.Error("the provider is not on a working session after the sign-in")
	}
	next, _, _, err := p.NewStreamer(testTrackURI)
	if err != nil || opens.Load() != 2 {
		t.Fatalf("NewStreamer() after the sign-in = %v after %d opens", err, opens.Load())
	}
	if err := playing.Close(); err != nil || cancelled.Load() != 1 {
		t.Errorf("Close() of the old stream = %v, cancelled %d times, want nil and once", err, cancelled.Load())
	}
	if n, ok := playing.Stream(samples); ok || n != 0 {
		t.Error("the old stream still plays after Close")
	}
	if n, ok := next.Stream(samples); !ok || n != len(samples) {
		t.Error("the new stream does not play after the old one closed")
	}
	next.Close()
}
