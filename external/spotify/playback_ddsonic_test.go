package spotify

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"

	"github.com/bjarneo/cliamp/playlist"
)

// playKit stands in for librespot under the playback bundles of a test.
type playKit struct {
	t  *testing.T
	mu sync.Mutex
	// done is each bundle's access point's Done channel; events records, in
	// order, "ap-close" and "player-close" per bundle.
	done   map[*playback]chan struct{}
	events []string
	// apCloseHolds makes the access point's close block after it has closed
	// Done, as librespot's does while it reconnects; apCloseIsDeaf makes it
	// close nothing at all.
	apCloseHolds  chan struct{}
	apCloseIsDeaf bool
	// open is what a stream setup does. The default returns a stream.
	open  func(b *playback, ctx context.Context, client *http.Client) (*librespotPlayer.Stream, error)
	opens atomic.Int32
	// gens is each bundle's access point's connection generation, from 1,
	// and the channel closed when it moves on (advance).
	gens map[*playback]*apGen
}

type apGen struct {
	gen     uint64
	changed chan struct{}
}

func newPlayKit(t *testing.T) *playKit {
	t.Helper()
	quiesce(t)
	k := &playKit{t: t, done: map[*playback]chan struct{}{}, gens: map[*playback]*apGen{}}
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		return &librespotPlayer.Stream{Source: &closableSource{}}, nil
	}
	origOpen, origAP, origDone, origClose, origHook, origGen := playerNewStream, closeAccessPoint, accessPointDone, closeLibrespot, openHook, accessPointGeneration
	playerNewStream = func(b *playback, ctx context.Context, client *http.Client, _ librespot.SpotifyId, _ int) (*librespotPlayer.Stream, error) {
		k.opens.Add(1)
		return k.open(b, ctx, client)
	}
	accessPointDone = func(b *playback) <-chan struct{} { return k.doneOf(b) }
	accessPointGeneration = func(b *playback) (uint64, <-chan struct{}) {
		k.mu.Lock()
		defer k.mu.Unlock()
		g := k.genLocked(b)
		return g.gen, g.changed
	}
	closeAccessPoint = func(b *playback) {
		if k.apCloseIsDeaf {
			return
		}
		k.record("ap-close")
		k.mu.Lock()
		ch := k.doneLocked(b)
		select {
		case <-ch:
		default:
			close(ch) // the first thing librespot's close does
		}
		holds := k.apCloseHolds
		k.mu.Unlock()
		if holds != nil {
			<-holds
		}
	}
	closeLibrespot = func(*librespotPlayer.Player, *session.Session) { k.record("player-close") }
	t.Cleanup(func() {
		playerNewStream, closeAccessPoint, accessPointDone, closeLibrespot, openHook, accessPointGeneration = origOpen, origAP, origDone, origClose, origHook, origGen
	})
	settleBackground(t)
	return k
}

func (k *playKit) doneLocked(b *playback) chan struct{} {
	if k.done[b] == nil {
		k.done[b] = make(chan struct{})
	}
	return k.done[b]
}

func (k *playKit) genLocked(b *playback) *apGen {
	if k.gens[b] == nil {
		k.gens[b] = &apGen{gen: 1, changed: make(chan struct{})}
	}
	return k.gens[b]
}

// advance puts b's access point on its next connection generation.
func (k *playKit) advance(b *playback) {
	k.mu.Lock()
	defer k.mu.Unlock()
	g := k.genLocked(b)
	close(g.changed)
	k.gens[b] = &apGen{gen: g.gen + 1, changed: make(chan struct{})}
}

func (k *playKit) doneOf(b *playback) chan struct{} {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.doneLocked(b)
}

func (k *playKit) record(event string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.events = append(k.events, event)
}

func (k *playKit) count(event string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, e := range k.events {
		if e == event {
			n++
		}
	}
	return n
}

// closableSource is a decoder as librespot's MP3 and FLAC ones are: closed
// by Close() error. plainCloser is as its Vorbis one: Close().
type closableSource struct {
	fakeSource
	closes atomic.Int32
}

