package spotify

// ddsonic: building a session, and who may build one when.
//
// A build is librespot work: a connection, a handshake, a player. librespot
// can keep a builder past its context (its identity getters take a lock a
// background reconnect holds, and a failed builder's own cleanup closes the
// connection synchronously), so no call, phase or waiter here depends on a
// builder returning. Every build runs as a worker, which nobody waits for,
// and is awaited only until the build's own context ends (runBuild).
//
// A provider without a healthy session is in one build phase:
//
//   - idle: nothing is being built.
//   - storedBuilding: the one build from the stored credential is in flight.
//     Startup and Authenticate share it, whichever asked first, so the
//     stored refresh token is refreshed by one authority at a time: this
//     build, and afterwards the coordinator of the session it produced.
//   - browserSignIn: the user is signing in through the browser. No stored
//     build starts meanwhile; a caller that has no session is told so.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	librespot "github.com/devgianlu/go-librespot"
	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	"github.com/devgianlu/go-librespot/session"
)

type buildPhase int

const (
	phaseIdle buildPhase = iota
	phaseStoredBuilding
	phaseBrowserSignIn
)

// storedCall is the one build from the stored credential, shared by every
// caller that asks while it runs. Its controller (runStored) alone completes
// it: err is written before done is closed and read after.
type storedCall struct {
	done chan struct{}
	err  error
}

// Hooks for tests, nil otherwise: storedJoined runs when a caller has started
// to wait for a stored build another caller began, and buildCommit just
// before a stored build's completion takes the provider's lock.
var (
	storedJoined func()
	buildCommit  func()
	// beforeAccept runs when a build has returned and is about to be
	// published or installed: just before the lock that decides it.
	beforeAccept func()
)

// builds counts the build workers still running, so a test can wait for
// them before it takes its stand-ins away; no production caller waits.
var builds sync.WaitGroup

// runBuild runs build on a goroutine of its own and waits for it only while
// ctx lasts. A build that returns in time is the caller's. One that does not
// is abandoned: runBuild returns ctx's error at once, and the worker retires
// whatever the build produces later, unwritten (a session nobody took was
// never claimed). A result that arrives with ctx already ended is rejected
// the same way, success included.
func runBuild(ctx context.Context, build func(context.Context) (*Session, error)) (*Session, error) {
	type result struct {
		sess *Session
		err  error
	}
	var state atomic.Int32
	results := make(chan result, 1)
	builds.Add(1)
	go func() {
		defer builds.Done()
		sess, err := build(ctx)
		if !state.CompareAndSwap(openPending, openReady) {
			closeBuilt(sess) // abandoned: ours to retire
			return
		}
		results <- result{sess, err}
	}()
	var res result
	select {
	case res = <-results:
	case <-ctx.Done():
		if state.CompareAndSwap(openPending, openAbandoned) {
			return nil, fmt.Errorf("spotify: the session build did not finish in time: %w", ctx.Err())
		}
		res = <-results // published already: ours, checked below
	}
	if err := ctx.Err(); err != nil {
		closeBuilt(res.sess) // too late, whatever it says
		return nil, fmt.Errorf("spotify: the session build did not finish in time: %w", err)
	}
	return res.sess, res.err
}

// closeBuilt retires a built session that nobody took, if there is one.
func closeBuilt(sess *Session) {
	if sess != nil {
		sess.Close()
	}
}

