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
	"github.com/bjarneo/cliamp/provider"
)

// build stands in for Spotify under the real session builders.
type build struct {
	browserFlows, librespotSessions, playerInits, disposals atomic.Int32
	initErr                                                 error
	onInit                                                  func() // runs while the replacement is being finished
	storedGate                                              chan struct{}
	storedEntered                                           chan struct{}
}

// disposed is how many librespot sessions were torn down, once every retired
// playback bundle has been.
// torn is disposed for a test that is still holding a build: it waits for
// the bundle tails only.
func (b *build) torn() int32 {
	tails.Wait()
	return b.disposals.Load()
}

func (b *build) disposed() int32 {
	builds.Wait()
	tails.Wait()
	return b.disposals.Load()
}

func stubBuild(t *testing.T) *build {
	t.Helper()
	quiesce(t)
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
	settleBackground(t)
	return b
}

// spotifyHTTP stands in for the token endpoint and the Web API, each
// answering by request number, and counts both.
type spotifyHTTP struct {
	tokenCalls, apiCalls atomic.Int32
}

func fakeSpotifyHTTP(t *testing.T, token func(n int) (int, string), api func(n int, req *http.Request) (int, http.Header)) *spotifyHTTP {
	t.Helper()
	quiesce(t)
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
	settleBackground(t)
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
	s := goodSession()
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
	if b.disposed() != 1 {
		t.Errorf("the refused setup's session was closed %d times, want 1", b.disposed())
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
			if b.disposed() != b.librespotSessions.Load() {
				t.Errorf("%d librespot sessions were opened and %d closed", b.librespotSessions.Load(), b.disposed())
			}
		})
		t.Run(have+": quit while the replacement is finished", func(t *testing.T) {
			path := storeCreds(t)
			before := mustRead(t, path)
			b := stubBuild(t)
			fakeSpotifyHTTP(t, tokenRevoked, apiOK)
			p := newProvider()
			old := p.session
			oldToken := ""
			if old != nil {
				oldToken = tokenOf(old)
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
			if old != nil && (!old.viewNow().closed || tokenOf(old) != oldToken) {
				t.Error("the closed session adopted the replacement")
			}
			if b.disposed() < 1 {
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
	oldToken := tokenOf(old)
	p := New(old, "client", 320)

	done := make(chan error, 1)
	go func() { done <- p.Authenticate() }()
	<-entered
	p.Close()    // finishes first
	tails.Wait() // the closed session's own teardown; the build is still held
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
	if !old.viewNow().closed || tokenOf(old) != oldToken || p.session != nil {
		t.Error("the replacement was published into a closed session")
	}
	if got := b.disposed() - closedAt; got != 1 {
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
	stubRefresh(t, func(n int) (*oauth2.Token, error) {
		if n == 1 {
			close(entered)
			<-release // the slow request's refresh, still in flight during the sign-in
		}
		return nil, revoked
	})
	sess := expiredSession()
	p := New(sess, "client", 320)

	slow := make(chan error, 1)
	go func() {
		resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if resp != nil {
			resp.Body.Close()
		}
		slow <- err
	}()
	<-entered
	// Playback is refused meanwhile, and the user signs in again.
	sess.refuseSignIn(sess.generation())
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
	if _, err := p.webAPI(context.Background(), "GET", "/v1/me", nil); err != nil {
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
	sess := goodSession()
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
	closedBefore := b.torn()

	close(b.storedGate) // the silent rebuild finishes now
	if err := <-silent; err != nil {
		t.Errorf("the overtaken silent reconnect = %v, want nil: the session is already renewed", err)
	}
	if got := tokenOf(sess); got != "signed-in" {
		t.Errorf("token after the overtaken reconnect = %q, want the sign-in's", got)
	}
	if got := mustRead(t, path); got != signedIn {
		t.Errorf("the overtaken reconnect rewrote the credential: %q", got)
	}
	if got := b.disposed() - closedBefore; got != 1 {
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
	sess := tokenSession(nil)
	p := New(sess, "client", 320)

	for i := range 3 {
		if err := ensureWebAPI(p); !errors.Is(err, playlist.ErrNeedsAuth) {
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
		sess := goodSession()
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
		// One write, and one read-only check of the renewed token.
		if err == nil || h.apiCalls.Load() != 2 {
			t.Fatalf("the write = %v after %d requests, want an error after the write and one check", err, h.apiCalls.Load())
		}
		if errors.Is(err, playlist.ErrNeedsAuth) || sess.signInRefused() {
			t.Errorf("the write = %v (refused %v): the token was refreshed, so this is not a sign-in", err, sess.signInRefused())
		}
		// Asked again by the user, it goes through on the refreshed token.
		resp, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
			strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusOK)
		if err != nil || h.apiCalls.Load() != 3 {
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

// signedInSession is a session built and taken through the real builders,
// with its credential on disk.
func signedInSession(t *testing.T) (*SpotifyProvider, *Session) {
	t.Helper()
	p := New(nil, "client", 320)
	if err := p.Authenticate(); err != nil {
		t.Fatal(err)
	}
	return p, p.session
}

// Work that belongs to a sign-in the user has since replaced must not write
// the credential: neither a renewal that was still waiting for Spotify, nor
// the old token source's rotation callback.
func TestRetiredSignInCannotWriteCredential(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	path, _ := CredsPath()
	stubBuild(t)
	entered, release := make(chan struct{}), make(chan struct{})
	fakeSpotifyHTTP(t, func(n int) (int, string) {
		if n == 1 {
			close(entered)
			<-release // the old sign-in's renewal, held at Spotify
		}
		return http.StatusOK, `{"access_token":"late","token_type":"Bearer","refresh_token":"stale-rotation","expires_in":3600}`
	}, apiOK)
	p, sess := signedInSession(t)
	old := sess.viewNow().tokens // the first sign-in's coordinator

	renewal := make(chan error, 1)
	go func() { _, _, err := old.renew(context.Background(), lease{old, 1}); renewal <- err }()
	<-entered

	sess.refuseSignIn(sess.generation())
	if err := p.Authenticate(); err != nil { // the user signs in again
		t.Fatal(err)
	}
	signedIn := mustRead(t, path)

	close(release) // the old renewal's answer, with a rotated refresh token
	if err := <-renewal; !errors.Is(err, errSuperseded) {
		t.Errorf("the overtaken renewal = %v, want to be told a newer sign-in stands", err)
	}
	if got := mustRead(t, path); got != signedIn || strings.Contains(got, "stale-rotation") {
		t.Errorf("the old sign-in's renewal rewrote the credential:\n was %q\n now %q", signedIn, got)
	}
	old.setIdentity("stale", []byte("identity"), "dev")
	if got := mustRead(t, path); got != signedIn {
		t.Errorf("the replaced sign-in's coordinator rewrote the credential: %q", got)
	}
	if got := tokenOf(sess); got != "signed-in" {
		t.Errorf("token after the overtaken renewal = %q, want the new sign-in's", got)
	}

	// A closed session's coordinator writes nothing either.
	live := sess.viewNow().tokens
	p.Close()
	live.setIdentity("after", []byte("close"), "dev")
	if _, _, err := live.renew(context.Background(), lease{live, 1}); !errors.Is(err, errSuperseded) || mustRead(t, path) != signedIn {
		t.Errorf("a renewal after Close = %v and changed the credential: %v", err, mustRead(t, path) != signedIn)
	}
}

// A write that was waiting for its token when the user signed in again is
// not sent under the new sign-in: the user did not ask for it there. It ends
// with an error that is not a sign-in request, whether the old token request
// was refused or answered late. A read may go through.
func TestPendingWriteIsNotSentAfterSignIn(t *testing.T) {
	for _, late := range []string{"refused", "answered"} {
		t.Run("the old token request is "+late, func(t *testing.T) {
			storeCreds(t)
			stubBuild(t)
			h := fakeSpotifyHTTP(t, tokenOK, apiOK)
			entered, release := make(chan struct{}), make(chan struct{})
			stubRefresh(t, func(n int) (*oauth2.Token, error) {
				if n == 1 {
					close(entered)
					<-release
					if late == "answered" {
						return goodToken()
					}
				}
				return nil, &oauth2.RetrieveError{ErrorCode: "invalid_grant"}
			})
			sess := expiredSession()
			p := New(sess, "client", 320)

			write := make(chan error, 1)
			go func() {
				resp, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
					strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusOK, http.StatusCreated)
				if resp != nil {
					resp.Body.Close()
				}
				write <- err
			}()
			<-entered
			sess.refuseSignIn(sess.generation())
			if err := p.Authenticate(); err != nil {
				t.Fatal(err)
			}

			close(release)
			err := <-write
			if h.apiCalls.Load() != 0 {
				t.Fatalf("the pending write was sent %d times under the new sign-in, want 0", h.apiCalls.Load())
			}
			if err == nil || errors.Is(err, playlist.ErrNeedsAuth) || !errors.Is(err, errSignInChanged) {
				t.Errorf("the pending write = %v, want errSignInChanged and no sign-in request", err)
			}
			if sess.signInRefused() {
				t.Error("the new sign-in is refused")
			}
			// Asked again by the user, it is sent, once.
			resp, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
				strings.NewReader(`{}`), "application/json", http.StatusOK)
			if err != nil || h.apiCalls.Load() != 1 {
				t.Fatalf("the write, asked again = %v after %d requests", err, h.apiCalls.Load())
			}
			resp.Body.Close()
		})
	}
}

// The credential is read and rotated under one lock (for the race detector).
func TestCredentialSnapshotsAreLocked(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	stubBuild(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	_, sess := signedInSession(t)
	tokens := sess.viewNow().tokens

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() { tokens.setIdentity(fmt.Sprintf("user-%d", i), []byte("blob"), "dev") })
		wg.Go(func() { _, _, _ = tokens.renew(context.Background(), lease{tokens, uint64(i)}) })
		wg.Go(func() { _ = tokens.snapshot() })
		wg.Go(func() { _ = sess.needsSignIn() })
	}
	wg.Wait()
}

// apiByMethod answers each method by its own request count, and counts them.
type apiByMethod struct {
	mu     sync.Mutex
	counts map[string]int
	answer func(method string, n int) int
}

func (a *apiByMethod) handle(_ int, req *http.Request) (int, http.Header) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.counts == nil {
		a.counts = map[string]int{}
	}
	a.counts[req.Method]++
	return a.answer(req.Method, a.counts[req.Method]), nil
}

func (a *apiByMethod) sent(method string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.counts[method]
}

// A write that gets a 401 is never sent again, and it still finds out what
// the 401 meant: the renewed token is tried on one read. If that is refused
// too, the write reports sign-in required, at once, not at some later read.
func TestWriteUnauthorizedFindsOutWithoutReplay(t *testing.T) {
	tests := []struct {
		name      string
		probe     int // what the read-only check gets
		needsAuth bool
	}{
		{"the renewed token is refused too", http.StatusUnauthorized, true},
		{"the renewed token works", http.StatusOK, false},
		{"the check cannot reach Spotify", http.StatusServiceUnavailable, false},
		{"the check is rate limited", http.StatusTooManyRequests, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := storeCreds(t)
			api := &apiByMethod{answer: func(method string, _ int) int {
				if method == http.MethodPost {
					return http.StatusUnauthorized
				}
				return tt.probe
			}}
			h := fakeSpotifyHTTP(t, tokenOK, api.handle)
			sess := goodSession()
			p := New(sess, "client", 320)

			_, err := p.webAPIWithBody(context.Background(), "POST", "/v1/playlists/pl/tracks", nil,
				strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusCreated)
			if err == nil || errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth || sess.signInRefused() != tt.needsAuth {
				t.Fatalf("the write = %v (refused %v), want an error, needing sign-in %v", err, sess.signInRefused(), tt.needsAuth)
			}
			if api.sent(http.MethodPost) != 1 {
				t.Errorf("the write was sent %d times, want 1", api.sent(http.MethodPost))
			}
			if api.sent(http.MethodGet) != 1 || h.tokenCalls.Load() != 1 {
				t.Errorf("read-only checks = %d, renewals = %d; want 1 and 1: bounded", api.sent(http.MethodGet), h.tokenCalls.Load())
			}
			if !credsExist(t, path) {
				t.Error("the credential was deleted")
			}
		})
	}
}

// A sign-in that was published and whose credential was written has
// succeeded, and says so, even when ddsonic quits the moment after.
func TestSignInThatCommittedReportsSuccess(t *testing.T) {
	path := storeCreds(t)
	b := stubBuild(t)
	fakeSpotifyHTTP(t, tokenRevoked, apiOK)
	sess := brokenSession()
	p := New(sess, "client", 320)
	// Close arrives while the old session is being torn down: after the
	// replacement is in place and its credential written.
	teardown := closeLibrespot
	closeLibrespot = func(pl *librespotPlayer.Player, s *session.Session) {
		teardown(pl, s)
		if b.disposals.Load() == 1 {
			p.Close()
		}
	}

	err := p.Authenticate()
	if !strings.Contains(mustRead(t, path), "new-refresh") {
		t.Fatal("the sign-in's credential was not written; the test did not reach the case")
	}
	if err != nil {
		t.Errorf("Authenticate() = %v for a sign-in that was published and stored", err)
	}
}

// waitFor polls until ok, for what another goroutine is about to do.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 400 {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A write belongs to the sign-in it was asked under, for all of it: the
// second request of a write made of two included.
// When the sign-in changes meanwhile it ends unsent, and the user asks again.
func TestWriteStaysWithItsSignIn(t *testing.T) {
	post := func(p *SpotifyProvider, ctx context.Context) error {
		resp, err := p.webAPIWithBody(ctx, "POST", "/v1/playlists/pl/tracks", nil,
			strings.NewReader(`{"uris":["spotify:track:1"]}`), "application/json", http.StatusOK, http.StatusCreated)
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}
	// ddsonic: a write that is rate limited is not sent again (it used to
	// be, after a wait in which the sign-in could change).
	t.Run("a rate limit ends the write, sent once", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, func(n int, _ *http.Request) (int, http.Header) {
			if n == 1 {
				return http.StatusTooManyRequests, http.Header{"Retry-After": {"1"}}
			}
			return http.StatusOK, nil
		})
		p := New(goodSession(), "client", 320)
		if err := post(p, context.Background()); !errors.As(err, new(*catalog.RateLimitError)) || h.apiCalls.Load() != 1 {
			t.Errorf("the write = %v after %d requests, want a RateLimitError after 1", err, h.apiCalls.Load())
		}
	})
	t.Run("a write made of two requests", func(t *testing.T) {
		storeCreds(t)
		stubBuild(t)
		h := fakeSpotifyHTTP(t, tokenOK, apiOK)
		sess := goodSession()
		p := New(sess, "client", 320)

		ctx := p.WithSignIn(context.Background()) // create a playlist, then add to it
		if err := post(p, ctx); err != nil {
			t.Fatal(err)
		}
		sess.refuseSignIn(sess.generation())
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
		if err := post(p, ctx); !errors.Is(err, errSignInChanged) || h.apiCalls.Load() != 1 {
			t.Errorf("the second request = %v after %d requests in all, want errSignInChanged and 1", err, h.apiCalls.Load())
		}
		// A new action is a new context, and is sent.
		if err := post(p, p.WithSignIn(context.Background())); err != nil || h.apiCalls.Load() != 2 {
			t.Errorf("the write, asked again = %v after %d requests", err, h.apiCalls.Load())
		}
	})
}

// An answer that arrives after the user has signed in again is about the old
// sign-in, whatever it says: a renewal Spotify refuses, or a 401 for the
// renewed token. A read then goes through on the new sign-in and the session
// stays working; a write ends unsent.
func TestLateRefusalsAreAboutTheOldSignIn(t *testing.T) {
	type gate struct{ entered, release chan struct{} }
	newGate := func() gate { return gate{make(chan struct{}), make(chan struct{})} }
	reauth := func(t *testing.T, p *SpotifyProvider, sess *Session) {
		t.Helper()
		sess.refuseSignIn(sess.generation())
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
	}
	call := func(p *SpotifyProvider, method string) error {
		var body io.Reader
		if method != "GET" {
			body = strings.NewReader(`{}`)
		}
		resp, err := p.webAPIWithBody(context.Background(), method, "/v1/me", nil, body, "application/json", http.StatusOK)
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}

	for _, method := range []string{"GET", "POST"} {
		t.Run(method+": the renewal is refused late", func(t *testing.T) {
			storeCreds(t)
			stubBuild(t)
			g := newGate()
			api := &apiByMethod{answer: func(_ string, n int) int {
				if n == 1 {
					return http.StatusUnauthorized
				}
				return http.StatusOK
			}}
			fakeSpotifyHTTP(t, func(n int) (int, string) {
				if n == 1 {
					close(g.entered)
					<-g.release
				}
				return tokenRevoked(n)
			}, api.handle)
			sess := goodSession()
			p := New(sess, "client", 320)

			result := make(chan error, 1)
			go func() { result <- call(p, method) }()
			<-g.entered
			reauth(t, p, sess)
			close(g.release)
			err := <-result

			if sess.signInRefused() || errors.Is(err, playlist.ErrNeedsAuth) {
				t.Fatalf("%s = %v, session refused = %v: the old sign-in's refusal reached the new one", method, err, sess.signInRefused())
			}
			if method == "GET" {
				if err != nil || api.sent("GET") != 2 {
					t.Errorf("the read = %v after %d requests, want it through on the new sign-in", err, api.sent("GET"))
				}
			} else if !errors.Is(err, errSignInChanged) || api.sent("POST") != 1 {
				t.Errorf("the write = %v after %d requests, want errSignInChanged and 1", err, api.sent("POST"))
			}
		})
	}
	t.Run("GET: the renewed token's 401 arrives late", func(t *testing.T) {
		storeCreds(t)
		stubBuild(t)
		g := newGate()
		var n atomic.Int32
		fakeSpotifyHTTP(t, tokenOK, func(int, *http.Request) (int, http.Header) {
			switch n.Add(1) {
			case 1:
				return http.StatusUnauthorized, nil
			case 2: // the retry with the renewed token, answered after the sign-in
				close(g.entered)
				<-g.release
				return http.StatusUnauthorized, nil
			}
			return http.StatusOK, nil
		})
		sess := goodSession()
		p := New(sess, "client", 320)

		result := make(chan error, 1)
		go func() { result <- call(p, "GET") }()
		<-g.entered
		reauth(t, p, sess)
		close(g.release)
		if err := <-result; err != nil || sess.signInRefused() {
			t.Errorf("the read = %v, session refused = %v; want it through on the new sign-in", err, sess.signInRefused())
		}
		if n.Load() != 3 {
			t.Errorf("requests = %d, want 3: one more on the new sign-in, and no loop", n.Load())
		}
	})
	t.Run("an outage during the renewal stays an outage", func(t *testing.T) {
		storeCreds(t)
		api := &apiByMethod{answer: func(string, int) int { return http.StatusUnauthorized }}
		fakeSpotifyHTTP(t, func(int) (int, string) { return http.StatusServiceUnavailable, `no healthy upstream` }, api.handle)
		sess := goodSession()
		p := New(sess, "client", 320)
		if err := call(p, "GET"); err == nil || errors.Is(err, playlist.ErrNeedsAuth) || errors.Is(err, errSignInChanged) || sess.signInRefused() {
			t.Errorf("the read = %v (refused %v), want an outage error", err, sess.signInRefused())
		}
	})
}

// An action on the account belongs to the sign-in that stood when it began
// (WithSignIn), for all of it. A sign-in the user completes afterwards is not
// a new request for the action: its writes are not sent under it, whether
// the sign-in comes before the action's first write or between two of them,
// and whether or not the provider had a session when the action began. What
// may happen on the way is the silent setup of the stored session, which
// changes no sign-in. The provider's own CreatePlaylist and
// AddTrackToPlaylist are used, over the real builders.
func TestActionKeepsItsSignInFromTheStart(t *testing.T) {
	track := playlist.Track{Path: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Title: "Blue in Green"}
	setup := func(t *testing.T) (*build, *apiByMethod) {
		t.Helper()
		storeCreds(t)
		b := stubBuild(t)
		api := &apiByMethod{answer: func(string, int) int { return http.StatusOK }}
		fakeSpotifyHTTP(t, tokenOK, api.handle)
		return b, api
	}
	reauth := func(t *testing.T, p *SpotifyProvider) {
		t.Helper()
		p.session.refuseSignIn(p.session.generation())
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
	}
	established := func() *SpotifyProvider {
		return New(goodSession(), "client", 320)
	}
	wantUnsent := func(t *testing.T, what string, err error, api *apiByMethod, writes int) {
		t.Helper()
		if !errors.Is(err, errSignInChanged) || api.sent("POST") != writes {
			t.Fatalf("%s = %v after %d writes in all, want errSignInChanged and %d", what, err, api.sent("POST"), writes)
		}
	}

	t.Run("a session, and a sign-in before the first write", func(t *testing.T) {
		_, api := setup(t)
		p := established()
		ctx := p.WithSignIn(context.Background())
		reauth(t, p)
		_, err := p.CreatePlaylist(ctx, "New")
		wantUnsent(t, "the action's first request", err, api, 0)
	})
	t.Run("no session, and a sign-in before the first write", func(t *testing.T) {
		_, api := setup(t)
		p := New(nil, "client", 320)
		ctx := p.WithSignIn(context.Background())
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
		_, err := p.CreatePlaylist(ctx, "New")
		wantUnsent(t, "the action's first request", err, api, 0)
	})
	t.Run("no session, the stored one set up, then a sign-in between the writes", func(t *testing.T) {
		_, api := setup(t)
		p := New(nil, "client", 320)
		ctx := p.WithSignIn(context.Background())
		if _, err := p.CreatePlaylist(ctx, "New"); err != nil { // sets the stored session up
			t.Fatal(err)
		}
		reauth(t, p)
		wantUnsent(t, "the action's second request", p.AddTrackToPlaylist(ctx, "pl", track), api, 1)
		if err := p.AddTrackToPlaylist(p.WithSignIn(context.Background()), "pl", track); err != nil || api.sent("POST") != 2 {
			t.Errorf("asked again = %v after %d writes", err, api.sent("POST"))
		}
	})
	t.Run("the stored session's setup overlapping a sign-in", func(t *testing.T) {
		b, api := setup(t)
		b.storedGate, b.storedEntered = make(chan struct{}), make(chan struct{})
		joined := make(chan struct{})
		storedJoined = onceFunc(func() { close(joined) })
		t.Cleanup(func() { storedJoined = nil })
		p := New(nil, "client", 320)
		ctx := p.WithSignIn(context.Background())

		created := make(chan error, 1)
		go func() { _, err := p.CreatePlaylist(ctx, "New"); created <- err }()
		<-b.storedEntered
		// The user signs in while the setup runs. The sign-in and the setup
		// are the one build from the stored credential; when it is taken it
		// is that sign-in's, and the action began before it.
		signedIn := make(chan error, 1)
		go func() { signedIn <- p.Authenticate() }()
		<-joined
		close(b.storedGate)
		if err := <-signedIn; err != nil {
			t.Fatal(err)
		}
		wantUnsent(t, "the action's first request", <-created, api, 0)
		wantUnsent(t, "the action's second request", p.AddTrackToPlaylist(ctx, "pl", track), api, 0)
	})
	t.Run("a session, unchanged", func(t *testing.T) {
		_, api := setup(t)
		p := established()
		ctx := p.WithSignIn(context.Background())
		if _, err := p.CreatePlaylist(ctx, "New"); err != nil {
			t.Fatal(err)
		}
		if err := p.AddTrackToPlaylist(ctx, "pl", track); err != nil || api.sent("POST") != 2 {
			t.Errorf("the second request = %v after %d writes, want both sent", err, api.sent("POST"))
		}
	})
	t.Run("no session, the stored one set up, nothing else", func(t *testing.T) {
		_, api := setup(t)
		p := New(nil, "client", 320)
		ctx := p.WithSignIn(context.Background())
		if _, err := p.CreatePlaylist(ctx, "New"); err != nil {
			t.Fatal(err)
		}
		if err := p.AddTrackToPlaylist(ctx, "pl", track); err != nil || api.sent("POST") != 2 {
			t.Errorf("the second request = %v after %d writes, want both sent", err, api.sent("POST"))
		}
	})
}

// A single write made through the provider's own CreatePlaylist or
// AddTrackToPlaylist, with an ordinary context, is an action from the moment
// it is called: before the stored session is set up, and before any other
// wait. A sign-in the user completes while that setup runs ends it unsent.
// (Adapted from the reviewer's round-6 probe.)
func TestSingleWritesAreActionsFromTheCall(t *testing.T) {
	for _, kind := range []string{"create", "add"} {
		t.Run(kind+": a sign-in during the stored session's setup", func(t *testing.T) {
			storeCreds(t)
			stubBuild(t)
			entered, release := make(chan struct{}), make(chan struct{})
			var made atomic.Int32
			original := newLibrespotSession
			newLibrespotSession = func(ctx context.Context, opts *session.Options) (*session.Session, error) {
				if _, stored := opts.Credentials.(session.StoredCredentials); stored && made.Add(1) == 1 {
					close(entered)
					<-release // the write's own setup, held
				}
				return original(ctx, opts)
			}
			h := fakeSpotifyHTTP(t, tokenOK, apiOK)
			joined := make(chan struct{})
			storedJoined = onceFunc(func() { close(joined) })
			t.Cleanup(func() { storedJoined = nil })
			p := New(nil, "client", 320)

			done := make(chan error, 1)
			go func() {
				if kind == "create" {
					_, err := p.CreatePlaylist(context.Background(), "New")
					done <- err
				} else {
					done <- p.AddTrackToPlaylist(context.Background(), "pl", playlist.Track{Path: "spotify:track:one"})
				}
			}()
			<-entered
			// The sign-in shares the write's own setup, and takes it.
			signedIn := make(chan error, 1)
			go func() { signedIn <- p.Authenticate() }()
			<-joined
			close(release)
			if err := <-signedIn; err != nil {
				t.Fatal(err)
			}
			err := <-done
			if !errors.Is(err, errSignInChanged) || h.apiCalls.Load() != 0 {
				t.Fatalf("the %s = %v after %d requests, want errSignInChanged and none sent", kind, err, h.apiCalls.Load())
			}
		})
		t.Run(kind+": the stored session's setup alone", func(t *testing.T) {
			storeCreds(t)
			stubBuild(t)
			h := fakeSpotifyHTTP(t, tokenOK, apiOK)
			p := New(nil, "client", 320)
			var err error
			if kind == "create" {
				_, err = p.CreatePlaylist(context.Background(), "New")
			} else {
				err = p.AddTrackToPlaylist(context.Background(), "pl", playlist.Track{Path: "spotify:track:one"})
			}
			if err != nil || h.apiCalls.Load() != 1 {
				t.Errorf("the %s = %v after %d requests, want it sent once: silent setup is no sign-in", kind, err, h.apiCalls.Load())
			}
		})
	}
}

// A write that got a 401 was refused: it did not take effect, whatever
// happens next. When the renewal that follows cannot reach Spotify, the
// error still says so (provider.NotApplied), and the outage stays an outage.
// The write is sent once. (Adapted from the reviewer's round-6 probe.)
func TestRefusedWriteStaysNotAppliedThroughAnOutage(t *testing.T) {
	post := func(p *SpotifyProvider) error {
		resp, err := p.webAPIWithBody(context.Background(), "POST", "/v1/me/playlists", nil, strings.NewReader(`{}`), "application/json", http.StatusOK)
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}
	t.Run("the renewal cannot reach Spotify", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, func(int) (int, string) { return http.StatusServiceUnavailable, "unavailable" },
			func(int, *http.Request) (int, http.Header) { return http.StatusUnauthorized, nil })
		sess := goodSession()
		err := post(New(sess, "client", 320))
		if err == nil || h.apiCalls.Load() != 1 {
			t.Fatalf("the write = %v after %d requests, want an error after one", err, h.apiCalls.Load())
		}
		if errors.Is(err, playlist.ErrNeedsAuth) || sess.signInRefused() {
			t.Errorf("the outage was taken for a refused sign-in: %v", err)
		}
		if !provider.NotApplied(err) {
			t.Errorf("the refused write is reported as uncertain: %v", err)
		}
		if !strings.Contains(err.Error(), "503") && !strings.Contains(err.Error(), "unavailable") {
			t.Errorf("the error %q lost the renewal's cause", err)
		}
	})
	t.Run("the check of the renewed token cannot reach Spotify", func(t *testing.T) {
		storeCreds(t)
		api := &apiByMethod{answer: func(method string, _ int) int {
			if method == http.MethodPost {
				return http.StatusUnauthorized
			}
			return http.StatusBadGateway
		}}
		fakeSpotifyHTTP(t, tokenOK, api.handle)
		sess := goodSession()
		err := post(New(sess, "client", 320))
		if err == nil || api.sent("POST") != 1 || errors.Is(err, playlist.ErrNeedsAuth) || !provider.NotApplied(err) {
			t.Errorf("the write = %v after %d writes; want one write, no sign-in, and not applied", err, api.sent("POST"))
		}
	})
}