func (s *closableSource) Close() error { s.closes.Add(1); return nil }

type plainCloser struct {
	fakeSource
	closes atomic.Int32
}

func (s *plainCloser) Close() { s.closes.Add(1) }

var testTrack = func() librespot.SpotifyId {
	id, err := librespot.SpotifyIdFromUri(testTrackURI)
	if err != nil {
		panic(err)
	}
	return *id
}()

type opened struct {
	stream *librespotPlayer.Stream
	cancel context.CancelFunc
	err    error
}

// openAsync opens a track on s and delivers what came of it.
func openAsync(s *Session, ctx context.Context) chan opened {
	out := make(chan opened, 1)
	go func() {
		stream, cancel, err := s.NewStream(ctx, testTrack, 320)
		out <- opened{stream, cancel, err}
	}()
	return out
}

func within[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

func bundleOf(s *Session) *playback {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.play
}

// hookAt makes the open's hook block at point until the returned release is
// called, and closes reached when it gets there.
func hookAt(point string) (reached chan struct{}, release func()) {
	reached = make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	openHook = func(p string) {
		if p == point {
			once.Do(func() { close(reached); <-gate })
		}
	}
	return reached, func() { close(gate) }
}

// No caller ever runs librespot's close, which can block for as long as
// librespot is reconnecting: a session is replaced, closed, and the provider
// closed, while a bundle's access point close is still stuck. The workers
// were released (its Done channel closed first), and the player is closed.
func TestRetirementNeverMakesItsCallerWait(t *testing.T) {
	k := newPlayKit(t)
	k.apCloseHolds = make(chan struct{})
	t.Cleanup(func() { close(k.apCloseHolds) })
	stubSignIn(t, nil)
	sess := goodSession()
	first := bundleOf(sess)
	p := New(sess, "client", 320)

	sess.refuseSignIn(sess.generation())
	if err := returnsWithin(t, 5*time.Second, "a sign-in that replaces the session", p.Authenticate); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced bundle's player was never closed while its access point close was stuck")
	}
	// The session works on its new bundle meanwhile.
	if o := within(t, "an open on the new bundle", openAsync(sess, context.Background())); o.err != nil {
		t.Fatalf("an open on the new bundle = %v", o.err)
	}
	if sess.needsSignIn() {
		t.Error("the session needs sign-in after the sign-in")
	}
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	within(t, "Provider.Close", closed)
	// Each retired bundle's close is started, by its own tail.
	waitFor(t, "an access point close per retired bundle", func() bool { return k.count("ap-close") == 2 })
}

// A setup that ignores its context, as librespot's audio key request does,
// is released by the bundle's retirement: the access point's Done channel.
// The caller does not wait for it, and the player is closed only after it
// has returned.
func TestRetireUnblocksStalledSetup(t *testing.T) {
	k := newPlayKit(t)
	entered := make(chan struct{})
	var returned atomic.Bool
	k.open = func(b *playback, _ context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
		close(entered)
		<-k.doneOf(b) // deaf to its context
		if k.count("player-close") != 0 {
			k.t.Error("the player was closed while a setup was inside it")
		}
		returned.Store(true)
		return nil, errors.New("accesspoint closed")
	}
	sess := goodSession()
	b := bundleOf(sess)
	open := openAsync(sess, context.Background())
	<-entered

	closed := make(chan struct{})
	go func() { sess.Close(); close(closed) }()
	within(t, "Close", closed)
	if o := within(t, "the open", open); !errors.Is(o.err, errSessionChanged) || o.stream != nil {
		t.Errorf("the open on a retired bundle = %v, want errSessionChanged", o.err)
	}
	within(t, "the bundle's teardown", b.gone)
	if !returned.Load() || k.count("player-close") != 1 {
		t.Errorf("the worker returned %t, player closes %d; want the player closed once, after the worker", returned.Load(), k.count("player-close"))
	}
}

