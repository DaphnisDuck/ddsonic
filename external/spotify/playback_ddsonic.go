package spotify

// ddsonic: what a session plays with, and the opening of a track on it.
//
// Upstream held the session's lock across librespot's stream setup and its
// teardown, so that a player was never closed under a stream being opened.
// That lock is no longer held across either. A session's librespot session
// and player are one playback bundle; a track open takes a reference to the
// bundle for as long as its worker is inside librespot; and a bundle that is
// replaced or closed is retired: detached under the lock, then told to stop,
// then torn down on its own goroutine once its workers have returned.
//
// Two things about go-librespot shape this:
//   - A stream setup can outlive its context in places (its HTTP work, a
//     write that has begun). What releases it is the access point's Done
//     channel, or the open's own HTTP lifetime.
//   - Accesspoint.Close can block: it closes Done first, then waits for a
//     lock its receive loop holds across a reconnect. So no caller ever
//     calls it; the bundle's tail does, on a goroutine of its own.
//
// A track open has two lifetimes. The setup is librespot's NewStream call,
// bounded by the caller's deadline and ended when the bundle is retired. The
// stream is what that call returns; it reads its audio over an HTTP context
// of its own, which nothing here ends once the stream has been accepted.
//
// The access point's connection (the contract tests in sdkcontract pin what
// go-librespot does, with ddsonic's patch 0002, docs/ddsonic/upstream.md):
//   - Nothing reads the connection until something calls Receive, and
//     librespot's first caller is the first audio key request. Until then
//     pings go unanswered and a close by the peer goes unnoticed. So a
//     bundle's access point is serviced from the moment the bundle is made
//     (serviceAccessPoint): librespot then answers pings, and reconnects when
//     the connection ends, while nothing is being opened.
//   - With patch 0002 the key provider ends a request whose connection was
//     replaced (ConnectionReplacedError) or whose write failed (KeySendError)
//     at once, from positive evidence, and never sends one again; and an
//     access point whose reconnect fails for good closes itself (Done).
//
// An open that ends with an error the session accepted carries where it came
// from (originError): the bundle and the session build it ran on. What the
// provider does with it, with the one recovery a play has, is in
// recovery_ddsonic.go.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"sync/atomic"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"
	"github.com/devgianlu/go-librespot/vorbis"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/memdiag"
)

// errSessionChanged ends a track open whose session was replaced or closed
// before the stream was accepted. It is not an auth error: nothing recovers
// from it, and the user plays the track again.
var errSessionChanged = errors.New("spotify: the session changed while the track was opening; try again")

// errAccessPointClosed ends a track open on a bundle whose access point
// librespot has closed for good, while the bundle is still the session's.
// It is not an auth error: it says the connection is gone, and nothing about
// the sign-in.
var errAccessPointClosed = fmt.Errorf("spotify: the connection to Spotify is closed: %w", ap.ErrAccesspointClosed)

// originError is an error a track open ended with, and that the session
// accepted (the open's bundle was still the session's and its caller still
// asking), with where it came from: the bundle and the session build it ran
// on. cause is the error itself, so errors.Is and errors.As see through it.
//
// beforeWork is set on the one origin error made before any librespot work
// was started: the bundle's access point had already closed when the open
// went to take it.
type originError struct {
	gen        uint64
	origin     *playback
	cause      error
	beforeWork bool
}

func (e *originError) Error() string { return e.cause.Error() }
func (e *originError) Unwrap() error { return e.cause }

// The librespot calls a bundle makes, as variables so a test can stand in
// for them and hold them.
var (
	// playerNewStream is librespot's stream setup on a bundle's player.
	playerNewStream = func(b *playback, ctx context.Context, client *http.Client, id librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, error) {
		if b.player == nil {
			return nil, fmt.Errorf("spotify: session closed")
		}
		return b.player.NewStream(ctx, client, id, bitrate, 0)
	}
	// closeAccessPoint starts the close of a bundle's access point. It may block for as long as librespot is reconnecting.
	closeAccessPoint = func(b *playback) {
		if b.sess != nil {
			b.sess.Accesspoint().Close()
		}
	}
	// accessPointDone is closed once a close of the access point has begun:
	// librespot's waits that ignore a context end with it.
	accessPointDone = func(b *playback) <-chan struct{} {
		if b.sess == nil {
			return closedChan
		}
		return b.sess.Accesspoint().Done()
	}
	// accessPointGeneration is the access point's connection generation and
	// the channel closed when it moves on (patch 0002). It takes no lock.
	accessPointGeneration = func(b *playback) (uint64, <-chan struct{}) {
		if b.sess == nil {
			return 0, nil
		}
		return b.sess.Accesspoint().Generation()
	}
	// serviceAccessPoint starts librespot's receive loop on a bundle's
	// access point: Receive with no packet types registers nothing, and
	// starts the loop that answers pings and reconnects. It is called once,
	// when the bundle is made, by a builder that holds no lock.
	serviceAccessPoint = func(b *playback) {
		if b.sess != nil {
			b.sess.Accesspoint().Receive()
		}
	}
	// openHook, when set by a test, runs at the named points of a track open.
	openHook func(point string)
)

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// tails counts the bundle tails still running, so a test can wait for
// teardown, which no production caller does.
var tails sync.WaitGroup

