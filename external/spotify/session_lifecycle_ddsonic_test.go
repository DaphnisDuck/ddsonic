package spotify

// ddsonic: the sign-in's lifecycle, on the real builders. Only what reaches
// Spotify is a stand-in (the browser flow, librespot's session and player,
// the token endpoint and the Web API), so the order in which a session is
// built, published and its credential written is the product's own.

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

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// No test in this package may reach Spotify or open a browser: a test that
// forgets its stand-in fails here instead. Each test that expects a call
// installs its own (stubBuild, fakeSpotifyHTTP, fakeTokenEndpoint).
func init() {
	interactiveTokens = func(context.Context, []oauthFlow) ([]*oauth2.Token, error) {
		return nil, errors.New("test: a browser sign-in was started without a stand-in")
	}
	real := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.Contains(req.URL.Host, "spotify.com") || strings.Contains(req.URL.Host, "scdn.co") {
			return nil, fmt.Errorf("test: a request to %s was made without a stand-in", req.URL.Host)
		}
		return real.RoundTrip(req)
	})
}

// build stands in for Spotify under the real session builders.
type build struct {
	browserFlows, librespotSessions, playerInits, disposals atomic.Int32
	initErr                                                 error
	onInit                                                  func() // runs while the replacement is being finished
	storedGate                                              chan struct{}
	storedEntered                                           chan struct{}
}

func stubBuild(t *testing.T) *build {
	t.Helper()
	b := &build{}
	origTokens, origSess, origID, origInit, origClose := interactiveTokens, newLibrespotSession, sessionIdentity, initSessionPlayer, closeLibrespot
	interactiveTokens = func(_ context.Context, flows []oauthFlow) ([]*oauth2.Token, error) {
		b.browserFlows.Add(1)
		tokens := make([]*oauth2.Token, len(flows))
		for i := range tokens {
			tokens[i] = &oauth2.Token{AccessToken: "signed-in", RefreshToken: "new-refresh", Expiry: time.Now().Add(time.Hour)}
		}
		return tokens, nil
	}
	newLibrespotSession = func(ctx context.Context, opts *session.Options) (*session.Session, error) {
		b.librespotSessions.Add(1)
		if _, stored := opts.Credentials.(session.StoredCredentials); stored && b.storedGate != nil {
			if b.storedEntered != nil {
				close(b.storedEntered)
			}
			<-b.storedGate
		}
		return nil, nil
	}
	sessionIdentity = func(*session.Session) (string, []byte) { return "user", []byte("blob") }
	initSessionPlayer = func(*Session) error {
		b.playerInits.Add(1)
		if b.onInit != nil {
			b.onInit()
		}
		return b.initErr
	}
	closeLibrespot = func(*librespotPlayer.Player, *session.Session) { b.disposals.Add(1) }
	t.Cleanup(func() {
		interactiveTokens, newLibrespotSession, sessionIdentity, initSessionPlayer, closeLibrespot = origTokens, origSess, origID, origInit, origClose
	})
	return b
}

// spotifyHTTP stands in for the token endpoint and the Web API, each
// answering by request number, and counts both.
type spotifyHTTP struct {
	tokenCalls, apiCalls atomic.Int32
}

func fakeSpotifyHTTP(t *testing.T, token func(n int) (int, string), api func(n int, req *http.Request) (int, http.Header)) *spotifyHTTP {
	t.Helper()
	h := &spotifyHTTP{}
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status, body, header := http.StatusOK, `{}`, http.Header{}
		switch {
		case strings.Contains(req.URL.Host, "accounts.spotify.com"):
			status, body = token(int(h.tokenCalls.Add(1)))
			header.Set("Content-Type", "application/json")
		case strings.Contains(req.URL.Host, "api.spotify.com"):
			var hd http.Header
			status, hd = api(int(h.apiCalls.Add(1)), req)
			if hd != nil {
				header = hd
			}
		default:
			t.Errorf("unexpected request to %s", req.URL)
		}
		return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	return h
}