// When nothing releases a setup, nothing waits for it either: the session
// is closed, and the player is left alone until the worker returns.
func TestNobodyWaitsForAStuckSetup(t *testing.T) {
	k := newPlayKit(t)
	k.apCloseIsDeaf = true
	entered, release := make(chan struct{}), make(chan struct{})
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		close(entered)
		<-release
		return nil, errors.New("gave up")
	}
	sess := goodSession()
	b := bundleOf(sess)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	open := openAsync(sess, ctx)
	<-entered
	if o := within(t, "the open, at its deadline", open); !errors.Is(o.err, context.DeadlineExceeded) {
		t.Fatalf("the open = %v, want its deadline", o.err)
	}
	closed := make(chan struct{})
	go func() { sess.Close(); close(closed) }()
	within(t, "Close", closed)
	time.Sleep(20 * time.Millisecond)
	if k.count("player-close") != 0 {
		t.Fatal("the player was closed under a setup that is still inside it")
	}
	close(release)
	close(k.doneOf(b))
	within(t, "the bundle's teardown", b.gone)
	if k.count("player-close") != 1 {
		t.Errorf("the player was closed %d times, want once", k.count("player-close"))
	}
}

// Who owns an open's result, at each point where the caller and the worker
// can pass each other. A stream that is not returned is disposed of by its
// one owner: its HTTP lifetime ended and its decoder closed, once.
func TestOpenHandoff(t *testing.T) {
	type env struct {
		k      *playKit
		sess   *Session
		source *closableSource
		client atomic.Pointer[http.Client]
	}
	setup := func(t *testing.T) *env {
		e := &env{k: newPlayKit(t), source: &closableSource{}}
		e.k.open = func(_ *playback, _ context.Context, client *http.Client) (*librespotPlayer.Stream, error) {
			e.client.Store(client)
			return &librespotPlayer.Stream{Source: e.source}, nil
		}
		e.sess = goodSession()
		return e
	}
	// httpEnded reports whether the open's HTTP lifetime has been ended.
	httpEnded := func(e *env) bool {
		return e.client.Load().Transport.(streamContextTransport).ctx.Err() != nil
	}
	disposed := func(t *testing.T, e *env) {
		t.Helper()
		builds.Wait()
		if n := e.source.closes.Load(); n != 1 || !httpEnded(e) {
			t.Errorf("the unaccepted stream: decoder closed %d times, HTTP lifetime ended %t; want once and ended", n, httpEnded(e))
		}
	}

	t.Run("the worker published, then the caller cancelled", func(t *testing.T) {
		e := setup(t)
		reached, release := hookAt("worker-published") // its swap is done, its send is held
		ctx, cancel := context.WithCancel(context.Background())
		open := openAsync(e.sess, ctx)
		<-reached
		cancel()
		time.Sleep(10 * time.Millisecond) // the caller is at its own swap, which fails
		release()
		if o := within(t, "the open", open); !errors.Is(o.err, context.Canceled) || o.stream != nil {
			t.Fatalf("the open = %v, %v; want the cancellation and no stream", o.stream, o.err)
		}
		disposed(t, e)
	})
	t.Run("the worker published, then the bundle was retired", func(t *testing.T) {
		e := setup(t)
		reached, release := hookAt("caller-before-accept")
		open := openAsync(e.sess, context.Background())
		<-reached
		e.sess.Close()
		release()
		if o := within(t, "the open", open); !errors.Is(o.err, errSessionChanged) || o.stream != nil {
			t.Fatalf("the open = %v, %v; want errSessionChanged and no stream", o.stream, o.err)
		}
		disposed(t, e)
	})
	t.Run("the caller gave up first", func(t *testing.T) {
		e := setup(t)
		reached, release := hookAt("worker-before-publish")
		ctx, cancel := context.WithCancel(context.Background())
		open := openAsync(e.sess, ctx)
		<-reached
		cancel()
		if o := within(t, "the open", open); !errors.Is(o.err, context.Canceled) {
			t.Fatalf("the open = %v, want the cancellation", o.err)
		}
		release() // the worker's swap fails now: the stream is its to dispose of
		e.sess.Close()
		tails.Wait()
		disposed(t, e)
	})
	t.Run("the caller's context ended before it accepted", func(t *testing.T) {
		for _, arrangement := range []string{"between the result and the lock", "while the lock was held"} {
			e := setup(t)
			reached, release := hookAt("caller-before-accept")
			ctx, cancel := context.WithCancel(context.Background())
			open := openAsync(e.sess, ctx)
			<-reached
			if arrangement == "while the lock was held" {
				e.sess.mu.Lock()
				release()
				time.Sleep(10 * time.Millisecond) // the caller waits for the lock
				cancel()
				e.sess.mu.Unlock()
			} else {
				cancel()
				release()
			}
			if o := within(t, "the open", open); !errors.Is(o.err, context.Canceled) || o.stream != nil {
				t.Fatalf("%s: the open = %v, %v; want the cancellation and no stream", arrangement, o.stream, o.err)
			}
			disposed(t, e)
		}
	})
	t.Run("accepted, then cancelled and retired", func(t *testing.T) {
		e := setup(t)
		ctx, cancel := context.WithCancel(context.Background())
		o := within(t, "the open", openAsync(e.sess, ctx))
		if o.err != nil || o.stream == nil {
			t.Fatalf("the open = %v", o.err)
		}
		cancel()       // the setup's context, after the stream was accepted
		e.sess.Close() // and its bundle retired
		tails.Wait()
		if httpEnded(e) || e.source.closes.Load() != 0 {
			t.Fatalf("an established stream was ended by what came after: HTTP ended %t, decoder closed %d", httpEnded(e), e.source.closes.Load())
		}
		o.cancel()
		if !httpEnded(e) {
			t.Error("the stream's own cancel did not end its HTTP lifetime")
		}
	})
	t.Run("a setup that fails or panics", func(t *testing.T) {
		for _, how := range []string{"fails", "panics"} {
			e := setup(t)
			e.k.open = func(_ *playback, _ context.Context, client *http.Client) (*librespotPlayer.Stream, error) {
				e.client.Store(client)
				if how == "panics" {
					panic("decoder blew up")
				}
				return nil, errors.New("no such track")
			}
			o := within(t, "the open", openAsync(e.sess, context.Background()))
			if o.err == nil || o.stream != nil || !httpEnded(e) {
				t.Errorf("a setup that %s: open = %v, HTTP lifetime ended %t; want an error and it ended", how, o.err, httpEnded(e))
			}
			b := bundleOf(e.sess)
			e.sess.Close()
			within(t, "the bundle's teardown: the reference was released", b.gone)
		}
	})
	t.Run("a decoder closed by Close()", func(t *testing.T) {
		e := setup(t)
		vorbis := &plainCloser{}
		e.k.open = func(_ *playback, _ context.Context, client *http.Client) (*librespotPlayer.Stream, error) {
			e.client.Store(client)
			return &librespotPlayer.Stream{Source: vorbis}, nil
		}
		reached, release := hookAt("caller-before-accept")
		open := openAsync(e.sess, context.Background())
		<-reached
		e.sess.Close()
		release()
		within(t, "the open", open)
		if vorbis.closes.Load() != 1 {
			t.Errorf("the decoder was closed %d times, want once", vorbis.closes.Load())
		}
	})
}

