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
//   - A session knows which sign-in it is on (its generation) and whether
//     Spotify refused that one. A refusal is held against the sign-in that
//     produced it: a late answer to a request made under the old sign-in says
//     nothing about the new one.
//   - A refusal is playlist.ErrNeedsAuth, remembered in memory. The stored
//     credential is never deleted, and is replaced only by a sign-in that
//     produced a working session which was taken: built sessions carry their
//     credential staged, and whoever publishes the session writes it.
//   - A session is published only into a live owner. A closed provider or
//     session takes nothing; what it does not take is closed once and its
//     credential is not written.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/playlist"
)

var (
	// errSignInRefused is what a Web API call returns once Spotify has
	// refused the session's sign-in.
	errSignInRefused = fmt.Errorf("spotify: the sign-in is no longer accepted: %w", playlist.ErrNeedsAuth)
	// errStoredSignInRefused is a session setup that Spotify refused for the
	// stored refresh token. Unlike a missing credential, asking again cannot
	// help, so the provider remembers it.
	errStoredSignInRefused = fmt.Errorf("spotify: the stored sign-in is no longer accepted: %w", playlist.ErrNeedsAuth)
	// errSignInChanged ends a write whose sign-in was replaced while it was
	// pending, by the user signing in again or by a reconnect: it was not
	// asked for under the new one, and is not sent or retried.
	errSignInChanged = errors.New("spotify: the Spotify session changed, so the request was not sent; try again")
	// errSessionClosed and errProviderClosed end work that outlived its owner.
	errSessionClosed  = fmt.Errorf("spotify: session closed: %w", context.Canceled)
	errProviderClosed = fmt.Errorf("spotify: closed: %w", context.Canceled)
)

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
	// The pieces a session is built from.
	interactiveTokens   = productionInteractiveTokens
	newLibrespotSession = productionLibrespotSession
	sessionIdentity     = func(sess *session.Session) (username string, data []byte) {
		return sess.Username(), sess.StoredCredentials()
	}
	initSessionPlayer = func(s *Session) error { return s.initPlayer() }
	// closeLibrespot tears down a player and session: the ones a reconnect
	// replaced, a closed session's, or a replacement nobody took.
	closeLibrespot = func(p *librespotPlayer.Player, sess *session.Session) {
		if p != nil {
			p.Close()
		}
		if sess != nil {
			sess.Close()
		}
	}
)

// signInState is the part of a Session that says which sign-in it is on.
// Session.mu guards it, except cred, which has its own lock.
type signInState struct {
	// gen counts the sign-ins the session has been through: a reconnect
	// that is published starts the next. tokenRev counts the Web API token
	// sources within one.
	gen, tokenRev uint64
	// refused is set when Spotify refused the sign-in refusedGen.
	refused    bool
	refusedGen uint64
	closed     bool
	// cred is the credential that belongs to this sign-in.
	cred *credential
}

// credMu guards every credential, and with it every write of the credential
// file that a session makes. One lock for all of them, so that "is this still
// the sign-in that owns the file" and the write are one step: a rotation that
// belongs to a replaced sign-in either lands before the new sign-in's
// credential is written, which then overwrites it, or finds itself retired
// and writes nothing. Lock order: SpotifyProvider.mu, Session.mu, credMu;
// never the reverse, and nothing is called with credMu held.
var credMu sync.Mutex

// credential is a sign-in's stored form. It is staged while the session that
// carries it is built, and written when that session is taken; a rotated
// refresh token follows the same rule. Once its sign-in has been replaced, or
// its session closed, it is retired and never writes again.
type credential struct {
	creds     storedCreds
	committed bool
	retired   bool
}

// stageCreds gives the session being built its credential, unwritten.
func (s *Session) stageCreds(creds storedCreds) {
	s.cred = &credential{creds: creds}
}

// stagedTokenSource is the Web API token source of a session's credential: a
// rotated refresh token is kept with it, and written only once the credential
// has been, and only while it is the session's. The caller owns s (it is
// building it, or holds s.mu).
func (s *Session) stagedTokenSource(token *oauth2.Token) oauth2.TokenSource {
	cred := s.cred
	return &persistingTokenSource{
		source:       spotifyOAuthConfig(s.clientID, oauthScopes).TokenSource(tokenContext(), token),
		refreshToken: cred.snapshot().RefreshToken,
		persist:      cred.rotate,
	}
}

func (c *credential) snapshot() storedCreds {
	credMu.Lock()
	defer credMu.Unlock()
	return c.creds
}

// rotate records a refresh token Spotify issued in place of the old one, and
// writes it when this credential is the one on disk. A retired credential
// does neither.
func (c *credential) rotate(refreshToken string) error {
	credMu.Lock()
	defer credMu.Unlock()
	if c.retired {
		return nil
	}
	c.creds.RefreshToken = refreshToken
	if !c.committed {
		return nil
	}
	return saveCreds(&c.creds)
}