// newPlaybackSession builds a session's playback from its credential, for a
// silent reconnect: a librespot session and a player, and no Web API token.
// The session that takes it keeps its own coordinator.
func newPlaybackSession(ctx context.Context, clientID string, creds storedCreds) (*Session, error) {
	if creds.Username == "" || len(creds.Data) == 0 {
		return nil, errNoStoredCreds
	}
	devID := creds.DeviceID
	if devID == "" {
		devID = generateDeviceID()
	}
	sess, err := newLibrespotSession(ctx, &session.Options{
		Log:        &librespot.NullLogger{},
		DeviceType: devicespb.DeviceType_COMPUTER,
		DeviceId:   devID,
		Credentials: session.StoredCredentials{
			Username: creds.Username,
			Data:     creds.Data,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: stored auth: %w", err)
	}
	username, data := sessionIdentity(sess)
	s := &Session{clientID: clientID}
	s.ownerCh = make(chan struct{})
	s.play = newPlayback(sess, username, data, devID)
	s.storeViewLocked() // not shared yet
	if err := initSessionPlayer(s); err != nil {
		closeLibrespot(nil, sess)
		return nil, err
	}
	return s, nil
}

// ensureSession makes sure the provider has a session, building one from the
// stored credential when it has none (no browser). It returns
// playlist.ErrNeedsAuth if interactive sign-in is needed.
func (p *SpotifyProvider) ensureSession() error {
	return p.storedBuild(context.Background())
}

// storedBuild returns once the provider has a session, or cannot get one from
// the stored credential. It starts the stored build or joins the one in
// flight, and waits for it while wait lasts; the build itself runs on its own
// context and goes on when a caller stops waiting.
func (p *SpotifyProvider) storedBuild(wait context.Context) error {
	p.mu.Lock()
	switch {
	case p.session != nil:
		p.mu.Unlock()
		return nil
	case p.closed:
		// A closed provider builds nothing.
		p.mu.Unlock()
		return errProviderClosed
	case p.storedRefused:
		// Spotify refused the stored sign-in: it is not offered again.
		p.mu.Unlock()
		return errStoredSignInRefused
	case p.phase == phaseBrowserSignIn:
		// The user is signing in. Nothing is built from the stored
		// credential meanwhile, and the caller is not made to wait for the
		// browser: the sync a sign-in requires follows it.
		p.mu.Unlock()
		return errSignInInProgress
	}
	call, joined := p.stored, true
	if call == nil {
		clientID := p.clientID
		if clientID == "" {
			p.mu.Unlock()
			return fmt.Errorf("spotify: no client ID available")
		}
		// Bounded, so a stalled setup ends (session_setup_ddsonic.go).
		ctx, cancel := context.WithTimeout(context.Background(), sessionSetupTimeout)
		call, joined = &storedCall{done: make(chan struct{})}, false
		p.stored, p.storedCancel, p.phase = call, cancel, phaseStoredBuilding
		go p.runStored(call, ctx, cancel, clientID)
	}
	p.mu.Unlock()
	if joined && storedJoined != nil {
		storedJoined()
	}
	select {
	case <-call.done:
		return call.err
	case <-wait.Done():
		return wait.Err()
	}
}

// runStored is a stored build's controller: it awaits the build until the
// call's own context ends, and then completes the call in one step under the
// provider's lock. The provider takes the session only if the build
// succeeded in time, the provider is open and it still has none; anything
// else is retired, unwritten. A sign-in that is waiting for this build is
// handed the browser phase in the same step, so no caller sees the provider
// idle in between.
func (p *SpotifyProvider) runStored(call *storedCall, ctx context.Context, cancel context.CancelFunc, clientID string) {
	sess, err := runBuild(ctx, func(ctx context.Context) (*Session, error) { return newSessionSilent(ctx, clientID) })
	if buildCommit != nil {
		buildCommit()
	}
	p.mu.Lock()
	p.stored, p.storedCancel = nil, nil
	// The call's context is looked at first, whatever the build returned: a
	// call that has ended, by its deadline or a cancellation, ended. Its
	// session is not taken, and neither is its verdict: a refusal that
	// arrives with a dead call is not remembered against the stored sign-in.
	if ctxErr := ctx.Err(); ctxErr != nil {
		err = fmt.Errorf("spotify: the session build did not finish in time: %w", ctxErr)
	}
	signingIn := p.signingIn != nil // a sign-in is registered, and waiting
	installed := false
	switch {
	case p.closed:
		err = errProviderClosed
	case err == nil && p.session == nil:
		p.session = sess
		if signingIn && !sess.needsSignIn() {
			// A stored session that signs the user in, installed for a
			// sign-in, is that sign-in's: the count moves with the install,
			// as it always has. One without a Web API token signs nobody
			// in; the browser sign-in that follows is what counts.
			p.authEpoch.Add(1)
		}
		sess.accept() // taken, so its credential is written
		p.resetSessionScopedStateLocked()
		installed = true
	case err == nil:
		// The provider has a session already; this one is not needed.
	case errors.Is(err, errStoredSignInRefused):
		p.storedRefused = true // remembered: Spotify is not asked again
	}
	p.phase = phaseIdle
	if !p.closed && signingIn && (p.session == nil || p.session.needsSignIn()) {
		p.phase = phaseBrowserSignIn
	}
	p.mu.Unlock()
	cancel()
	if !installed && sess != nil {
		sess.Close() // built, and not taken
	}
	switch {
	case installed, err == nil:
	case errors.Is(err, errProviderClosed):
		call.err = err
	default:
		call.err = silentSessionError(err) // only credential failures need sign-in
	}
	close(call.done)
}

// signInCall is one interactive sign-in, shared by every caller that asks
// while it runs.
type signInCall struct {
	done chan struct{}
	err  error
}

// signIn is Authenticate. It signs in when there is no session, or when the
// session needs it; a healthy session is left alone. One sign-in runs at a
// time: a second caller waits for the first's result. A closed provider
// starts nothing.
//
// With no session, the stored credential is tried first, through the one
// stored build (storedBuild): when it works, the sign-in is done and no
// browser opens. The browser comes after that build has settled, never
// beside it, and signs in from a new grant.
//
// A session that needs sign-in is replaced in place: the new one is built
// first, so a sign-in that fails or is cancelled leaves the old session, its
// refusal and the stored credential as they were, and what a successful one
// replaces is retired at once. A stream opened before the swap reads its
// audio over its own connection and is not cut.
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
	storedFirst := sess == nil && !p.storedRefused
	if !storedFirst {
		p.phase = phaseBrowserSignIn
	}
	p.mu.Unlock()

	if storedFirst {
		// Whatever it returns, the state is what decides, below.
		_ = p.storedBuild(ctx)
	}

	// After waking for any reason the sign-in decides from the state, with
	// the lock a stored build's completion takes.
	p.mu.Lock()
	sess = p.session
	browser := false
	switch {
	case p.closed:
		call.err = errProviderClosed
	case sess != nil && !sess.needsSignIn():
		// The stored credential worked: signed in, with no browser.
	case ctx.Err() != nil:
		call.err = fmt.Errorf("spotify: authentication cancelled: %w", ctx.Err())
	default:
		browser = true
		p.phase = phaseBrowserSignIn
	}
	p.mu.Unlock()

	var fresh *Session
	if browser {
		if sess != nil {
			// The provider's count of sign-ins moves with the swap itself.
			call.err = sess.reconnect(ctx, buildInteractiveSession, sess.generation(), false, func() { p.authEpoch.Add(1) })
		} else {
			fresh, call.err = runBuild(ctx, func(ctx context.Context) (*Session, error) { return newSessionInteractive(ctx, clientID) })
		}
	}
	if browser && beforeAccept != nil {
		beforeAccept()
	}

	// The sign-in ends here, and only here: it deregisters itself, so a
	// stored build that completes later moves no count for it.
	p.mu.Lock()
	p.signingIn, p.authCancel = nil, nil
	if p.phase == phaseBrowserSignIn {
		p.phase = phaseIdle
	}
	switch {
	case call.err != nil:
	case fresh != nil && p.closed:
		// Quit while the browser was open: the session is not taken.
		call.err = errProviderClosed
	case fresh != nil && ctx.Err() != nil:
		// The sign-in's own call ended before its session was installed:
		// decided here, under the lock the install would take. Nothing is
		// installed, counted or written.
		call.err = fmt.Errorf("spotify: authentication cancelled: %w", ctx.Err())
	case fresh != nil && p.session != nil:
		// The provider has a session already; this one is not needed.
	default:
		// The sign-in is done once its session is in place and its
		// credential written: here for a provider that had no session, in
		// publish for one that replaced its session. A quit that lands after
		// that does not undo it, and does not make it a failure.
		if fresh != nil {
			p.session = fresh
			p.authEpoch.Add(1)
			fresh.accept()
			fresh = nil
		}
		p.storedRefused = false
		p.resetSessionScopedStateLocked()
	}
	p.mu.Unlock()
	cancel() // after the decision: it must not be what ends the call
	if fresh != nil {
		fresh.Close() // built, and not taken
	}
	close(call.done)
	return call.err
}

// sessionNow is the provider's session, or nil.
func (p *SpotifyProvider) sessionNow() *Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session
}
