package spotify

// ddsonic: the one recovery a play has, after its first track open failed.
//
// The open's error is classified once, first match wins (lossOf):
//  1. the caller's or the setup's context ended: final;
//  2. the access point closed for good (ErrAccesspointClosed): the bundle
//     is rebuilt from the credential and the track opened once more;
//  3. the key request's connection was replaced (ConnectionReplacedError):
//     the track is opened once more on the same session, which is healthy;
//  4. the key request's write failed (KeySendError): opened once more only
//     when the access point it ran on shows something positive: a newer
//     connection, its close, or the session's owner changing;
//  5. Spotify refused the key (an auth error): the existing silent rebuild
//     and one more open, sign-in asked for when the credential cannot make
//     a session, never a browser;
//  6. anything else, and any error with no origin: final.
//
// Rows 2 to 5 need the error's origin (originError): the bundle and the
// session build it ran on. Everything the recovery does is about that build,
// never about one read before or after it. What it does is decided under
// the session's lock (Session.decide, DR), before every wait and after every
// wake. Whatever row it is, the play gets one recovery: at most two opens
// that start librespot work, at most two key writes (librespot sends none
// again), at most one rebuild, no browser. A failure of the second open is
// the play's result, except that an auth error then asks for sign-in, for
// that open's own build.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/audio"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/playlist"
)

// recoveryTimeout bounds a recovery's decisions, its wait and its rebuild,
// as the silent reconnect always was. A variable, for tests.
var recoveryTimeout = 30 * time.Second

// loss is what a failed open's error says, for recovery.
type loss int

const (
	lossFinal    loss = iota // nothing to recover from
	lossClosed               // row 2: the access point closed for good
	lossReplaced             // row 3: the key request's connection was replaced
	lossSendFail             // row 4: the key request's write failed
	lossAuth                 // row 5: Spotify refused the key
)

// lossOf classifies err, the precedence table above, and returns its origin
// for every row but the final one.
func lossOf(err error) (loss, *originError) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return lossFinal, nil
	}
	var oe *originError
	if !errors.As(err, &oe) || oe.origin == nil {
		return lossFinal, nil
	}
	var replaced *audio.ConnectionReplacedError
	var sendFail *audio.KeySendError
	switch {
	case errors.Is(err, ap.ErrAccesspointClosed):
		return lossClosed, oe
	case errors.As(err, &replaced):
		return lossReplaced, oe
	case errors.As(err, &sendFail):
		return lossSendFail, oe
	case isAuthError(err):
		return lossAuth, oe
	}
	return lossFinal, nil
}

// recoveryHandle is what a recovery holds of the open that failed: its
// bundle, its build and, for a failed write, the connection generation the
// write was on. It reads only what is stable or locked: the bundle's access
// point (Generation, Done), its retirement, and the session under its lock.
// It takes no reference on the bundle and starts no librespot work on it.
// It is kept through the last decision and the last open that use it, then
// released once.
type recoveryHandle struct {
	kind   loss
	origin *playback
	gen    uint64 // the origin's session build
	sent   uint64 // row 4: the write's connection generation
	once   sync.Once
}

func (h *recoveryHandle) release() { h.once.Do(func() { h.origin = nil }) }

// action is what a decision says to do.
type action int

const (
	actFinal   action = iota // stop with the decision's error
	actOpen                  // open once more on the session's current bundle
	actRebuild               // rebuild the origin's build, then open
	actWait                  // row 4: nothing positive yet; wait
)

// decide is the deciding-and-registration section (DR). Under the session's
// lock, in this order: the step's context, the session's close, a change of
// owner, then the origin's access point, read once (its generation and its
// channel as one pair) and its Done, then the row. When the answer is to
// wait it also returns what to wait on, captured in the same section: the
// session's owner channel and the generation channel of that same pair. So
// a publication or a close after the decision closes the channel it
// captured, and a new connection on the origin closes the other.
func (s *Session) decide(ctx context.Context, h *recoveryHandle) (act action, ownerCh, changed <-chan struct{}, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return actFinal, nil, nil, err
	}
	if s.closed {
		return actFinal, nil, nil, errSessionClosed
	}
	if s.play != h.origin {
		return actOpen, nil, nil, nil
	}
	og, och := accessPointGeneration(h.origin)
	select {
	case <-accessPointDone(h.origin):
		return actRebuild, nil, nil, nil
	default:
	}
	switch h.kind {
	case lossClosed, lossAuth:
		return actRebuild, nil, nil, nil
	case lossReplaced:
		return actOpen, nil, nil, nil
	case lossSendFail:
		if og > h.sent {
			return actOpen, nil, nil, nil
		}
		return actWait, s.ownerChLocked(), och, nil
	}
	return actFinal, nil, nil, errors.New("spotify: no recovery for this failure")
}

