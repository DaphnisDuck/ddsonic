package spotify

// ddsonic: a Spotify sign-in that stops working while ddsonic runs, and the
// sign-in that replaces it.
//
// Upstream treats a session that exists as a signed-in one: Authenticate
// returns at once when the provider has a session, so a session whose refresh
// token Spotify has revoked can never be signed in again without a restart,
// and the refusal itself reaches callers as a plain error. Upstream also
// writes the credential while a session is still being built, and deletes it
// when a stored one is refused.
//
// Here:
//   - A session's Web API token and its stored credential belong to its
//     token coordinator (token_coordinator_ddsonic.go): one per sign-in, the
//     only thing that refreshes, publishes or persists them.
//   - A refusal is playlist.ErrNeedsAuth, remembered in memory. The stored
//     credential is never deleted, and is replaced only by a sign-in that
//     produced a working session which was taken.
//   - A session is published only into a live owner. A closed provider or
//     session takes nothing; what it does not take is retired, unwritten.
//   - What a session plays with is its playback bundle
//     (playback_ddsonic.go), replaced as a whole and torn down outside every
//     lock.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

var (
	// errSignInRefused is what a Web API call returns once Spotify has
	// refused the session's sign-in.
	errSignInRefused = fmt.Errorf("spotify: the sign-in is no longer accepted: %w", playlist.ErrNeedsAuth)
	// errStoredSignInRefused is a session setup that Spotify refused for the
	// stored refresh token. Unlike a missing credential, asking again cannot
	// help, so the provider remembers it.
	errStoredSignInRefused = fmt.Errorf("spotify: the stored sign-in is no longer accepted: %w", playlist.ErrNeedsAuth)
	// errSignInInProgress is what a caller without a session gets while the
	// user is signing in through the browser: no session is built from the
	// stored credential meanwhile, and nothing is remembered against it.
	errSignInInProgress = fmt.Errorf("spotify: sign-in in progress: %w", playlist.ErrNeedsAuth)
	// errSignInChanged ends a write whose sign-in was replaced while it was
	// pending, by the user signing in again or by a reconnect: it was not
	// asked for under the new one, and is not sent or retried.
	errSignInChanged error = notSent("spotify: the Spotify session changed, so the request was not sent; try again")
	// errSessionClosed and errProviderClosed end work that outlived its owner.
	errSessionClosed  = fmt.Errorf("spotify: session closed: %w", context.Canceled)
	errProviderClosed = fmt.Errorf("spotify: closed: %w", context.Canceled)
)

// notSent is an error for a write that was not sent: it is
// provider.ErrNotApplied to whoever reports what became of the write.
type notSent string

func (e notSent) Error() string        { return string(e) }
func (e notSent) Is(target error) bool { return target == provider.ErrNotApplied }

// signInTimeout bounds one interactive sign-in. A variable so a test can
// let it pass.
var signInTimeout = 5 * time.Minute

// The session's builders and librespot calls, as variables so a test can
// stand in for Spotify.
var (
	// newSessionInteractive signs in through the browser for a provider that
	// has no session. It builds from a new grant only: the stored credential
	// is tried first by the provider's one stored build (build_ddsonic.go).
	newSessionInteractive = newInteractiveSession
	// buildInteractiveSession signs in anew for a session that needs it.
	buildInteractiveSession = newInteractiveSession
	// buildPlaybackSession rebuilds a session's playback from its credential.
	buildPlaybackSession = newPlaybackSession
	sessionNewStream     = func(s *Session, ctx context.Context, id librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
		return s.NewStream(ctx, id, bitrate)
	}
	// sessionReconnect rebuilds the playback of the session build gen.
	sessionReconnect = func(s *Session, ctx context.Context, gen uint64) error { return s.reconnectPlayback(ctx, gen) }
	// The pieces a session is built from.
	interactiveTokens   = productionInteractiveTokens
	newLibrespotSession = productionLibrespotSession
	sessionIdentity     = func(sess *session.Session) (username string, data []byte) {
		return sess.Username(), sess.StoredCredentials()
	}
	initSessionPlayer = func(s *Session) error { return s.initPlayer() }
	// closeLibrespot tears down a player and session: a retired bundle's,
	// once nothing is being opened on it, or a build's that failed.
	closeLibrespot = func(p *librespotPlayer.Player, sess *session.Session) {
		if p != nil {
			p.Close()
		}
		if sess != nil {
			sess.Close()
		}
	}
)

