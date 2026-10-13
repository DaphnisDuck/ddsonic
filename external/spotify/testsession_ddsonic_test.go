package spotify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// tokenSession is a signed-in session for a test: its coordinator holds tok,
// and the stored credential when the test stored one (storeCreds), which it
// then owns, as a session the provider took would.
func tokenSession(tok *oauth2.Token) *Session {
	creds := storedCreds{}
	stored, err := loadCreds()
	if err == nil {
		creds = *stored
	}
	s := builtSession("client", nil, creds, tok)
	if err == nil {
		s.accept()
	}
	return s
}

// staticSession is a session whose token never expires and cannot be renewed.
func staticSession() *Session { return tokenSession(&oauth2.Token{AccessToken: "token"}) }

// goodSession is a session with a token that is good for an hour.
func goodSession() *Session {
	tok, _ := goodToken()
	return tokenSession(tok)
}

// tokenOf is the session's current access token, or "".
func tokenOf(s *Session) string {
	v := s.viewNow()
	if v.tokens == nil {
		return ""
	}
	v.tokens.mu.Lock()
	defer v.tokens.mu.Unlock()
	return v.tokens.access
}

// settleBackground waits, at the end of a test, for the build workers and
// bundle tails it started: they call the package's stand-ins, which the test
// is about to take away. Register it after the stand-ins' own cleanup, so
// that it runs first.
func settleBackground(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { quiesce(t) })
}

// quiesce waits for the build workers and bundle tails that are running. A
// helper that replaces a stand-in calls it first: those goroutines read the
// stand-ins.
func quiesce(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { builds.Wait(); tails.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Error("a build worker or a bundle tail is still running")
	}
}

// expiredSession is a session whose token has expired: its next use refreshes
// it, from the stored credential's refresh token.
func expiredSession() *Session {
	return tokenSession(&oauth2.Token{AccessToken: "expired", Expiry: time.Now().Add(-time.Hour)})
}

// stubRefresh makes every token refresh answer with what answer returns for
// the n-th one, and counts them. It stands in below the coordinator, where
// the token endpoint is.
func stubRefresh(t *testing.T, answer func(n int) (*oauth2.Token, error)) *atomic.Int32 {
	t.Helper()
	calls := new(atomic.Int32)
	orig := refreshTokens
	refreshTokens = func(context.Context, string, string) (*oauth2.Token, error) {
		return answer(int(calls.Add(1)))
	}
	t.Cleanup(func() { refreshTokens = orig })
	return calls
}

// ensureWebAPI is what an unattended Web API caller goes through before it
// sends anything: a session, and a token from its coordinator.
func ensureWebAPI(p *SpotifyProvider) error {
	if err := p.ensureSession(); err != nil {
		return err
	}
	v := p.sessionNow().viewNow()
	switch {
	case v.playbackRefused:
		return errSignInRefused
	case v.tokens == nil:
		return errNoToken()
	}
	_, _, err := v.tokens.token(context.Background())
	return err
}