// An open whose caller has stopped asking is not fed to recovery either: a
// key error that arrives for it is not returned, so the provider reconnects
// nothing for it.
func TestEndedOpenWithAKeyErrorIsNotRecovered(t *testing.T) {
	k := newPlayKit(t)
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		return nil, fmt.Errorf("failed retrieving audio key: %w", &audio.KeyProviderError{Code: 1})
	}
	sess := goodSession()
	reached, release := hookAt("caller-before-accept")
	ctx, cancel := context.WithCancel(context.Background())
	open := openAsync(sess, ctx)
	<-reached
	cancel()
	release()
	o := within(t, "the open", open)
	if !errors.Is(o.err, context.Canceled) || isAuthError(o.err) {
		t.Errorf("the ended open = %v, want its cancellation and no auth error", o.err)
	}
}

// An error that a retired bundle produced says nothing about the session
// that replaced it. The open ends with "session changed", which is not an
// auth error: the provider reconnects nothing, refuses nothing, and the
// session stays on the build it is on.
func TestRetiredBundleErrorIsNotRecovered(t *testing.T) {
	k := newPlayKit(t)
	stubSignIn(t, nil)
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		return nil, fmt.Errorf("failed retrieving audio key: %w", &audio.KeyProviderError{Code: 1})
	}
	var reconnects atomic.Int32
	orig := sessionReconnect
	sessionReconnect = func(s *Session, ctx context.Context, gen uint64) error {
		reconnects.Add(1)
		return orig(s, ctx, gen)
	}
	t.Cleanup(func() { sessionReconnect = orig })
	sess := goodSession()
	p := New(sess, "client", 320)

	reached, release := hookAt("caller-before-accept") // the key error is published
	result := make(chan error, 1)
	go func() { _, _, _, err := p.NewStreamer(testTrackURI); result <- err }()
	<-reached
	sess.refuseSignIn(sess.generation())
	if err := p.Authenticate(); err != nil { // the session is replaced meanwhile
		t.Fatal(err)
	}
	gen := sess.generation()
	release()

	err := within(t, "NewStreamer", result)
	if !errors.Is(err, errSessionChanged) || errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("NewStreamer() = %v, want errSessionChanged and no sign-in request", err)
	}
	if reconnects.Load() != 0 || sess.signInRefused() || sess.generation() != gen || k.opens.Load() != 1 {
		t.Errorf("the old bundle's error was acted on: %d reconnects, refused %t, build %d -> %d, %d opens",
			reconnects.Load(), sess.signInRefused(), gen, sess.generation(), k.opens.Load())
	}
}