// signInState is the part of a Session that says which sign-in it is on and
// what belongs to it. Session.mu guards it; view is its copy for readers.
type signInState struct {
	// view is what a reader needs, in one load: no lock is taken to make a
	// request or to ask whether the session needs sign-in. It is stored only
	// with Session.mu held.
	view atomic.Pointer[sessionView]
	// gen counts the session's builds: every publication into it, a sign-in
	// or a silent rebuild of its playback, starts the next.
	gen    uint64
	closed bool
	// playbackRefused is set when Spotify refused the build gen to play, and
	// a silent rebuild did not cure it: a sign-in must replace the session.
	// It is about the build, not about a token, and a renewed token leaves
	// it standing.
	playbackRefused bool
	// tokens is the sign-in's coordinator, and play what the build plays
	// with. A session built without one of them has nil there.
	tokens *tokenCoordinator
	play   *playback
	// ownerCh is closed, and replaced, when play changes hands: at a
	// publication's swap and at Close, under Session.mu. A recovery waiting
	// on the bundle it ran on captures it under the lock (recovery_ddsonic.go).
	ownerCh chan struct{}
}

// swapOwnerLocked tells whoever captured the session's owner channel that
// the owner has changed: that channel is closed and a new one put in its
// place. s.mu is held.
func (s *Session) swapOwnerLocked() {
	if s.ownerCh != nil {
		close(s.ownerCh)
	}
	s.ownerCh = make(chan struct{})
}

// ownerChLocked is the session's current owner channel. s.mu is held.
func (s *Session) ownerChLocked() <-chan struct{} {
	if s.ownerCh == nil { // a session made by a test, not by a builder
		s.ownerCh = make(chan struct{})
	}
	return s.ownerCh
}

// sessionView is a session as a reader sees it.
type sessionView struct {
	tokens          *tokenCoordinator
	gen             uint64
	closed          bool
	playbackRefused bool
}

// storeViewLocked publishes the session's state to readers. s.mu is held.
func (s *Session) storeViewLocked() {
	s.view.Store(&sessionView{tokens: s.tokens, gen: s.gen, closed: s.closed, playbackRefused: s.playbackRefused})
}

// viewNow is the session as it stands.
func (s *Session) viewNow() sessionView {
	if v := s.view.Load(); v != nil {
		return *v
	}
	return sessionView{}
}

// builtSession is the session a builder made: its playback bundle, and its
// coordinator with the credential staged in it, unwritten. token is the Web
// API token it got, or nil.
func builtSession(clientID string, sess *session.Session, creds storedCreds, token *oauth2.Token) *Session {
	s := &Session{clientID: clientID}
	s.ownerCh = make(chan struct{})
	s.play = newPlayback(sess, creds.Username, creds.Data, creds.DeviceID)
	s.tokens = newTokenCoordinator(clientID, creds, token)
	s.storeViewLocked() // not shared yet
	return s
}

// accept is called by the owner that takes a newly built session: its
// credential becomes the stored one.
func (s *Session) accept() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed && s.tokens != nil {
		claimCreds(s.tokens, nil)
	}
}

// generation is the build the session is on.
func (s *Session) generation() uint64 { return s.viewNow().gen }

// signInRefused reports whether Spotify has refused the sign-in the session
// is on: its token, or the build to play.
func (s *Session) signInRefused() bool {
	v := s.viewNow()
	if v.playbackRefused {
		return true
	}
	if v.tokens == nil {
		return false
	}
	v.tokens.mu.Lock()
	defer v.tokens.mu.Unlock()
	return v.tokens.refusedLocked()
}

// refuseSignIn records that Spotify refused the session build gen to play.
// It reports whether that is still the session's build: a refusal of an
// earlier one changes nothing.
func (s *Session) refuseSignIn(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen || s.closed {
		return false
	}
	s.playbackRefused = true
	s.storeViewLocked()
	return true
}

// needsSignIn reports whether the session cannot call the Web API until the
// user signs in again: Spotify refused it, or it has no token.
func (s *Session) needsSignIn() bool {
	v := s.viewNow()
	return v.playbackRefused || v.tokens.needsSignIn()
}

// signInPin is the sign-in an action on the account belongs to: the one that
// stood when the action began. It holds the provider's count of accepted
// sign-ins at that moment (authEpoch), and the session and its build: the
// ones the provider had then, or, for an action that began before the stored
// session was set up, the ones its first write is made on. The silent setup
// of the stored session is no sign-in and leaves the action as it was.
type signInPin struct {
	p     *SpotifyProvider
	epoch uint64

	mu    sync.Mutex
	bound bool
	sess  *Session
	gen   uint64
}