func tokenOK(int) (int, string) {
	return http.StatusOK, `{"access_token":"refreshed","token_type":"Bearer","refresh_token":"silent-rotated","expires_in":3600}`
}
func tokenRevoked(int) (int, string) { return http.StatusBadRequest, `{"error":"invalid_grant"}` }

// refusedRefresh is how many requests one refused refresh makes: oauth2 asks
// a second time with the client ID in the body before it gives up.
const refusedRefresh = 2

func apiOK(int, *http.Request) (int, http.Header) {
	return http.StatusOK, nil
}

func credsExist(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func brokenSession() *Session {
	s := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
	s.refuseSignIn(s.generation())
	return s
}

// A stored sign-in that Spotify refuses at setup (a launch, or playback's
// silent reconnect) needs sign-in, and the credential file stays exactly as
// it was. The refusal is remembered: Spotify is not asked again.
func TestRejectedStoredSignInKeepsCredential(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	b := stubBuild(t)
	h := fakeSpotifyHTTP(t, tokenRevoked, apiOK)

	if _, err := NewSessionSilent(context.Background(), "client"); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("NewSessionSilent() = %v, want ErrNeedsAuth", err)
	}
	if !credsExist(t, path) || mustRead(t, path) != before {
		t.Fatal("a refused stored sign-in changed or deleted the credential")
	}
	if b.disposals.Load() != 1 {
		t.Errorf("the refused setup's session was closed %d times, want 1", b.disposals.Load())
	}

	p := New(nil, "client", 320)
	for i := range 3 {
		if err := p.ensureSession(); !errors.Is(err, playlist.ErrNeedsAuth) {
			t.Fatalf("ensureSession() #%d = %v, want ErrNeedsAuth", i+1, err)
		}
	}
	if got := h.tokenCalls.Load(); got != 2*refusedRefresh { // NewSessionSilent's, and the provider's first
		t.Errorf("token endpoint asked %d times, want %d: the provider remembers the refusal", got, 2*refusedRefresh)
	}
	if mustRead(t, path) != before {
		t.Error("the credential changed while the provider reported the refusal")
	}
	// An explicit sign-in is what clears it, and what replaces the credential.
	if err := p.Authenticate(); err != nil {
		t.Fatal(err)
	}
	if err := p.ensureSession(); err != nil || mustRead(t, path) == before {
		t.Errorf("after signing in: ensureSession() = %v, credential replaced = %v", err, mustRead(t, path) != before)
	}
}