// decision runs DR, and while it says to wait, waits for anything that can
// change its answer and runs it again.
func (s *Session) decision(ctx context.Context, h *recoveryHandle) (action, error) {
	for {
		act, ownerCh, changed, err := s.decide(ctx, h)
		if act != actWait {
			return act, err
		}
		if decisionHook != nil {
			decisionHook("before-wait")
		}
		select {
		case <-ownerCh:
		case <-changed:
		case <-accessPointDone(h.origin):
		case <-h.origin.ctx.Done():
		case <-ctx.Done():
		}
	}
}

// decisionHook, when set by a test, runs at the named points of a recovery.
var decisionHook func(point string)

// recoverStream is the play's one recovery after its first open failed with
// err. open opens the track once on the session's current bundle. sess is
// the session the play is on; nothing is read from it outside its lock.
func recoverStream(sess *Session, err error, open func() (*spotifyStreamer, error)) (*spotifyStreamer, error) {
	kind, oe := lossOf(err)
	if kind == lossFinal {
		return nil, fmt.Errorf("spotify: new stream: %w", err)
	}
	h := &recoveryHandle{kind: kind, origin: oe.origin, gen: oe.gen}
	defer h.release()
	var sendFail *audio.KeySendError
	if errors.As(err, &sendFail) {
		h.sent = sendFail.Gen
	}
	switch kind {
	case lossAuth:
		applog.UserWarn("spotify: stream auth error (%v), attempting silent reconnect...", err)
	default:
		applog.UserWarn("spotify: the connection to Spotify was lost (%v), reconnecting...", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), recoveryTimeout)
	defer cancel()
	if decisionHook != nil {
		decisionHook("begin")
	}
	act, derr := sess.decision(ctx, h)
	if decisionHook != nil {
		decisionHook("decided")
	}
	switch act {
	case actFinal:
		return nil, fmt.Errorf("spotify: new stream: %w (recovering from: %v)", derr, err)
	case actRebuild:
		if rerr := rebuildFor(sess, ctx, h); rerr != nil {
			return nil, rerr
		}
	}
	s, err2 := open()
	// B9: the decision said to open on the origin, and before anything was
	// started its access point closed: the same recovery takes the
	// terminal form, a rebuild of the origin's build and one more open. That
	// open is the recovery's only one to start librespot work.
	var oe2 *originError
	if act == actOpen && errors.As(err2, &oe2) && oe2.beforeWork && oe2.origin == h.origin {
		if decisionHook != nil {
			decisionHook("terminal-at-acquisition")
		}
		if rerr := rebuildFor(sess, ctx, h); rerr != nil {
			return nil, rerr
		}
		s, err2 = open()
	}
	h.release()
	if err2 == nil {
		return s, nil
	}
	if !isAuthError(err2) {
		return nil, fmt.Errorf("spotify: new stream after silent reconnect: %w", err2)
	}
	// Still refused after the one recovery: ask for sign-in, for the build
	// that refused it, never a browser.
	applog.UserWarn("spotify: stream still failing after silent reconnect (%v); sign-in required", err2)
	if errors.As(err2, &oe2) {
		sess.refuseSignIn(oe2.gen)
	}
	return nil, fmt.Errorf("spotify: stream auth error after silent reconnect: %w", playlist.ErrNeedsAuth)
}

// rebuildFor rebuilds the playback of the handle's build (a no-op when the
// session has moved past it) and says what a failure means: a credential
// that can no longer make a session asks for sign-in, for that build, with
// no browser; any other failure is the play's.
func rebuildFor(sess *Session, ctx context.Context, h *recoveryHandle) error {
	err := sessionReconnect(sess, ctx, h.gen)
	if err == nil {
		return nil
	}
	if !needsSignIn(err) {
		applog.UserWarn("spotify: silent reconnect failed (%v)", err)
		if h.kind == lossAuth {
			return fmt.Errorf("spotify: stream auth error, silent reconnect failed: %w", err)
		}
		return fmt.Errorf("spotify: new stream: the connection to Spotify was lost, and reconnecting failed: %w", err)
	}
	applog.UserWarn("spotify: silent reconnect failed (%v); sign-in required", err)
	sess.refuseSignIn(h.gen) // so a sign-in replaces this session
	return fmt.Errorf("spotify: stream auth error, silent reconnect failed: %w", playlist.ErrNeedsAuth)
}