// holds reports whether the action may write on sess now: no sign-in has
// been accepted since the action began, and sess is the action's session,
// still on the action's build. An action that began without a session is
// bound to sess by the first call.
//
// The session's build is read before the provider's count: a sign-in moves
// the count before it publishes the build, so a caller that saw the new
// build sees the new count.
func (pin *signInPin) holds(sess *Session) bool {
	gen := sess.generation()
	if pin.p.authEpoch.Load() != pin.epoch {
		return false
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if !pin.bound {
		pin.bound, pin.sess, pin.gen = true, sess, gen
	}
	return pin.sess == sess && pin.gen == gen
}

type signInPinKey struct{}

// WithSignIn marks ctx as one user action on the account, begun now: every
// write made with it belongs to the sign-in that stands now. An action that
// takes more than one request (a new playlist and the track added to it, or
// many tracks added at once) passes the same context to each. None of them
// is sent once a sign-in has been accepted since: not the first, and not the
// rest. Signing in again is not asking for the action again. What the action
// had written stays written; nothing is undone or resumed.
func (p *SpotifyProvider) WithSignIn(ctx context.Context) context.Context {
	if _, ok := ctx.Value(signInPinKey{}).(*signInPin); ok {
		return ctx // already one action
	}
	return context.WithValue(ctx, signInPinKey{}, p.newSignInPin())
}

// newSignInPin is the pin of an action that begins now.
func (p *SpotifyProvider) newSignInPin() *signInPin {
	p.mu.Lock()
	defer p.mu.Unlock()
	pin := &signInPin{p: p, epoch: p.authEpoch.Load()}
	if sess := p.session; sess != nil {
		pin.bound, pin.sess, pin.gen = true, sess, sess.generation()
	}
	return pin
}

// signInPinFrom is the pin of the action a write belongs to (WithSignIn), or
// one for the single request it is, begun now.
func (p *SpotifyProvider) signInPinFrom(ctx context.Context) *signInPin {
	if pin, ok := ctx.Value(signInPinKey{}).(*signInPin); ok {
		return pin
	}
	return p.newSignInPin()
}

// isWrite reports whether a Web API method changes the account.
func isWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// publish puts a newly built session's parts into s, in place, and reports
// whether it did. Nothing is published into a closed session, and a silent
// rebuild is not published over a build that was published after it began
// (from is the build it set out to renew): an unpublished build is retired,
// unwritten.
//
// An interactive build brings a new sign-in: its coordinator replaces the
// session's, and the old one is closed and the new credential written in one
// step (claimCreds), before Close can run. A silent rebuild brings playback
// only: the session keeps its coordinator, token and refresh token, and
// records the rebuilt identity. Either way the build counts up, which ends
// the writes of an action that began on the old one.
//
// The old playback bundle is retired after the lock is released; the caller
// does not wait for it (playback.retireNow).
//
// ctx is the context of the call that owns the build. It is looked at here,
// under the lock, before anything is swapped, counted or written: a call
// that has ended publishes nothing, however late it ended. One that ends
// after this point has been published, and stands.
func (s *Session) publish(ctx context.Context, fresh *Session, from uint64, silent bool, published func()) (bool, error) {
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		fresh.Close()
		return false, errSessionClosed
	case ctx.Err() != nil:
		err := ctx.Err()
		s.mu.Unlock()
		fresh.Close()
		return false, err
	case silent && s.gen != from:
		s.mu.Unlock()
		fresh.Close()
		return false, nil
	}
	fresh.mu.Lock()
	oldPlay, oldTokens := s.play, s.tokens
	s.play = fresh.play
	s.swapOwnerLocked()
	s.gen++
	s.playbackRefused = false
	if silent {
		s.storeViewLocked()
		if s.tokens != nil && s.play != nil {
			s.tokens.setIdentity(s.play.username, s.play.data, s.play.devID)
		}
	} else {
		s.tokens = fresh.tokens
		if published != nil {
			published() // the provider's count moves before the build is seen
		}
		if publishHook != nil {
			publishHook("counted")
		}
		s.storeViewLocked()
		if s.tokens != nil {
			claimCreds(s.tokens, oldTokens)
		} else {
			oldTokens.close()
		}
	}
	// What fresh held is s's now: its Close must not retire it. A silent
	// build's own coordinator, if it made one, is not taken.
	var untaken *tokenCoordinator
	if silent {
		untaken = fresh.tokens
	}
	fresh.play, fresh.tokens, fresh.closed = nil, nil, true
	fresh.storeViewLocked()
	fresh.mu.Unlock()
	s.mu.Unlock()
	if detachHook != nil {
		detachHook("publish")
	}
	untaken.close()
	oldPlay.retireNow()
	return true, nil
}

// detachHook, when set by a test, runs where a publication or a close has
// detached the old bundle and not yet retired it.
var detachHook func(point string)

// publishHook, when set by a test, runs where a sign-in's publication has
// moved the provider's count and not yet stored the session's new view
// ("counted"), with the session's lock held.
var publishHook func(point string)

// Close ends the session: its coordinator is closed (after one last attempt
// to write a credential it had accepted and could not write), and its
// playback retired. The caller does not wait for librespot.
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed { // once; a closed session takes no replacement
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.storeViewLocked()
	s.tokens.close()
	play := s.play
	s.play = nil
	s.swapOwnerLocked()
	s.mu.Unlock()
	if detachHook != nil {
		detachHook("close")
	}
	play.retireNow()
}