// The control: the same error from the bundle the session is on is an auth
// error, and the provider recovers as it always did: one silent rebuild of
// playback, one more try, and then a request to sign in.
func TestLiveBundleKeyErrorIsRecovered(t *testing.T) {
	storeCreds(t)
	b := stubBuild(t)
	k := newPlayKit(t)
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		return nil, fmt.Errorf("failed retrieving audio key: %w", &audio.KeyProviderError{Code: 1})
	}
	h := fakeSpotifyHTTP(t, tokenOK, apiOK)
	sess := goodSession()
	p := New(sess, "client", 320)
	gen, token := sess.generation(), tokenOf(sess)

	_, _, _, err := p.NewStreamer(testTrackURI)
	if !errors.Is(err, playlist.ErrNeedsAuth) || !sess.signInRefused() {
		t.Errorf("NewStreamer() = %v, refused %t; want a request to sign in", err, sess.signInRefused())
	}
	if k.opens.Load() != 2 || b.librespotSessions.Load() != 1 || sess.generation() != gen+1 {
		t.Errorf("%d opens, %d playback rebuilds, build %d -> %d; want 2, 1 and the next build", k.opens.Load(), b.librespotSessions.Load(), gen, sess.generation())
	}
	// The rebuild refreshed nothing and kept the token.
	if h.tokenCalls.Load() != 0 || tokenOf(sess) != token {
		t.Errorf("the playback rebuild made %d token requests and the token is %q, was %q", h.tokenCalls.Load(), tokenOf(sess), token)
	}
}