// The credential is written only when the sign-in has produced a working
// session that the provider took. A player that fails to start, or a quit
// while the replacement was being finished, leaves the previous credential
// byte for byte, and the unaccepted session is closed once.
func TestInteractiveSignInCommitsCredentialOnlyWhenAccepted(t *testing.T) {
	for _, have := range []string{"a broken session", "no session"} {
		newProvider := func() *SpotifyProvider {
			if have == "no session" {
				return New(nil, "client", 320)
			}
			return New(brokenSession(), "client", 320)
		}
		t.Run(have+": the player fails to start", func(t *testing.T) {
			path := storeCreds(t)
			before := mustRead(t, path)
			b := stubBuild(t)
			fakeSpotifyHTTP(t, tokenRevoked, apiOK)
			b.initErr = errors.New("spotify: player init: no audio key")
			p := newProvider()
			old := p.session

			if err := p.Authenticate(); err == nil {
				t.Fatal("Authenticate() = nil although the player did not start")
			}
			if mustRead(t, path) != before {
				t.Error("a sign-in that failed to start its player changed the credential")
			}
			if p.session != old || (old != nil && !old.signInRefused()) {
				t.Error("a failed sign-in changed the session")
			}
			// Every librespot session the sign-in opened is closed, once: the
			// stored one Spotify refused (with no session to start from), and
			// the one whose player failed.
			if b.disposals.Load() != b.librespotSessions.Load() {
				t.Errorf("%d librespot sessions were opened and %d closed", b.librespotSessions.Load(), b.disposals.Load())
			}
		})
		t.Run(have+": quit while the replacement is finished", func(t *testing.T) {
			path := storeCreds(t)
			before := mustRead(t, path)
			b := stubBuild(t)
			fakeSpotifyHTTP(t, tokenRevoked, apiOK)
			p := newProvider()
			old := p.session
			var oldSource oauth2.TokenSource
			if old != nil {
				oldSource = old.tokenSource
			}
			b.onInit = p.Close // the replacement is built; ddsonic quits before it is taken

			if err := p.Authenticate(); err == nil {
				t.Fatal("Authenticate() = nil for a sign-in that ended after quit")
			}
			if mustRead(t, path) != before {
				t.Error("a sign-in that ended after quit changed the credential")
			}
			if p.session != nil {
				t.Error("a session is installed after quit")
			}
			if old != nil && (old.player != nil || old.sess != nil || fmt.Sprint(old.tokenSource) != fmt.Sprint(oldSource)) {
				t.Error("the closed session adopted the replacement")
			}
			if b.disposals.Load() < 1 {
				t.Error("the unaccepted replacement was never closed")
			}
		})
		t.Run(have+": accepted", func(t *testing.T) {
			path := storeCreds(t)
			before := mustRead(t, path)
			stubBuild(t)
			fakeSpotifyHTTP(t, tokenRevoked, apiOK)
			p := newProvider()

			if err := p.Authenticate(); err != nil {
				t.Fatal(err)
			}
			after := mustRead(t, path)
			if after == before || !strings.Contains(after, "new-refresh") {
				t.Errorf("an accepted sign-in did not store its credential: %q", after)
			}
			if p.session == nil || p.session.signInRefused() {
				t.Error("no working session after an accepted sign-in")
			}
		})
	}
	t.Run("first run: nothing is written for a sign-in that fails", func(t *testing.T) {
		t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
		path, _ := CredsPath()
		b := stubBuild(t)
		b.initErr = errors.New("spotify: player init: no audio key")
		if err := New(nil, "client", 320).Authenticate(); err == nil || credsExist(t, path) {
			t.Errorf("Authenticate() = %v, credential written = %v; want a failure and no file", err, credsExist(t, path))
		}
	})
}

// A provider that has been closed starts no sign-in: no browser, no session.
func TestAuthenticateAfterCloseStartsNothing(t *testing.T) {
	for _, p := range []*SpotifyProvider{New(nil, "client", 320), New(brokenSession(), "client", 320)} {
		storeCreds(t)
		b := stubBuild(t)
		fakeSpotifyHTTP(t, tokenRevoked, apiOK)
		p.Close()
		if err := p.Authenticate(); err == nil {
			t.Error("Authenticate() = nil on a closed provider")
		}
		if b.browserFlows.Load() != 0 || b.librespotSessions.Load() != 0 || p.session != nil {
			t.Errorf("a closed provider started %d browser flows and %d sessions (session installed: %v), want none",
				b.browserFlows.Load(), b.librespotSessions.Load(), p.session != nil)
		}
	}
}

// Close while a replacement is being built: the build is not cut short by
// the cancellation here (it has its answer already), and still nothing is
// published into the closed session, nothing is written, and the replacement
// is closed once.
func TestCloseWhileReplacementIsBuilt(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	b := stubBuild(t)
	fakeSpotifyHTTP(t, tokenRevoked, apiOK)
	entered, release := make(chan struct{}), make(chan struct{})
	b.onInit = func() {
		close(entered)
		<-release // ignores the cancelled context: the session is complete
	}
	old := brokenSession()
	oldSource := old.tokenSource
	p := New(old, "client", 320)

	done := make(chan error, 1)
	go func() { done <- p.Authenticate() }()
	<-entered
	p.Close() // finishes first
	closedAt := b.disposals.Load()
	close(release)
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Authenticate() did not return after Close")
	}
	if err == nil {
		t.Error("Authenticate() = nil for a replacement that arrived after Close")
	}
	if old.player != nil || old.sess != nil || fmt.Sprint(old.tokenSource) != fmt.Sprint(oldSource) || p.session != nil {
		t.Error("the replacement was published into a closed session")
	}
	if got := b.disposals.Load() - closedAt; got != 1 {
		t.Errorf("the unaccepted replacement was closed %d times, want exactly 1", got)
	}
	if mustRead(t, path) != before {
		t.Error("the unaccepted replacement's credential was written")
	}
}