// Reconnect rebuilds the session's playback from its credential (no
// browser). It refreshes no token: the session's coordinator is the one
// thing that does.
func (s *Session) Reconnect(ctx context.Context) error {
	return s.reconnectPlayback(ctx, s.generation())
}

// reconnectPlayback is Reconnect for the build gen: when the session has
// been rebuilt or signed in again since, there is nothing to do.
func (s *Session) reconnectPlayback(ctx context.Context, gen uint64) error {
	v := s.viewNow()
	switch {
	case v.closed:
		return errSessionClosed
	case v.gen != gen:
		return nil
	case v.tokens == nil:
		return fmt.Errorf("spotify: reconnect: %w", errNoStoredCreds)
	}
	creds := v.tokens.snapshot()
	return s.reconnect(ctx, func(ctx context.Context, clientID string) (*Session, error) {
		return buildPlaybackSession(ctx, clientID, creds)
	}, gen, true, nil)
}

// ReconnectInteractive forces a fresh browser-based OAuth2 flow. The stored
// credential is kept until the new session has been taken.
func (s *Session) ReconnectInteractive(ctx context.Context) error {
	return s.reconnect(ctx, buildInteractiveSession, s.generation(), false, nil)
}

// reconnect replaces the session's parts with a newly built session's. The
// new one is built first, with no lock held and on its own goroutine, so a
// build that fails, is cancelled or outlives ctx leaves the session as it
// was (runBuild); from is the build it set out to renew.
func (s *Session) reconnect(ctx context.Context, build func(context.Context, string) (*Session, error), from uint64, silent bool, published func()) error {
	if s.viewNow().closed {
		return errSessionClosed
	}
	clientID := s.clientID
	newSess, err := runBuild(ctx, func(ctx context.Context) (*Session, error) { return build(ctx, clientID) })
	if err != nil {
		return fmt.Errorf("spotify: reconnect: %w", err)
	}
	if beforeAccept != nil {
		beforeAccept()
	}
	done, err := s.publish(ctx, newSess, from, silent, published)
	if !done {
		return err
	}

	const reauthMsg = "spotify: re-authenticated successfully"
	applog.Info(reauthMsg)
	applog.Status(reauthMsg)
	return nil
}

// rejectedWrite is the error of a call whose 401 was followed by cause, a
// failure to renew the token or to check the renewed one. For a write it
// keeps what is known: Spotify refused the write, so it was not applied
// (provider.ErrNotApplied), and it is not sent again; cause is kept beside
// that, as what it is. A sign-in that cause says is required is reported as
// that. For a read it is cause alone.
func rejectedWrite(method string, cause error) error {
	if !isWrite(method) || errors.Is(cause, playlist.ErrNeedsAuth) {
		return cause
	}
	return &notAppliedError{cause: cause}
}

type notAppliedError struct{ cause error }

func (e *notAppliedError) Error() string {
	return "spotify: the request was not authorised, so it was not carried out, and has not been sent again: " + e.cause.Error()
}
func (e *notAppliedError) Unwrap() error        { return e.cause }
func (e *notAppliedError) Is(target error) bool { return target == provider.ErrNotApplied }

// What reaches Spotify, and the browser, when no test has put a stand-in in
// its place. Under go test these refuse: a test that forgets its stand-in
// fails, in whichever package it is, instead of signing in for real.
var errNoStandIn = errors.New("spotify: not done under go test without a stand-in")

func productionInteractiveTokens(ctx context.Context, flows []oauthFlow) ([]*oauth2.Token, error) {
	if testing.Testing() {
		return nil, fmt.Errorf("browser sign-in: %w", errNoStandIn)
	}
	return performOAuth2PKCEFlows(ctx, flows)
}

func productionLibrespotSession(ctx context.Context, opts *session.Options) (*session.Session, error) {
	if testing.Testing() {
		return nil, fmt.Errorf("session: %w", errNoStandIn)
	}
	return session.NewSessionFromOptions(ctx, opts)
}

// realTransport is the transport the process started with. A test that
// stands in for Spotify replaces http.DefaultTransport.
var realTransport = http.DefaultTransport

// guardedTransport is the token and Web API clients' transport: the default
// one, except that under go test it refuses to use the real one.
type guardedTransport struct{}

func (guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt := http.DefaultTransport
	if testing.Testing() && rt == realTransport {
		return nil, fmt.Errorf("request to %s: %w", req.URL.Host, errNoStandIn)
	}
	return rt.RoundTrip(req)
}

func init() {
	tokenHTTPClient.Transport = guardedTransport{}
	webHTTPClient.Transport = guardedTransport{}
}