func storedRefreshToken(t *testing.T) string {
	t.Helper()
	creds, err := loadCreds()
	if err != nil {
		t.Fatal(err)
	}
	return creds.RefreshToken
}

func tokenAnswer(access, refresh string) (int, string) {
	return http.StatusOK, fmt.Sprintf(`{"access_token":%q,"token_type":"Bearer","refresh_token":%q,"expires_in":3600}`, access, refresh)
}

// The catalog's background album read answers a 401 as every read does: the
// token is renewed once and the read sent once more; a 401 the renewed token
// gets too is sign-in required. It keeps its own rule for rate limits: none
// is waited for. (The first case is adapted from the reviewer's round-7
// probe.)
func TestBackgroundAlbumReadRecoversFrom401(t *testing.T) {
	read := func(p *SpotifyProvider) error {
		_, err := p.AlbumTrackRecordsOnce(context.Background(), "album")
		return err
	}
	healthy := func() (*SpotifyProvider, *Session) {
		s := goodSession()
		return New(s, "client", 320), s
	}
	always := func(status int, header http.Header) func(int, *http.Request) (int, http.Header) {
		return func(int, *http.Request) (int, http.Header) { return status, header }
	}

	t.Run("a 401 that survives the renewal needs sign-in", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, always(http.StatusUnauthorized, nil))
		p, s := healthy()
		err := read(p)
		if !errors.Is(err, playlist.ErrNeedsAuth) || !s.signInRefused() {
			t.Fatalf("the read = %v (refused %v), want ErrNeedsAuth and the session refused", err, s.signInRefused())
		}
		if h.apiCalls.Load() != 2 || h.tokenCalls.Load() != 1 {
			t.Errorf("requests = %d, renewals = %d; want 2 and 1", h.apiCalls.Load(), h.tokenCalls.Load())
		}
	})
	t.Run("a first 401 alone refuses nothing: the renewed token is tried", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, func(n int, _ *http.Request) (int, http.Header) {
			if n == 1 {
				return http.StatusUnauthorized, nil
			}
			return http.StatusOK, nil
		})
		p, s := healthy()
		err := read(p)
		if errors.Is(err, playlist.ErrNeedsAuth) || s.signInRefused() {
			t.Fatalf("the read = %v (refused %v): one 401 was taken for a revoked sign-in", err, s.signInRefused())
		}
		if h.apiCalls.Load() < 2 || h.tokenCalls.Load() != 1 {
			t.Errorf("requests = %d, renewals = %d; want the read sent again after one renewal", h.apiCalls.Load(), h.tokenCalls.Load())
		}
	})
	t.Run("403, 503 and an outage are what they were", func(t *testing.T) {
		for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
			storeCreds(t)
			h := fakeSpotifyHTTP(t, tokenOK, always(status, nil))
			p, s := healthy()
			err := read(p)
			var se *StatusError
			if !errors.As(err, &se) || errors.Is(err, playlist.ErrNeedsAuth) || s.signInRefused() || h.apiCalls.Load() != 1 || h.tokenCalls.Load() != 0 {
				t.Errorf("%d: the read = %v after %d requests and %d renewals (refused %v); want a status error, 1 and 0", status, err, h.apiCalls.Load(), h.tokenCalls.Load(), s.signInRefused())
			}
		}
		storeCreds(t)
		fakeSpotifyHTTP(t, func(int) (int, string) { return http.StatusServiceUnavailable, "unavailable" }, always(http.StatusUnauthorized, nil))
		p, s := healthy()
		if err := read(p); err == nil || errors.Is(err, playlist.ErrNeedsAuth) || s.signInRefused() {
			t.Errorf("a renewal that cannot reach Spotify: the read = %v (refused %v), want an outage", err, s.signInRefused())
		}
	})
	t.Run("a rate limit is not waited for", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, always(http.StatusTooManyRequests, http.Header{"Retry-After": {"1"}}))
		p, s := healthy()
		start := time.Now()
		err := read(p)
		if _, ok := errors.AsType[*catalog.RateLimitError](err); !ok || h.apiCalls.Load() != 1 || time.Since(start) > 500*time.Millisecond || s.signInRefused() {
			t.Errorf("the read = %v after %d requests in %v; want a RateLimitError at once, after 1", err, h.apiCalls.Load(), time.Since(start))
		}
	})
	t.Run("a 401 for the old sign-in, after the user signed in again", func(t *testing.T) {
		storeCreds(t)
		stubBuild(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var n atomic.Int32
		fakeSpotifyHTTP(t, tokenOK, func(int, *http.Request) (int, http.Header) {
			if n.Add(1) == 1 {
				close(entered)
				<-release
				return http.StatusUnauthorized, nil
			}
			return http.StatusOK, nil
		})
		p, s := healthy()
		done := make(chan error, 1)
		go func() { done <- read(p) }()
		<-entered
		s.refuseSignIn(s.generation())
		if err := p.Authenticate(); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-done; errors.Is(err, playlist.ErrNeedsAuth) || s.signInRefused() {
			t.Errorf("the read = %v (refused %v): the old sign-in's 401 reached the new one", err, s.signInRefused())
		}
	})
}