// playback is one librespot session and its player, and the identity
// librespot gave the session.
type playback struct {
	sess     *session.Session
	player   *librespotPlayer.Player
	username string
	data     []byte
	devID    string

	ctx    context.Context // ends when the bundle is retired
	cancel context.CancelFunc
	users  sync.WaitGroup // track-open workers inside librespot
	retire sync.Once
	gone   chan struct{} // closed when the tail has finished
}

func newPlayback(sess *session.Session, username string, data []byte, devID string) *playback {
	ctx, cancel := context.WithCancel(context.Background())
	b := &playback{sess: sess, username: username, data: data, devID: devID, ctx: ctx, cancel: cancel, gone: make(chan struct{})}
	serviceAccessPoint(b) // from here on: see the note at the top
	noteBundle("created", 0)
	return b
}

// retireNow retires a bundle that has been detached from its session: no
// track open can take it any more. It tells the opens in progress to stop
// and starts the tail, and returns; it makes no librespot call, and the
// caller waits for nothing. It is safe to call more than once, and on nil.
func (b *playback) retireNow() {
	if b == nil {
		return
	}
	b.retire.Do(func() {
		noteBundle("retired", 1)
		b.cancel()
		tails.Add(1)
		go b.tail()
	})
}

// tail tears a retired bundle down. The access point is closed first, on a
// goroutine of its own because that call can block; its Done channel, which
// closes as that call begins, releases the workers that a context does not,
// and ends every key request the bundle's key provider still has. The player
// is closed only after every worker has returned. No bound is claimed for
// when the connection closes or the tail ends: while librespot is
// reconnecting they wait for it, and nothing waits for them.
func (b *playback) tail() {
	defer tails.Done()
	defer close(b.gone)
	defer noteBundle("tail_finished", -1)
	go closeAccessPoint(b)
	<-accessPointDone(b)
	b.users.Wait()
	closeLibrespot(b.player, b.sess)
}

// An open's result is published by its worker and accepted, or not, by its
// caller. The state says who owns the result.
const (
	openPending   int32 = iota // the worker is inside librespot
	openReady                  // the worker published a result: the caller owns it
	openAbandoned              // the caller gave up first: the worker owns what it gets
)

type openResult struct {
	stream *librespotPlayer.Stream
	err    error
}

// openHandle ends an open's HTTP lifetime, once. Until a stream is accepted
// every way out of the open calls it; an accepted stream's caller gets it as
// the stream's cancel function, and nothing here calls it again.
type openHandle struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (h *openHandle) end() { h.once.Do(h.cancel) }

// disposeStream releases a stream that was never accepted. Its disposer is
// its one owner, librespot's call has returned, and no reader was given it:
// the HTTP context is ended and the decoder closed, once. (An accepted
// stream's teardown is the streamer's, and is not this.)
func disposeStream(stream *librespotPlayer.Stream, h *openHandle, seq uint64, why string, outcome error) {
	h.end()
	noteDiscarded(seq, why, outcome, stream)
	if stream == nil {
		return
	}
	releaseDecoder(stream.Source)
}

// releaseDecoder closes a decoder nobody reads, when closing it is safe.
//
// An Ogg Vorbis decoder is NOT closed (review round 9,
// R9-4; the owner's decision of 2026-10-13, in plan.md's
// Decisions log).
//
// go-librespot v0.9.1's vorbis.Decoder.Close can abort the
// process. The abort is in Close itself, on a decoder nobody has
// read, and cannot be avoided from outside the SDK: the fields
// involved are unexported. So a Vorbis decoder that was never
// accepted is left unreleased, as an established one is by the
// streamer: its native memory may stay retained.
//
// The owner accepted that as a documented 1.0 resource
// limitation, to be measured on the next candidate. The owner
// handles the report to go-librespot, outside this repository;
// the SDK is not patched for it.
func releaseDecoder(src librespot.AudioSource) {
	switch src := src.(type) {
	case *vorbis.Decoder:
		// not closed: see above
		noteRelease("skipped", src)
	case io.Closer: // mp3, flac
		noteRelease("requested", src)
		if err := src.Close(); err != nil {
			applog.Warn("spotify: closing an unused stream: %v", err)
		}
		noteRelease("completed", src)
	case interface{ Close() }:
		src.Close()
	}
}