// commit writes the credential: the session it belongs to has been taken.
func (c *credential) commit() {
	if c == nil {
		return
	}
	credMu.Lock()
	defer credMu.Unlock()
	if c.committed || c.retired {
		return
	}
	c.committed = true
	if err := saveCreds(&c.creds); err != nil {
		applog.UserError("spotify: failed to save credentials: %v", err)
	}
}

// retire ends the credential's right to write: its sign-in was replaced, or
// its session closed.
func (c *credential) retire() {
	if c == nil {
		return
	}
	credMu.Lock()
	defer credMu.Unlock()
	c.retired = true
}

// accept is called by the owner that takes a newly built session: it writes
// the session's credential.
func (s *Session) accept() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.closed {
		s.cred.commit()
	}
}

// generation is the sign-in the session is on.
func (s *Session) generation() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen
}

// signInRefused reports whether Spotify has refused the sign-in the session
// is on.
func (s *Session) signInRefused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refusedLocked()
}

func (s *Session) refusedLocked() bool { return s.refused && s.refusedGen == s.gen }

// refuseSignIn records that Spotify refused the sign-in gen. It reports
// whether that is still the session's sign-in: a refusal of an earlier one
// changes nothing.
func (s *Session) refuseSignIn(gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return false
	}
	s.refused, s.refusedGen = true, gen
	return true
}

// needsSignIn reports whether the session cannot call the Web API until the
// user signs in again: Spotify refused its sign-in, or it has no token.
func (s *Session) needsSignIn() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.refusedLocked() || s.tokenSource == nil
}

// tokenStamp says which token a Web API call was made with.
type tokenStamp struct{ gen, rev uint64 }

// accessToken returns a Web API access token and the sign-in it belongs to.
// A refresh that Spotify answers with invalid_grant refuses that sign-in and
// is playlist.ErrNeedsAuth.
//
// When the sign-in was replaced while the token was being fetched, the answer
// is about the old one. A read asks the new one instead, once. A write does
// not: it was asked for under the old sign-in (pin, when its caller has one,
// else the one it starts under), so it ends with errSignInChanged and is sent
// only if the user asks again.
func (s *Session) accessToken(write bool, pin *signInPin) (string, tokenStamp, error) {
	for attempt := 0; ; attempt++ {
		s.mu.RLock()
		ts, stamp, refused := s.tokenSource, tokenStamp{s.gen, s.tokenRev}, s.refusedLocked()
		s.mu.RUnlock()
		if write && pin != nil && !pin.holdsGen(s, stamp.gen) {
			return "", stamp, errSignInChanged
		}
		if ts == nil {
			return "", stamp, fmt.Errorf("spotify: web api token unavailable, run '%s spotify reset' and sign in again: %w", appmeta.ClientName(), playlist.ErrNeedsAuth)
		}
		if refused {
			return "", stamp, errSignInRefused
		}
		tok, err := ts.Token()
		changed := s.generation() != stamp.gen
		switch {
		case err == nil && write && changed:
			return "", stamp, errSignInChanged
		case err == nil:
			return tok.AccessToken, stamp, nil
		case !isInvalidGrant(err):
			return "", stamp, fmt.Errorf("refresh access token: %w", err)
		case s.refuseSignIn(stamp.gen):
			return "", stamp, errSignInRefused
		case write:
			return "", stamp, errSignInChanged
		case attempt > 0:
			return "", stamp, errors.New("spotify: the Spotify session changed during the request; try again")
		}
	}
}

// signInPin is the sign-in an action on the account belongs to: the session
// its first write was made on, and the sign-in that session was on then. An
// action that began before the provider had a session is bound by its first
// write, to the session that write is made on, and to no other after that.
type signInPin struct {
	mu    sync.Mutex
	bound bool
	sess  *Session
	gen   uint64
}

// holds reports whether sess is the action's session, still on the action's
// sign-in. The first call binds an unbound pin to sess as it is now.
func (pin *signInPin) holds(sess *Session) bool {
	return pin.holdsGen(sess, sess.generation())
}

// holdsGen is holds for a caller that has read sess's sign-in already.
func (pin *signInPin) holdsGen(sess *Session, gen uint64) bool {
	pin.mu.Lock()
	defer pin.mu.Unlock()
	if !pin.bound {
		pin.bound, pin.sess, pin.gen = true, sess, gen
	}
	return pin.sess == sess && pin.gen == gen
}

type signInPinKey struct{}