// A token request made under the old sign-in that fails after the user has
// signed in again says nothing about the new sign-in: the session stays
// working, and the call goes through on the new token.
func TestStaleTokenResultDoesNotKillNewSignIn(t *testing.T) {
	storeCreds(t)
	stubBuild(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	revoked := &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(func() (*oauth2.Token, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release // the slow request, still in flight during the sign-in
		}
		return nil, revoked
	})}
	p := New(sess, "client", 320)

	slow := make(chan error, 1)
	go func() {
		resp, err := sess.webApiWithBody(context.Background(), "GET", "/v1/me", nil, nil, "")
		if resp != nil {
			resp.Body.Close()
		}
		slow <- err
	}()
	<-entered
	// A second request is refused at once; the user signs in again.
	if _, err := sess.webApiWithBody(context.Background(), "GET", "/v1/me", nil, nil, ""); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Fatalf("the refused request = %v, want ErrNeedsAuth", err)
	}
	if err := p.Authenticate(); err != nil || sess.signInRefused() {
		t.Fatalf("Authenticate() = %v, still refused = %v", err, sess.signInRefused())
	}

	close(release) // the old request's refusal arrives now
	err := <-slow
	if sess.signInRefused() {
		t.Error("the old sign-in's refusal marked the new sign-in refused")
	}
	if err != nil {
		t.Errorf("the slow request = %v, want it to go through on the new sign-in", err)
	}
	if _, err := sess.webApiWithBody(context.Background(), "GET", "/v1/me", nil, nil, ""); err != nil {
		t.Errorf("a request after the sign-in = %v", err)
	}
}

// Playback's silent reconnect, begun before the user signed in again, must
// not replace the newer sign-in when it finishes, nor write its credential.
func TestSilentReconnectDoesNotReplaceNewerSignIn(t *testing.T) {
	path := storeCreds(t)
	b := stubBuild(t)
	b.storedGate, b.storedEntered = make(chan struct{}), make(chan struct{})
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
	p := New(sess, "client", 320)

	silent := make(chan error, 1)
	go func() { silent <- sess.Reconnect(context.Background()) }()
	<-b.storedEntered // the silent rebuild is under way, from the old credential

	sess.refuseSignIn(sess.generation())
	if err := p.Authenticate(); err != nil {
		t.Fatal(err)
	}
	signedIn := mustRead(t, path)
	if !strings.Contains(signedIn, "new-refresh") {
		t.Fatalf("the sign-in did not store its credential: %q", signedIn)
	}
	closedBefore := b.disposals.Load()

	close(b.storedGate) // the silent rebuild finishes now
	if err := <-silent; err != nil {
		t.Errorf("the overtaken silent reconnect = %v, want nil: the session is already renewed", err)
	}
	if tok, err := sess.tokenSource.Token(); err != nil || tok.AccessToken != "signed-in" {
		t.Errorf("token after the overtaken reconnect = %v, %v, want the sign-in's", tok, err)
	}
	if got := mustRead(t, path); got != signedIn {
		t.Errorf("the overtaken reconnect rewrote the credential: %q", got)
	}
	if got := b.disposals.Load() - closedBefore; got != 1 {
		t.Errorf("the overtaken replacement was closed %d times, want exactly 1", got)
	}
	if sess.signInRefused() {
		t.Error("the session is refused after the sign-in")
	}
}