// A silent reconnect rebuilds playback only. It refreshes no token and keeps
// the session's coordinator, lease and refresh token; it records the rebuilt
// identity; and it starts the next build, which ends the writes of an action
// that began before it. A reconnect asked for a build that has been replaced
// does nothing.
func TestPlaybackRebuildKeepsTokens(t *testing.T) {
	path := storeCreds(t)
	b := stubBuild(t)
	h := fakeSpotifyHTTP(t, tokenOK, apiOK)
	sess := goodSession()
	p := New(sess, "client", 320)
	tokens, gen := sess.viewNow().tokens, sess.generation()
	pin := p.newSignInPin()
	sess.refuseSignIn(gen) // playback was refused; the rebuild is its cure

	if err := sess.Reconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := sess.viewNow()
	if v.tokens != tokens || revOf(tokens) != 1 || tokenOf(sess) != "access" || h.tokenCalls.Load() != 0 {
		t.Errorf("the rebuild touched the token: coordinator kept %t, revision %d, token %q, %d token requests",
			v.tokens == tokens, revOf(tokens), tokenOf(sess), h.tokenCalls.Load())
	}
	if v.gen != gen+1 || v.playbackRefused || pin.holds(sess) {
		t.Errorf("after the rebuild: build %d -> %d, playback refused %t, an older action still holds %t", gen, v.gen, v.playbackRefused, pin.holds(sess))
	}
	if got := mustRead(t, path); !strings.Contains(got, `"username":"user"`) || !strings.Contains(got, `"refresh_token":"old"`) {
		t.Errorf("the credential file holds %q, want the rebuilt identity beside the same refresh token", got)
	}
	if b.disposed() != 1 {
		t.Errorf("the replaced bundle was torn down %d times, want once", b.disposed())
	}
	if err := sess.reconnectPlayback(context.Background(), gen); err != nil || b.librespotSessions.Load() != 1 {
		t.Errorf("a reconnect for the replaced build = %v after %d rebuilds, want nothing done", err, b.librespotSessions.Load())
	}

	// A token refused by Spotify is not cured by a playback rebuild.
	tokens.refuse(lease{tokens, 1})
	if err := sess.Reconnect(context.Background()); err != nil || !sess.needsSignIn() {
		t.Errorf("after a rebuild of a session whose token was refused: %v, needs sign-in %t", err, sess.needsSignIn())
	}
}

// The HTTP lifetime of an open that is not accepted is ended at once, with
// librespot's real chunked reader on the other side of our real stream
// client: its first request, which does not follow the setup's context, is
// released by the open's handle when the caller gives up or the bundle is
// retired. The worker then returns, and the bundle's teardown goes on.
func TestUnacceptedOpenEndsItsFirstChunkRequest(t *testing.T) {
	for _, stall := range []string{"before the headers", "in the body"} {
		for _, ending := range []string{"the caller's deadline", "the bundle's retirement"} {
			t.Run(stall+", "+ending, func(t *testing.T) {
				k := newPlayKit(t)
				asked, hold := make(chan struct{}), make(chan struct{})
				var once sync.Once
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if stall == "in the body" {
						w.Header().Set("Content-Range", "bytes 0-9/1000000")
						w.Header().Set("Content-Length", "10")
						w.WriteHeader(http.StatusPartialContent)
						w.(http.Flusher).Flush()
					}
					once.Do(func() { close(asked) })
					select {
					case <-hold:
					case <-r.Context().Done():
					}
				}))
				t.Cleanup(func() { close(hold); srv.CloseClientConnections(); srv.Close() })
				// The stream client goes to the test server, as it would to the CDN.
				origTransport := http.DefaultTransport
				http.DefaultTransport = srv.Client().Transport
				t.Cleanup(func() { http.DefaultTransport = origTransport })
				workerDone := make(chan error, 1)
				k.open = func(_ *playback, _ context.Context, client *http.Client) (*librespotPlayer.Stream, error) {
					_, err := audio.NewHttpChunkedReader(&librespot.NullLogger{}, client, srv.URL)
					workerDone <- err
					return nil, err
				}
				sess := goodSession()
				b := bundleOf(sess)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				open := openAsync(sess, ctx)
				within(t, "librespot's first chunk request", asked)

				want := context.Canceled
				if ending == "the caller's deadline" {
					cancel()
				} else {
					sess.Close()
					want = errSessionChanged
				}
				if o := within(t, "the open", open); !errors.Is(o.err, want) {
					t.Fatalf("the open = %v, want %v", o.err, want)
				}
				if err := within(t, "librespot's reader to give up its request", workerDone); err == nil {
					t.Fatal("the stalled first chunk request succeeded")
				}
				sess.Close()
				within(t, "the bundle's teardown", b.gone)
			})
		}
	}
}