// WithSignIn marks ctx as one user action on the account: every write made
// with it belongs to one sign-in, the one its first write is made under. An
// action that takes more than one request (a new playlist and the track
// added to it, or many tracks added at once) passes the same context to
// each, so that none of them is sent under a sign-in that replaced it
// part-way. What was written before that stays written; nothing is undone.
//
// The action has its identity from here, whether or not the provider has a
// session yet: one that starts before the stored session is set up is bound
// by its first write, not anew by each.
func (p *SpotifyProvider) WithSignIn(ctx context.Context) context.Context {
	if _, ok := ctx.Value(signInPinKey{}).(*signInPin); ok {
		return ctx // already one action
	}
	return context.WithValue(ctx, signInPinKey{}, &signInPin{})
}

// signInPinFrom is the pin of the action a write belongs to (WithSignIn), or
// one for the single request it is.
func signInPinFrom(ctx context.Context) *signInPin {
	if pin, ok := ctx.Value(signInPinKey{}).(*signInPin); ok {
		return pin
	}
	return &signInPin{}
}

// isWrite reports whether a Web API method changes the account.
func isWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// renewToken gets a new Web API token from the stored refresh token, for the
// sign-in and token of stamp: after a 401, or for a session that has none.
// When the token has been replaced since stamp, there is nothing to do.
// invalid_grant refuses the sign-in (playlist.ErrNeedsAuth, remembered); any
// other failure is an outage. The credential is never deleted.
func (s *Session) renewToken(ctx context.Context, stamp tokenStamp) error {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	s.mu.RLock()
	current, refused, cred, closed := tokenStamp{s.gen, s.tokenRev}, s.refusedLocked(), s.cred, s.closed
	s.mu.RUnlock()
	if closed {
		return errSessionClosed
	}
	if current.gen == stamp.gen && refused {
		return errSignInRefused
	}
	if current != stamp {
		return nil // a newer token is in place already
	}
	creds, err := s.renewalCreds(cred)
	if err != nil || creds.RefreshToken == "" {
		return playlist.ErrNeedsAuth
	}
	token, err := silentTokenRefresh(ctx, s.clientID, creds.RefreshToken)
	if isInvalidGrant(err) {
		// Held against the sign-in it was fetched for. When that one has
		// been replaced meanwhile, the refusal is not the session's: there
		// is nothing to report, and the caller goes on with the new one.
		if s.refuseSignIn(stamp.gen) {
			return errSignInRefused
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("spotify: web api token unavailable: %w", err)
	}

	// The answer is used, and its rotated refresh token written, only while
	// the session is still on the sign-in and token it was fetched for. The
	// check and the write are under the session's lock, which a new sign-in
	// takes to be published; a renewal that lost that race writes nothing.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (tokenStamp{s.gen, s.tokenRev}) != stamp {
		return nil
	}
	if s.cred == nil {
		// A session made without a credential of its own takes the stored
		// one as it was read for this renewal.
		s.cred = &credential{creds: creds, committed: true}
	}
	if token.RefreshToken != "" && token.RefreshToken != creds.RefreshToken {
		if err := s.cred.rotate(token.RefreshToken); err != nil {
			return fmt.Errorf("spotify: save rotated refresh token: %w", err)
		}
	}
	s.tokenSource = s.stagedTokenSource(token)
	s.tokenRev++
	return nil
}

// renewalCreds is the credential a renewal refreshes from: the session's
// own, or the stored one for a session built without it.
func (s *Session) renewalCreds(cred *credential) (storedCreds, error) {
	if cred != nil {
		return cred.snapshot(), nil
	}
	creds, err := loadCreds()
	if err != nil {
		return storedCreds{}, err
	}
	return *creds, nil
}

// publish puts a newly built session's parts into s, in place, and reports
// whether it did. The swap and the teardown of what it replaces happen under
// the write lock, as upstream's reconnect did, so no stream is being opened
// on the old player while it is closed.
//
// Nothing is published into a closed session, and a silent rebuild is not
// published over a sign-in that finished after it began (from is the sign-in
// it set out to renew): the session is renewed already, and the rebuild came
// from the older credential. An unpublished session is closed once and its
// credential is not written.
func (s *Session) publish(fresh *Session, from uint64, silent bool) (bool, error) {
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		fresh.Close()
		return false, errSessionClosed
	case silent && s.gen != from:
		s.mu.Unlock()
		fresh.Close()
		return false, nil
	}
	fresh.mu.Lock()
	oldPlayer, oldSess := s.player, s.sess
	s.sess, s.player, s.devID, s.tokenSource = fresh.sess, fresh.player, fresh.devID, fresh.tokenSource
	// The old sign-in's credential stops writing before the new one's is
	// written: nothing of the old sign-in can follow it onto disk.
	s.cred.retire()
	s.cred = fresh.cred
	s.gen++
	s.tokenRev = 0
	// What fresh held is s's now: its Close must not tear it down.
	fresh.sess, fresh.player, fresh.cred, fresh.closed = nil, nil, nil, true
	fresh.mu.Unlock()
	s.cred.commit()
	closeLibrespot(oldPlayer, oldSess)
	s.mu.Unlock()
	return true, nil
}