// expiredStagedSession is a session a provider took, whose access token has
// run out, so its next call refreshes it.
func expiredStagedSession(t *testing.T) *Session {
	t.Helper()
	s := builtSession("client", nil, storedCreds{Username: "user", Data: []byte("blob"), DeviceID: "dev", RefreshToken: "original"},
		&oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)})
	s.accept()
	return s
}

// A 401 that arrives for a token the session has renewed meanwhile is about
// the old token: the read is sent again on the one that stands, and nothing
// is refused. (The orderings this test used to hold beside it, a refresh in
// the air while a second, forced one overtook it, cannot be made any more:
// there is one refresh at a time. TestProductionCallersShareOneRefresh holds
// what they protected.)
func TestStaleRefreshDoesNotRefuseRenewedToken(t *testing.T) {
	storeCreds(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var n atomic.Int32
	fakeSpotifyHTTP(t, tokenOK, func(int, *http.Request) (int, http.Header) {
		if n.Add(1) == 1 { // sent with the old token; answered after the renewal
			close(entered)
			<-release
			return http.StatusUnauthorized, nil
		}
		return http.StatusOK, nil
	})
	sess := goodSession()
	p := New(sess, "client", 320)

	result := make(chan error, 1)
	go func() {
		resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	<-entered
	tokens := sess.viewNow().tokens
	if _, _, err := tokens.renew(context.Background(), lease{tokens, 1}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; err != nil || sess.signInRefused() || n.Load() != 2 {
		t.Errorf("the read = %v (refused %v) after %d requests; want it through on the renewed token, in 2", err, sess.signInRefused(), n.Load())
	}
}

// A refusal of the whole sign-in, from playback, is not about a token: a
// token renewed afterwards leaves it standing.
func TestPlaybackRefusalStaysThroughANewToken(t *testing.T) {
	storeCreds(t)
	forcedEntered, forcedRelease := make(chan struct{}), make(chan struct{})
	fakeSpotifyHTTP(t, func(n int) (int, string) {
		if n == 1 {
			close(forcedEntered)
			<-forcedRelease
		}
		return tokenOK(n)
	}, apiOK)
	s := expiredStagedSession(t)
	tokens := s.viewNow().tokens

	forced := make(chan error, 1)
	go func() { _, _, err := tokens.renew(context.Background(), lease{tokens, 1}); forced <- err }()
	<-forcedEntered
	s.refuseSignIn(s.generation()) // playback: the sign-in itself is refused
	close(forcedRelease)
	if err := <-forced; err != nil {
		t.Fatal(err)
	}
	if !s.signInRefused() || !s.needsSignIn() {
		t.Error("a new token undid a refusal of the whole sign-in")
	}
	if _, err := New(s, "client", 320).webAPI(context.Background(), "GET", "/v1/me", nil); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("a read on a session refused for playback = %v, want playlist.ErrNeedsAuth", err)
	}
}

// A sign-in's coordinator writes the refresh token Spotify rotates while it
// is the sign-in's, and nothing once its session was closed or replaced.
func TestOnlyTheStandingSignInRotatesTheCredential(t *testing.T) {
	t.Run("the session's own refresh rotates", func(t *testing.T) {
		storeCreds(t)
		fakeSpotifyHTTP(t, func(int) (int, string) { return tokenAnswer("auto-access", "rotated-by-the-refresh") }, apiOK)
		s := expiredStagedSession(t)
		resp, err := New(s, "client", 320).webAPI(context.Background(), "GET", "/v1/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := storedRefreshToken(t); got != "rotated-by-the-refresh" {
			t.Errorf("stored refresh token = %q, want the one the refresh was given", got)
		}
	})
	t.Run("a session published by a sign-in rotates as itself", func(t *testing.T) {
		t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
		stubBuild(t)
		fakeSpotifyHTTP(t, func(int) (int, string) { return tokenAnswer("auto-access", "rotated-after-sign-in") }, apiOK)
		old := brokenSession()
		p := New(old, "client", 320)
		if err := p.Authenticate(); err != nil { // the built session's coordinator is old's now
			t.Fatal(err)
		}
		tokens := old.viewNow().tokens
		// The published token runs out; the coordinator refreshes and rotates.
		tokens.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		resp, err := p.webAPI(context.Background(), "GET", "/v1/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := storedRefreshToken(t); got != "rotated-after-sign-in" || old.viewNow().tokens != tokens {
			t.Errorf("stored refresh token = %q, want the published coordinator's own rotation", got)
		}
	})
	t.Run("a coordinator whose session was closed or replaced writes nothing", func(t *testing.T) {
		for _, ending := range []string{"closed", "replaced by a sign-in"} {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			stubBuild(t)
			entered, release := make(chan struct{}), make(chan struct{})
			fakeSpotifyHTTP(t, func(n int) (int, string) {
				if n == 1 {
					close(entered)
					<-release
				}
				return tokenAnswer("late-access", "late-rotation")
			}, apiOK)
			p, s := signedInSession(t)
			tokens := s.viewNow().tokens

			done := make(chan struct{})
			go func() { _, _, _ = tokens.renew(context.Background(), lease{tokens, 1}); close(done) }()
			<-entered
			if ending == "closed" {
				p.Close()
			} else {
				s.refuseSignIn(s.generation())
				if err := p.Authenticate(); err != nil {
					t.Fatal(err)
				}
			}
			before := storedRefreshToken(t)
			close(release)
			<-done
			if got := storedRefreshToken(t); got != before || got == "late-rotation" {
				t.Errorf("%s: the old sign-in's refresh rewrote the stored credential to %q", ending, got)
			}
		}
	})
}