// holdPlayback takes a reference to the session's playback bundle for the
// worker of one stream setup, and the session build it belongs to, both
// under the lock. The open's context comes first: one that has ended, before
// the call or while it waited for the lock, ends the open with its own
// error, and nothing is taken or started. A bundle that is still the
// session's and whose access point librespot has closed is not taken: the
// open fails at once with an origin error on it, made before any librespot
// work, for a caller still asking. A closed session, or one with no bundle,
// fails with no origin.
func (s *Session) holdPlayback(ctx context.Context) (*playback, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	b := s.play
	if s.closed || b == nil {
		return nil, 0, fmt.Errorf("spotify: session closed")
	}
	select {
	case <-accessPointDone(b):
		return nil, 0, &originError{gen: s.gen, origin: b, cause: errAccessPointClosed, beforeWork: true}
	default:
	}
	b.users.Add(1) // under the lock a retirement detaches the bundle with
	noteBundle("worker", 1)
	return b, s.gen, nil
}

// NewStream creates a decoded audio stream for the given Spotify track ID. The
// caller must retain and invoke the returned cancel function for the lifetime
// of a successful stream. ctx bounds setup independently of that lifetime.
//
// ddsonic: no lock is held while librespot sets the stream up. The stream is
// returned only if, once it is ready, the caller is still asking and the
// session is still on the bundle that produced it; that is the moment a
// track open becomes an established stream. Otherwise the open ends with the
// caller's context error or errSessionChanged, whatever librespot answered:
// an error from a bundle that has been retired says nothing about the
// session that replaced it. An error that passes that check is returned as
// an originError on the bundle and build it ran on. It makes one setup and
// never a second.
func (s *Session) NewStream(ctx context.Context, spotID librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
	b, gen, err := s.holdPlayback(ctx)
	if err != nil {
		return nil, nil, err
	}
	seq := memdiag.OpenSeq(ctx)
	noteOpenAttempt(seq)

	streamCtx, streamCancel := context.WithCancel(context.Background())
	h := &openHandle{cancel: streamCancel}
	client := newSpotifyStreamHTTPClient(streamCtx, http.DefaultTransport)
	// The setup ends with the caller's deadline or the bundle's retirement.
	setupCtx, setupCancel := context.WithCancel(ctx)
	stopOnRetire := context.AfterFunc(b.ctx, setupCancel)

	var state atomic.Int32
	results := make(chan openResult, 1)
	go func() {
		defer b.users.Done() // the reference lasts as long as the worker
		defer noteBundle("worker", -1)
		var res openResult
		defer func() {
			// A panic in librespot's stream setup would otherwise take down
			// the whole process and leave the terminal in raw mode; fail
			// the track instead.
			if r := recover(); r != nil {
				applog.Error("spotify: stream setup panicked: %v\n%s", r, debug.Stack())
				res = openResult{err: fmt.Errorf("spotify: stream setup failed: %v", r)}
			}
			if res.err != nil {
				h.end()
			}
			if openHook != nil {
				openHook("worker-before-publish")
			}
			if !state.CompareAndSwap(openPending, openReady) {
				disposeStream(res.stream, h, seq, "caller_gave_up_first", nil) // the caller gave up: ours to release
				return
			}
			if openHook != nil {
				openHook("worker-published")
			}
			results <- res // never blocks; the caller owns it from the swap
		}()
		res.stream, res.err = playerNewStream(b, setupCtx, client, spotID, bitrate)
		noteSetupResult(seq, res)
	}()
	defer func() {
		stopOnRetire()
		setupCancel()
	}()

	gaveUp := func(outcome error) (*librespotPlayer.Stream, context.CancelFunc, error) {
		h.end() // first: this is what releases a worker stalled on its first chunk
		noteGaveUp(seq, outcome)
		if openHook != nil {
			openHook("caller-before-abandon")
		}
		if !state.CompareAndSwap(openPending, openAbandoned) {
			res := <-results // published already: ours, and not wanted
			disposeStream(res.stream, h, seq, "caller_gave_up_after_result", outcome)
		}
		return nil, nil, outcome
	}
	var res openResult
	select {
	case res = <-results:
	case <-ctx.Done():
		return gaveUp(ctx.Err())
	case <-b.ctx.Done():
		return gaveUp(errSessionChanged)
	}

	// The result is validated before anything of it is returned, stream or
	// error alike, and with the lock a retirement takes to detach the bundle.
	if openHook != nil {
		openHook("caller-before-accept")
	}
	s.mu.Lock()
	var outcome error
	switch {
	case ctx.Err() != nil: // the caller has stopped asking
		outcome = ctx.Err()
	case s.closed || s.play != b: // the result is a retired bundle's
		outcome = errSessionChanged
	}
	s.mu.Unlock() // the stream is accepted here, or not at all
	switch {
	case outcome != nil:
		disposeStream(res.stream, h, seq, "rejected_at_accept", outcome)
		return nil, nil, outcome
	case res.err != nil:
		noteAccepted(seq, res)
		// the worker ended the HTTP lifetime; the error is accepted, and
		// carries its origin
		return nil, nil, &originError{gen: gen, origin: b, cause: res.err}
	}
	noteAccepted(seq, res)
	return res.stream, h.end, nil
}