// signInCall is one interactive sign-in, shared by every caller that asks
// while it runs.
type signInCall struct {
	done chan struct{}
	err  error
}

// signIn is Authenticate. It signs in when there is no session, or when the
// session needs it; a healthy session is left alone. One browser flow runs at
// a time: a second caller waits for the first's result. A closed provider
// starts nothing.
//
// A broken session is replaced in place (Session.ReconnectInteractive): the
// new one is built first, so a sign-in that fails or is cancelled leaves the
// old session, its refusal and the stored credential as they were, and what
// a successful one replaces is closed at once. A stream opened before the
// swap reads its audio over its own connection and is not cut.
func (p *SpotifyProvider) signIn() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errProviderClosed
	}
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
	case fresh != nil && p.closed:
		// Quit while the browser was open: the session is not taken.
		call.err = errProviderClosed
	default:
		// The sign-in is done once its session is in place and its
		// credential written: here for a provider that had no session, in
		// publish for one that replaced its session. A quit that lands after
		// that does not undo it, and does not make it a failure.
		if fresh != nil {
			p.session = fresh
			fresh.accept()
			fresh = nil
		}
		p.storedRefused = false
		p.resetSessionScopedStateLocked()
	}
	p.mu.Unlock()
	if fresh != nil {
		fresh.Close() // built, and not taken
	}
	close(call.done)
	return call.err
}

// unauthorized handles a 401 from the Web API for a call made with stamp. It
// renews the token once. A read may then be sent again (retry), and a 401 the
// renewed token gets too means the sign-in is refused.
//
// A write is never sent again, whatever the renewal did. So that it still
// learns what its 401 meant, the renewed token is tried on one read-only
// request: refused there too, the sign-in is refused and the write reports
// playlist.ErrNeedsAuth; otherwise the write reports that it was not sent.
func (s *Session) unauthorized(ctx context.Context, method string, stamp tokenStamp, renewed bool) (retry bool, err error) {
	if renewed {
		// The renewed token was refused too: the sign-in is, unless it has
		// been replaced meanwhile, when this answer is about the old one.
		if s.refuseSignIn(stamp.gen) {
			return false, errSignInRefused
		}
		return s.afterSignInChanged(method)
	}
	if err := s.renewToken(ctx, stamp); err != nil {
		return false, err
	}
	if s.generation() != stamp.gen {
		return s.afterSignInChanged(method)
	}
	if !isWrite(method) {
		return true, nil
	}
	resp, probe, err := s.webAPIStamped(ctx, http.MethodGet, "/v1/me", nil, nil, "", nil)
	if err != nil {
		if errors.Is(err, playlist.ErrNeedsAuth) {
			return false, err
		}
		return false, fmt.Errorf("spotify: the request was not authorised and has not been sent again: %w", err)
	}
	resp.Body.Close()
	switch {
	case probe.gen != stamp.gen:
		return false, errSignInChanged
	case resp.StatusCode == http.StatusUnauthorized:
		if s.refuseSignIn(probe.gen) {
			return false, errSignInRefused
		}
		return false, errSignInChanged
	}
	return false, errors.New("spotify: the request was not authorised and has not been sent again; the sign-in has been renewed, so try once more")
}

// afterSignInChanged is what a call does when the answer it got was about a
// sign-in that has since been replaced: a read is sent again, on the new
// one; a write is not sent.
func (s *Session) afterSignInChanged(method string) (retry bool, err error) {
	if isWrite(method) {
		return false, errSignInChanged
	}
	return true, nil
}

// startupResult finishes ensureSession: sess and err are what the stored
// credential's silent build produced, begun when the provider had no session.
// The provider takes the session only if it still has none and is open. A
// user who signed in while the build ran keeps that sign-in: the build is
// closed, unwritten, and neither it nor its failure changes anything.
func (p *SpotifyProvider) startupResult(sess *Session, err error) error {
	p.mu.Lock()
	overtaken, closed := p.session != nil, p.closed
	if err == nil && !overtaken && !closed {
		p.session = sess
		sess.accept() // taken, so its credential is written
		p.resetSessionScopedStateLocked()
		p.mu.Unlock()
		return nil
	}
	if err != nil && !overtaken && !closed && errors.Is(err, errStoredSignInRefused) {
		p.storedRefused = true // remembered: Spotify is not asked again
	}
	p.mu.Unlock()
	if sess != nil {
		sess.Close() // built, and not taken
	}
	switch {
	case overtaken:
		return nil
	case closed:
		return errProviderClosed
	}
	return silentSessionError(err) // only credential failures need sign-in
}

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
