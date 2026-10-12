package spotify

// ddsonic: a Spotify sign-in that stops working while ddsonic runs.
//
// Upstream treats a session that exists as a signed-in one: Authenticate
// returns at once when the provider has a session, so a session whose refresh
// token Spotify has revoked can never be signed in again without a restart,
// and the refusal itself reaches callers as a plain error. Here the session
// remembers the refusal (authDead), Web API calls report it as
// playlist.ErrNeedsAuth, and an explicit sign-in replaces the session.

import (
	"context"
	"fmt"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"

	"github.com/bjarneo/cliamp/playlist"
)

// errSignInRefused is what a Web API call returns once Spotify has refused
// the session's refresh token.
var errSignInRefused = fmt.Errorf("spotify: the sign-in is no longer accepted: %w", playlist.ErrNeedsAuth)

// signInTimeout bounds one interactive sign-in.
const signInTimeout = 5 * time.Minute

// The session's builders and librespot calls, as variables so a test can
// stand in for Spotify.
var (
	// newSessionInteractive signs in for a provider that has no session.
	newSessionInteractive = NewSession
	// buildInteractiveSession signs in anew for ReconnectInteractive.
	buildInteractiveSession = newInteractiveSession
	sessionNewStream        = func(s *Session, ctx context.Context, id librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
		return s.NewStream(ctx, id, bitrate)
	}
	sessionReconnect = func(s *Session, ctx context.Context) error { return s.Reconnect(ctx) }
	// closeLibrespot tears down the player and session a reconnect replaced.
	closeLibrespot = func(p *librespotPlayer.Player, sess *session.Session) {
		if p != nil {
			p.Close()
		}
		if sess != nil {
			sess.Close()
		}
	}
)

// needsSignIn reports whether the session cannot call the Web API until the
// user signs in again: Spotify refused its sign-in, or it has no token.
func (s *Session) needsSignIn() bool {
	if s.authDead.Load() {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenSource == nil
}

// signInCall is one interactive sign-in, shared by every caller that asks
// while it runs.
type signInCall struct {
	done chan struct{}
	err  error
}

// signIn is Authenticate. It signs in when there is no session, or when the
// session needs it; a healthy session is left alone. One browser flow runs at
// a time: a second caller waits for the first's result.
//
// A broken session is replaced in place (Session.ReconnectInteractive): the
// new one is built first, so a sign-in that fails or is cancelled leaves the
// old session, its auth-dead mark and the stored credential as they were, and
// what a successful one replaces is closed at once. A stream opened before
// the swap reads its audio over its own connection and is not cut.
func (p *SpotifyProvider) signIn() error {
	p.mu.Lock()
	sess := p.session
	if sess != nil && !sess.needsSignIn() {
		p.mu.Unlock()
		return nil
	}
	if call := p.signingIn; call != nil {
		p.mu.Unlock()
		<-call.done
		return call.err
	}
	clientID := p.clientID
	if clientID == "" {
		p.mu.Unlock()
		return fmt.Errorf("spotify: no client ID available")
	}
	call := &signInCall{done: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), signInTimeout)
	p.signingIn, p.authCancel = call, cancel
	p.mu.Unlock()

	var fresh *Session
	if sess != nil {
		call.err = sess.ReconnectInteractive(ctx)
	} else {
		fresh, call.err = newSessionInteractive(ctx, clientID)
	}
	cancel()

	p.mu.Lock()
	p.signingIn, p.authCancel = nil, nil
	switch {
	case call.err != nil:
	case p.closed:
		// Quit while the browser was open: keep nothing.
		call.err = context.Canceled
		if fresh != nil {
			go fresh.Close()
		}
	default:
		if fresh != nil {
			p.session = fresh
		}
		p.resetSessionScopedStateLocked()
	}
	p.mu.Unlock()
	close(call.done)
	return call.err
}