// A session that has no Web API token and whose stored refresh token Spotify
// refuses needs sign-in, remembers it, and keeps the credential.
func TestRefusedRestoreIsRemembered(t *testing.T) {
	path := storeCreds(t)
	before := mustRead(t, path)
	h := fakeSpotifyHTTP(t, tokenRevoked, apiOK)
	sess := &Session{clientID: "client"}
	p := New(sess, "client", 320)

	for i := range 3 {
		if err := p.ensureWebAPI(); !errors.Is(err, playlist.ErrNeedsAuth) {
			t.Fatalf("ensureWebAPI() #%d = %v, want ErrNeedsAuth", i+1, err)
		}
	}
	if got := h.tokenCalls.Load(); got != refusedRefresh {
		t.Errorf("token endpoint asked %d times, want one refresh (%d requests)", got, refusedRefresh)
	}
	if !sess.signInRefused() || mustRead(t, path) != before {
		t.Errorf("refused = %v, credential kept = %v; want both", sess.signInRefused(), mustRead(t, path) == before)
	}
}

// A 401 from the Web API. An access token that has gone stale is refreshed
// once and the read tried once more; a 401 that the fresh token gets too
// means the sign-in is refused. A write is never sent twice. Outages and
// rate limits are what they were.
func TestWebAPIUnauthorized(t *testing.T) {
	unauthorizedThenOK := func(n int, _ *http.Request) (int, http.Header) {
		if n == 1 {
			return http.StatusUnauthorized, nil
		}
		return http.StatusOK, nil
	}
	always := func(status int, header http.Header) func(int, *http.Request) (int, http.Header) {
		return func(int, *http.Request) (int, http.Header) { return status, header }
	}
	newProvider := func() (*SpotifyProvider, *Session) {
		sess := &Session{clientID: "client", tokenSource: tokenSourceFunc(goodToken)}
		return New(sess, "client", 320), sess
	}

	t.Run("a stale access token is refreshed once and the read retried", func(t *testing.T) {
		path := storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, unauthorizedThenOK)
		p, sess := newProvider()
		resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if err != nil {
			t.Fatalf("webAPI() = %v, want the retried read to succeed", err)
		}
		resp.Body.Close()
		if h.apiCalls.Load() != 2 || h.tokenCalls.Load() != 1 || sess.signInRefused() {
			t.Errorf("requests = %d, refreshes = %d, refused = %v; want 2, 1, false", h.apiCalls.Load(), h.tokenCalls.Load(), sess.signInRefused())
		}
		if !strings.Contains(mustRead(t, path), "silent-rotated") {
			t.Error("the rotated refresh token was not stored")
		}
	})
	t.Run("a 401 that survives the refresh needs sign-in", func(t *testing.T) {
		path := storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, always(http.StatusUnauthorized, nil))
		p, sess := newProvider()
		_, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if !errors.Is(err, playlist.ErrNeedsAuth) || !sess.signInRefused() {
			t.Fatalf("webAPI() = %v (refused %v), want ErrNeedsAuth and the session refused", err, sess.signInRefused())
		}
		if h.apiCalls.Load() != 2 || h.tokenCalls.Load() != 1 {
			t.Errorf("requests = %d, refreshes = %d; want 2 and 1: no loop", h.apiCalls.Load(), h.tokenCalls.Load())
		}
		if !credsExist(t, path) {
			t.Error("the credential was deleted")
		}
	})
	t.Run("a refresh Spotify refuses needs sign-in", func(t *testing.T) {
		path := storeCreds(t)
		before := mustRead(t, path)
		h := fakeSpotifyHTTP(t, tokenRevoked, always(http.StatusUnauthorized, nil))
		p, sess := newProvider()
		_, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if !errors.Is(err, playlist.ErrNeedsAuth) || !sess.signInRefused() || h.apiCalls.Load() != 1 {
			t.Errorf("webAPI() = %v (refused %v) after %d requests, want ErrNeedsAuth after 1", err, sess.signInRefused(), h.apiCalls.Load())
		}
		if mustRead(t, path) != before {
			t.Error("the credential changed")
		}
	})
	t.Run("a refresh that cannot reach Spotify is an outage", func(t *testing.T) {
		storeCreds(t)
		fakeSpotifyHTTP(t, func(int) (int, string) { return http.StatusServiceUnavailable, `no healthy upstream` }, always(http.StatusUnauthorized, nil))
		p, sess := newProvider()
		_, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if err == nil || errors.Is(err, playlist.ErrNeedsAuth) || sess.signInRefused() {
			t.Errorf("webAPI() = %v (refused %v), want an outage error and no sign-in", err, sess.signInRefused())
		}
	})
	t.Run("a write is not sent again", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, unauthorizedThenOK)
		p, sess := newProvider()
		_, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
			strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusCreated)
		if err == nil || h.apiCalls.Load() != 1 {
			t.Fatalf("the write = %v after %d requests, want an error and exactly 1 request", err, h.apiCalls.Load())
		}
		if errors.Is(err, playlist.ErrNeedsAuth) || sess.signInRefused() {
			t.Errorf("the write = %v (refused %v): the token was refreshed, so this is not a sign-in", err, sess.signInRefused())
		}
		// Asked again by the user, it goes through on the refreshed token.
		resp, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
			strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusOK)
		if err != nil || h.apiCalls.Load() != 2 {
			t.Fatalf("the write, asked again = %v after %d requests", err, h.apiCalls.Load())
		}
		resp.Body.Close()
	})
	t.Run("a write refused for the sign-in is not sent again either", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenRevoked, always(http.StatusUnauthorized, nil))
		p, sess := newProvider()
		_, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
			strings.NewReader(`{}`), "application/json", http.StatusCreated)
		if !errors.Is(err, playlist.ErrNeedsAuth) || !sess.signInRefused() || h.apiCalls.Load() != 1 {
			t.Errorf("the write = %v (refused %v) after %d requests, want ErrNeedsAuth after 1", err, sess.signInRefused(), h.apiCalls.Load())
		}
	})
	t.Run("503 and 429 are unchanged", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, always(http.StatusServiceUnavailable, nil))
		p, sess := newProvider()
		_, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if err == nil || errors.Is(err, playlist.ErrNeedsAuth) || h.tokenCalls.Load() != 0 || h.apiCalls.Load() != 1 {
			t.Errorf("503: webAPI() = %v after %d requests and %d refreshes, want a plain error, 1 and 0", err, h.apiCalls.Load(), h.tokenCalls.Load())
		}
		h = fakeSpotifyHTTP(t, tokenOK, always(http.StatusTooManyRequests, http.Header{"Retry-After": {"3600"}}))
		_, err = p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if _, ok := errors.AsType[*catalog.RateLimitError](err); !ok || errors.Is(err, playlist.ErrNeedsAuth) || h.tokenCalls.Load() != 0 {
			t.Errorf("429: webAPI() = %v after %d refreshes, want a RateLimitError and none", err, h.tokenCalls.Load())
		}
		if sess.signInRefused() {
			t.Error("an outage or a rate limit marked the sign-in refused")
		}
	})
}

// Concurrent sign-in, token failure and reads on one session, for the race
// detector: nothing here may deadlock or leave a refused session behind.
func TestSignInConcurrentWithRequests(t *testing.T) {
	storeCreds(t)
	stubBuild(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	sess := brokenSession()
	p := New(sess, "client", 320)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil); err == nil {
				resp.Body.Close()
			}
		})
		wg.Go(func() { _ = p.Authenticate() })
	}
	wg.Wait()
	if sess.signInRefused() {
		t.Error("the session is refused after the sign-ins")
	}
	if resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil); err != nil {
		t.Errorf("a read after the sign-ins = %v", err)
	} else {
		resp.Body.Close()
	}
}
