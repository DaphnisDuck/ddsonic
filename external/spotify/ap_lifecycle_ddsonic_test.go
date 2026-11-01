package spotify

// ddsonic: what a playback bundle does about its access point (it is
// serviced from the moment the bundle is made) and the one recovery a play
// has (recovery_ddsonic.go). These are unit tests: librespot and its access
// point are stood in for, modelled on what the contract tests in sdkcontract
// pin on the real patched SDK. Composed tests that run the real SDK and this
// code together are planned (M3b2) and do not exist yet.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/audio"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/session"

	"github.com/bjarneo/cliamp/playlist"
)

// apModel records what a test's bundles did with their access points.
type apModel struct {
	mu       sync.Mutex
	order    []string // "identity", "service" and "init", as they happen
	serviced map[*playback]int
}

func (m *apModel) note(event string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.order = append(m.order, event)
}

func (m *apModel) events() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

func (m *apModel) servicedTimes(b *playback) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serviced[b]
}

// stubAccessPoints records every bundle's servicing.
func stubAccessPoints(t *testing.T) *apModel {
	t.Helper()
	quiesce(t)
	m := &apModel{serviced: map[*playback]int{}}
	orig := serviceAccessPoint
	serviceAccessPoint = func(b *playback) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.order = append(m.order, "service")
		m.serviced[b]++
	}
	t.Cleanup(func() {
		quiesce(t)
		serviceAccessPoint = orig
	})
	return m
}

// errLostKey is what a track open ends with when its key request was lost on
// a connection nobody serviced.
var errLostKey = fmt.Errorf("failed retrieving audio key: %w", context.DeadlineExceeded)

// agedOpens makes a track open behave as the access point under it would
// after the session has sat idle past its peer's close: lost on a bundle
// nobody serviced, answered on one that was.
func agedOpens(k *playKit, m *apModel) {
	k.open = func(b *playback, _ context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
		if m.servicedTimes(b) == 0 {
			return nil, errLostKey
		}
		return &librespotPlayer.Stream{Source: &closableSource{}}, nil
	}
}

// Every way a session gets a playback bundle, from the stored credential,
// from a browser sign-in and from a silent rebuild, services the bundle's
// access point: once, after librespot's identity was read (the getters wait
// for a reconnect, which only a serviced access point makes), and before
// the player is made and the session can be taken. So the first track
// opened on a session that has sat idle past its peer's close plays.
// Without the call in newPlayback every case fails with the lost key.
func TestAgedFirstPlayOnEveryBuild(t *testing.T) {
	builds := []struct {
		name  string
		build func(t *testing.T) *SpotifyProvider
	}{
		{"stored credential", func(t *testing.T) *SpotifyProvider {
			p := New(nil, "client", 320)
			if err := p.ensureSession(); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"browser sign-in", func(t *testing.T) *SpotifyProvider {
			p := New(nil, "client", 320)
			p.storedRefused = true // nothing stored to build from
			if err := p.Authenticate(); err != nil {
				t.Fatal(err)
			}
			return p
		}},
		{"silent rebuild", func(t *testing.T) *SpotifyProvider {
			sess := goodSession()
			if err := sess.Reconnect(context.Background()); err != nil {
				t.Fatal(err)
			}
			return New(sess, "client", 320)
		}},
	}
	for _, tt := range builds {
		t.Run(tt.name, func(t *testing.T) {
			storeCreds(t)
			b := stubBuild(t)
			k := newPlayKit(t)
			fakeSpotifyHTTP(t, tokenOK, apiOK)
			m := stubAccessPoints(t)
			agedOpens(k, m)
			origID := sessionIdentity
			sessionIdentity = func(s *session.Session) (string, []byte) { m.note("identity"); return origID(s) }
			t.Cleanup(func() { quiesce(t); sessionIdentity = origID })
			b.onInit = func() { m.note("init") }

			p := tt.build(t)
			sess := p.sessionNow()
			if sess == nil {
				t.Fatal("the build left the provider without a session")
			}
			if n := m.servicedTimes(bundleOf(sess)); n != 1 {
				t.Errorf("the session's access point was serviced %d times, want once", n)
			}
			var last []string
			for _, e := range m.events() { // the last build's three events
				if e == "identity" {
					last = nil
				}
				last = append(last, e)
			}
			if fmt.Sprint(last) != "[identity service init]" {
				t.Errorf("the build went %v, want the identity read, then the access point serviced, then the player", last)
			}

			s, _, _, err := p.NewStreamer(testTrackURI)
			if err != nil {
				t.Fatalf("the aged first play = %v, want a stream", err)
			}
			_ = s.Close()
			if k.opens.Load() != 1 {
				t.Errorf("the aged first play took %d stream setups, want 1", k.opens.Load())
			}
		})
	}
}

// A build that fails after its bundle was made has a serviced access point
// that nobody will retire: the builder closes librespot's session itself,
// which is what ends the receive loop.
func TestFailedBuildClosesItsServicedAccessPoint(t *testing.T) {
	storeCreds(t)
	b := stubBuild(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	m := stubAccessPoints(t)
	b.initErr = errors.New("player init failed")

	p := New(nil, "client", 320)
	if err := p.ensureSession(); err == nil {
		t.Fatal("the build succeeded with a player that could not be made")
	}
	serviced := 0
	for _, e := range m.events() {
		if e == "service" {
			serviced++
		}
	}
	if serviced != 1 || b.disposed() != 1 {
		t.Errorf("%d access points serviced, %d librespot sessions closed; want 1 and 1", serviced, b.disposed())
	}
}

// closeByItself is librespot closing a bundle's access point after a
// reconnect failed for good: Done closes, and the bundle is still the
// session's (TestDDSonicContractFailedReconnectClosesAccessPoint).
func (k *playKit) closeByItself(b *playback) { close(k.doneOf(b)) }

// A track is not opened on a bundle whose access point has closed itself:
// the open fails at once, before librespot is asked anything, with an error
// that says the connection is gone, is not an auth error, and carries the
// bundle and build it is about, marked as made before any librespot work.
func TestOpenOnClosedAccessPointFailsAtOnce(t *testing.T) {
	k := newPlayKit(t)
	sess := goodSession()
	old := bundleOf(sess)
	k.closeByItself(old)

	_, _, err := sess.NewStream(context.Background(), testTrack, 320)
	if !errors.Is(err, ap.ErrAccesspointClosed) || isAuthError(err) || errors.Is(err, errSessionChanged) {
		t.Errorf("NewStream() = %v, want the closed access point, and no auth or session-changed error", err)
	}
	var oe *originError
	if !errors.As(err, &oe) || oe.origin != old || oe.gen != sess.generation() || !oe.beforeWork {
		t.Errorf("NewStream() = %#v, want an origin error on the session's bundle and build, before any work", err)
	}
	if k.opens.Load() != 0 {
		t.Errorf("librespot was asked for %d stream setups on a closed access point", k.opens.Load())
	}
}

// The provider recovers from a closed access point: the session's playback
// is rebuilt from its credential, silently and once, the closed bundle is
// torn down, and the track the user asked for is opened on the new one. No
// token is refreshed, no browser opened, and the sign-in is not refused.
func TestClosedAccessPointIsRebuiltAndTheTrackPlays(t *testing.T) {
	tests := []struct {
		name string
		// arrange makes the session's bundle fail as a closed access point
		// does, before the open or under it.
		arrange   func(k *playKit, old *playback)
		wantOpens int32
	}{
		{"closed before the open", func(k *playKit, old *playback) { k.closeByItself(old) }, 1},
		{"closes under the open", func(k *playKit, old *playback) {
			k.open = func(b *playback, _ context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
				if b == old {
					k.closeByItself(old)
					return nil, fmt.Errorf("failed retrieving audio key: %w", ap.ErrAccesspointClosed)
				}
				return &librespotPlayer.Stream{Source: &closableSource{}}, nil
			}
		}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeCreds(t)
			b := stubBuild(t)
			k := newPlayKit(t)
			h := fakeSpotifyHTTP(t, tokenOK, apiOK)
			sess := goodSession()
			p := New(sess, "client", 320)
			old, gen, token := bundleOf(sess), sess.generation(), tokenOf(sess)
			tt.arrange(k, old)

			s, _, _, err := p.NewStreamer(testTrackURI)
			if err != nil {
				t.Fatalf("NewStreamer() = %v, want the track on a rebuilt session", err)
			}
			_ = s.Close()
			if k.opens.Load() != tt.wantOpens || b.librespotSessions.Load() != 1 || sess.generation() != gen+1 {
				t.Errorf("%d stream setups, %d playback rebuilds, build %d -> %d; want %d, 1 and the next build",
					k.opens.Load(), b.librespotSessions.Load(), gen, sess.generation(), tt.wantOpens)
			}
			if bundleOf(sess) == old {
				t.Error("the session is still on the bundle whose access point closed")
			}
			tornDown(t, "the closed bundle", old)
			if k.count("player-close") != 1 {
				t.Errorf("the closed bundle's player and session were closed %d times, want once", k.count("player-close"))
			}
			if sess.signInRefused() || h.tokenCalls.Load() != 0 || tokenOf(sess) != token || b.browserFlows.Load() != 0 {
				t.Errorf("the rebuild touched the sign-in: refused %t, %d token requests, token %q (was %q), %d browser flows",
					sess.signInRefused(), h.tokenCalls.Load(), tokenOf(sess), token, b.browserFlows.Load())
			}
		})
	}
}

// One rebuild and one more try, and no more: when the rebuilt session's
// access point is closed as well, the play fails as a play does. Nothing
// loops, nothing is refused, and nobody is asked to sign in.
func TestClosedAccessPointRecoveryIsBounded(t *testing.T) {
	storeCreds(t)
	b := stubBuild(t)
	k := newPlayKit(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	k.open = func(b *playback, _ context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
		k.closeByItself(b)
		return nil, fmt.Errorf("failed retrieving audio key: %w", ap.ErrAccesspointClosed)
	}
	sess := goodSession()
	retireAtEnd(t, sess)
	p := New(sess, "client", 320)
	gen := sess.generation()

	_, _, _, err := p.NewStreamer(testTrackURI)
	if !errors.Is(err, ap.ErrAccesspointClosed) || errors.Is(err, playlist.ErrNeedsAuth) || sess.signInRefused() {
		t.Errorf("NewStreamer() = %v (refused %t), want the closed access point as a plain failure", err, sess.signInRefused())
	}
	if k.opens.Load() != 2 || b.librespotSessions.Load() != 1 || sess.generation() != gen+1 {
		t.Errorf("%d stream setups, %d rebuilds, build %d -> %d; want 2, 1 and the next build", k.opens.Load(), b.librespotSessions.Load(), gen, sess.generation())
	}
}

// What the rebuild's own failure means is the rebuild's to say: one that
// could not reach Spotify is an outage, and the play fails plainly; only a
// stored credential that can no longer make a session asks for sign-in. A
// closed access point is never, by itself, a refused sign-in.
func TestClosedAccessPointRebuildFailure(t *testing.T) {
	tests := []struct {
		name      string
		reconnErr error
		needsAuth bool
	}{
		{"Spotify cannot be reached", fmt.Errorf("spotify: stored auth: %w", &net.OpError{Op: "dial", Err: errors.New("network is unreachable")}), false},
		{"the rebuild timed out", fmt.Errorf("spotify: reconnect: %w", context.DeadlineExceeded), false},
		{"no stored credential", fmt.Errorf("spotify: reconnect: %w", errNoStoredCreds), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closed := func() error { return errAccessPointClosed }
			opens, reconnects := stubStreams(t, closed, tt.reconnErr)
			sess := goodSession()
			p := New(sess, "client", 320)

			_, _, _, err := p.NewStreamer(testTrackURI)
			if err == nil || errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth || sess.signInRefused() != tt.needsAuth {
				t.Errorf("NewStreamer() = %v (refused %t), want an error, needing sign-in %t", err, sess.signInRefused(), tt.needsAuth)
			}
			if opens.Load() != 1 || reconnects.Load() != 1 {
				t.Errorf("%d stream setups and %d rebuilds, want 1 and 1", opens.Load(), reconnects.Load())
			}
		})
	}
}

// A rebuilt session that Spotify then refuses to play on is an auth matter,
// as it always was; a rebuilt session that plays is the end of it.
func TestClosedAccessPointThenRefusal(t *testing.T) {
	var n atomic.Int32
	opens, reconnects := stubStreams(t, func() error {
		if n.Add(1) == 1 {
			return errAccessPointClosed
		}
		return fmt.Errorf("spotify: %w", &audio.KeyProviderError{Code: 1})
	}, nil)
	sess := goodSession()
	p := New(sess, "client", 320)

	_, _, _, err := p.NewStreamer(testTrackURI)
	if !errors.Is(err, playlist.ErrNeedsAuth) || opens.Load() != 2 || reconnects.Load() != 1 {
		t.Errorf("NewStreamer() = %v after %d setups and %d rebuilds; want a sign-in request after 2 and 1", err, opens.Load(), reconnects.Load())
	}
}

// retireAtEnd closes sess when the test ends, so that the workers of the
// setups it abandoned, which nothing else waits for, have returned (a
// retired bundle's tail waits for them) before the stand-ins are put back.
func retireAtEnd(t *testing.T, sess *Session) {
	t.Helper()
	t.Cleanup(func() { sess.Close(); quiesce(t) })
}

// tornDown waits for a bundle's tail to have finished: its access point
// closed, its workers returned, its player and session closed.
func tornDown(t *testing.T, what string, b *playback) {
	t.Helper()
	select {
	case <-b.gone:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was not torn down", what)
	}
}

// The key dispositions librespot gives, as a stream setup returns them.
var (
	errKeyReplaced = fmt.Errorf("failed retrieving audio key: %w", &audio.ConnectionReplacedError{Sent: 1, Now: 2})
	errKeyRefused  = fmt.Errorf("failed retrieving audio key: %w", &audio.KeyProviderError{Code: 1})
)

func errKeySend(gen uint64) error {
	return fmt.Errorf("failed retrieving audio key: %w", &audio.KeySendError{Gen: gen, Err: errors.New("write: broken pipe")})
}

// openLog records the bundle of every stream setup, in order.
type openLog struct {
	mu sync.Mutex
	on []*playback
}

func (l *openLog) add(b *playback) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = append(l.on, b)
	return len(l.on)
}

func (l *openLog) bundles() []*playback {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]*playback(nil), l.on...)
}

// opensFailing makes the nth stream setup fail with fails[n-1], or succeed
// past the end of fails, and logs which bundle each was on. before, when
// set, runs at the start of each setup with its number.
func opensFailing(k *playKit, before func(n int, b *playback), fails ...error) *openLog {
	l := &openLog{}
	k.open = func(b *playback, _ context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
		n := l.add(b)
		if before != nil {
			before(n, b)
		}
		if n <= len(fails) && fails[n-1] != nil {
			return nil, fails[n-1]
		}
		return &librespotPlayer.Stream{Source: &closableSource{}}, nil
	}
	return l
}

// atDecision runs f, once, when the recovery reaches the named point; the
// recovery goes on when f returns.
func atDecision(t *testing.T, point string, f func()) {
	t.Helper()
	var once sync.Once
	orig := decisionHook
	decisionHook = func(p string) {
		if p == point {
			once.Do(f)
		}
	}
	t.Cleanup(func() { quiesce(t); decisionHook = orig })
}

// signalAt closes the returned channel when the recovery first reaches the
// named point.
func signalAt(t *testing.T, point string) <-chan struct{} {
	t.Helper()
	reached := make(chan struct{})
	atDecision(t, point, func() { close(reached) })
	return reached
}

// streamerAsync runs NewStreamer on its own goroutine. The test's end waits
// for that play, bounded, before anything registered earlier (the stand-ins
// it reads) is put back, whether the test passed or failed.
func streamerAsync(t *testing.T, p *SpotifyProvider) <-chan error {
	t.Helper()
	out, ended := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(ended)
		s, _, _, err := p.NewStreamer(testTrackURI)
		if s != nil {
			_ = s.Close()
		}
		out <- err
	}()
	t.Cleanup(func() {
		select {
		case <-ended:
		case <-time.After(45 * time.Second):
			t.Error("the play did not end")
		}
	})
	return out
}

// recoveryKit is a provider on a good session with Spotify stood in for.
type recoveryKit struct {
	b    *build
	k    *playKit
	sess *Session
	p    *SpotifyProvider
	gen  uint64
	old  *playback
}

func newRecoveryKit(t *testing.T) *recoveryKit {
	t.Helper()
	storeCreds(t)
	r := &recoveryKit{b: stubBuild(t), k: newPlayKit(t)}
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	r.sess = goodSession()
	retireAtEnd(t, r.sess)
	r.p = New(r.sess, "client", 320)
	r.gen, r.old = r.sess.generation(), bundleOf(r.sess)
	return r
}

// A key request whose connection was replaced is opened once more on the
// same session, which is healthy: no rebuild, no new build, and the bundle
// it ran on is not retired (owner decision 2; I2).
func TestReplacedConnectionReopensOnTheSameSession(t *testing.T) {
	r := newRecoveryKit(t)
	log := opensFailing(r.k, nil, errKeyReplaced)

	s, _, _, err := r.p.NewStreamer(testTrackURI)
	if err != nil {
		t.Fatalf("NewStreamer() = %v, want the track reopened", err)
	}
	_ = s.Close()
	on := log.bundles()
	if len(on) != 2 || on[0] != r.old || on[1] != r.old {
		t.Errorf("the setups ran on %v, want two on the session's bundle", on)
	}
	if r.b.librespotSessions.Load() != 0 || r.sess.generation() != r.gen || bundleOf(r.sess) != r.old {
		t.Errorf("a healthy session was rebuilt: %d rebuilds, build %d -> %d", r.b.librespotSessions.Load(), r.gen, r.sess.generation())
	}
	if r.old.ctx.Err() != nil || r.k.count("ap-close") != 0 {
		t.Error("the healthy bundle was retired")
	}
}

// A key request whose write failed is opened once more only on positive
// evidence about the access point it ran on: a newer connection (opened on
// the same bundle), its close (rebuilt, then opened), the session's owner
// changing (opened on the new one, not rebuilt), or the session's close
// (final). With none of them by the recovery's deadline, the play fails
// (I6b): a write error on an unchanged connection is not retried.
func TestSendFailureNeedsPositiveEvidence(t *testing.T) {
	tests := []struct {
		name string
		// evidence happens while the recovery waits; nil gives none.
		evidence     func(t *testing.T, r *recoveryKit)
		wantErr      func(error) bool
		wantRebuilds int32
		wantOnOld    bool // the second setup ran on the bundle the first did
	}{
		{"a newer connection", func(_ *testing.T, r *recoveryKit) { r.k.advance(r.old) }, nil, 0, true},
		{"the access point closes", func(_ *testing.T, r *recoveryKit) { r.k.closeByItself(r.old) }, nil, 1, false},
		{"the owner changes", func(t *testing.T, r *recoveryKit) {
			if err := r.sess.Reconnect(context.Background()); err != nil {
				t.Error(err)
			}
		}, nil, 1, false},
		{"the session closes", func(_ *testing.T, r *recoveryKit) { r.sess.Close() },
			func(err error) bool { return errors.Is(err, errSessionClosed) }, 0, false},
		{"nothing by the deadline", nil,
			func(err error) bool { return errors.Is(err, context.DeadlineExceeded) }, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRecoveryKit(t)
			log := opensFailing(r.k, nil, errKeySend(1))
			waiting := signalAt(t, "before-wait")
			if tt.evidence == nil {
				orig := recoveryTimeout
				recoveryTimeout = 100 * time.Millisecond
				t.Cleanup(func() { recoveryTimeout = orig })
			}
			result := streamerAsync(t, r.p)
			within(t, "the recovery waiting", waiting)
			if tt.evidence != nil {
				tt.evidence(t, r)
			}
			err := within(t, "NewStreamer", result)
			on := log.bundles()
			switch {
			case tt.wantErr != nil:
				if !tt.wantErr(err) || len(on) != 1 {
					t.Errorf("NewStreamer() = %v after %d setups; want the final error after 1", err, len(on))
				}
			case err != nil:
				t.Errorf("NewStreamer() = %v, want the track reopened", err)
			case len(on) != 2 || (on[1] == r.old) != tt.wantOnOld:
				t.Errorf("%d setups, the second on the first's bundle: %t; want 2, %t", len(on), len(on) == 2 && on[1] == r.old, tt.wantOnOld)
			}
			if r.b.librespotSessions.Load() != tt.wantRebuilds {
				t.Errorf("%d playback builds, want %d", r.b.librespotSessions.Load(), tt.wantRebuilds)
			}
		})
	}
}

// The step's context comes first in every decision (B3; I16): when it has
// ended and the owner changed while the recovery waited, the play ends with
// the context's error, and nothing is opened again.
func TestRecoveryDeadlineComesFirst(t *testing.T) {
	r := newRecoveryKit(t)
	log := opensFailing(r.k, nil, errKeySend(1))
	orig := recoveryTimeout
	recoveryTimeout = 50 * time.Millisecond
	t.Cleanup(func() { recoveryTimeout = orig })
	atDecision(t, "before-wait", func() {
		time.Sleep(100 * time.Millisecond) // the step's deadline has passed
		if err := r.sess.Reconnect(context.Background()); err != nil {
			t.Error(err)
		}
	})

	_, _, _, err := r.p.NewStreamer(testTrackURI)
	if !errors.Is(err, context.DeadlineExceeded) || len(log.bundles()) != 1 {
		t.Errorf("NewStreamer() = %v after %d setups; want the deadline after 1", err, len(log.bundles()))
	}
}

// An error accepted from the bundle the session was on, whose session
// changed owner or closed before the recovery decided (B5; I19b): the track
// is opened on the session's current bundle, which is not rebuilt, or the
// play ends because the session is closed.
func TestOwnerChangedBeforeTheDecision(t *testing.T) {
	t.Run("owner changed", func(t *testing.T) {
		r := newRecoveryKit(t)
		log := opensFailing(r.k, nil, errKeyReplaced)
		atDecision(t, "begin", func() {
			if err := r.sess.Reconnect(context.Background()); err != nil {
				t.Error(err)
			}
		})
		s, _, _, err := r.p.NewStreamer(testTrackURI)
		if err != nil {
			t.Fatalf("NewStreamer() = %v, want the track on the current bundle", err)
		}
		_ = s.Close()
		on := log.bundles()
		if len(on) != 2 || on[1] != bundleOf(r.sess) || on[1] == r.old || r.b.librespotSessions.Load() != 1 {
			t.Errorf("%d setups, the second on the current bundle: %t, %d builds; want 2, true, 1 (the test's own)",
				len(on), len(on) == 2 && on[1] == bundleOf(r.sess), r.b.librespotSessions.Load())
		}
	})
	t.Run("session closed", func(t *testing.T) {
		r := newRecoveryKit(t)
		log := opensFailing(r.k, nil, errKeyReplaced)
		atDecision(t, "begin", func() { r.sess.Close() })
		_, _, _, err := r.p.NewStreamer(testTrackURI)
		if !errors.Is(err, errSessionClosed) || len(log.bundles()) != 1 {
			t.Errorf("NewStreamer() = %v after %d setups; want the session's close after 1", err, len(log.bundles()))
		}
	})
}

// The access point the request ran on closes after a replacement was
// reported (B8; I23) or after the decision to reopen on it, before the
// reopen took it (B9; I24): the one recovery takes the terminal form, a
// rebuild of that build and one open on the new bundle. Either way two
// setups start librespot work, and one rebuild is made.
func TestOriginClosedTakesTheTerminalForm(t *testing.T) {
	for _, point := range []string{"begin", "decided"} {
		t.Run(point, func(t *testing.T) {
			r := newRecoveryKit(t)
			log := opensFailing(r.k, nil, errKeyReplaced)
			atDecision(t, point, func() { r.k.closeByItself(r.old) })
			var terminalAtAcquisition atomic.Bool
			orig := decisionHook
			decisionHook = func(p string) {
				if p == "terminal-at-acquisition" {
					terminalAtAcquisition.Store(true)
				}
				orig(p)
			}

			s, _, _, err := r.p.NewStreamer(testTrackURI)
			if err != nil {
				t.Fatalf("NewStreamer() = %v, want the track on a rebuilt bundle", err)
			}
			_ = s.Close()
			on := log.bundles()
			if len(on) != 2 || on[1] == r.old || r.b.librespotSessions.Load() != 1 || r.sess.generation() != r.gen+1 {
				t.Errorf("%d setups, %d rebuilds, build %d -> %d; want 2, 1 and the next build",
					len(on), r.b.librespotSessions.Load(), r.gen, r.sess.generation())
			}
			if terminalAtAcquisition.Load() != (point == "decided") {
				t.Errorf("the terminal form was taken at the acquisition: %t, want %t", terminalAtAcquisition.Load(), point == "decided")
			}
			tornDown(t, "the closed bundle", r.old)
		})
	}
}

// The recovery acts on the build the error came from, not on one read
// before the open (B6; I20, I21): a build published after the play began
// and before its open took a bundle is the one rebuilt when that bundle's
// access point closes or Spotify refuses its key.
func TestRecoveryRebuildsTheErrorsOwnBuild(t *testing.T) {
	for _, first := range []struct {
		name string
		err  error
	}{{"closed", fmt.Errorf("failed retrieving audio key: %w", ap.ErrAccesspointClosed)}, {"refused", errKeyRefused}} {
		t.Run(first.name, func(t *testing.T) {
			r := newRecoveryKit(t)
			opensFailing(r.k, nil, first.err)
			var rebuilt []uint64
			var mu sync.Mutex
			origReconn, origOpen := sessionReconnect, sessionNewStream
			sessionReconnect = func(s *Session, ctx context.Context, gen uint64) error {
				mu.Lock()
				rebuilt = append(rebuilt, gen)
				mu.Unlock()
				return origReconn(s, ctx, gen)
			}
			var opened atomic.Int32
			sessionNewStream = func(s *Session, ctx context.Context, id librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
				if opened.Add(1) == 1 { // a build is published as the play begins
					if err := s.Reconnect(context.Background()); err != nil {
						t.Error(err)
					}
				}
				return origOpen(s, ctx, id, bitrate)
			}
			t.Cleanup(func() { quiesce(t); sessionReconnect, sessionNewStream = origReconn, origOpen })

			s, _, _, err := r.p.NewStreamer(testTrackURI)
			if err != nil {
				t.Fatalf("NewStreamer() = %v, want the track on a rebuilt session", err)
			}
			_ = s.Close()
			mu.Lock()
			defer mu.Unlock()
			if len(rebuilt) != 1 || rebuilt[0] != r.gen+1 || r.sess.generation() != r.gen+2 || r.b.librespotSessions.Load() != 2 {
				t.Errorf("rebuilds of builds %v, build %d -> %d, %d builds; want one of build %d, then build %d",
					rebuilt, r.gen, r.sess.generation(), r.b.librespotSessions.Load(), r.gen+1, r.gen+2)
			}
		})
	}
}

// A second refusal asks for sign-in for the build that refused, never for
// one published after it (B7; I22): a newer sign-in is not refused for an
// older build's error.
func TestSecondRefusalIsAboutItsOwnBuild(t *testing.T) {
	r := newRecoveryKit(t)
	opensFailing(r.k, nil, errKeyReplaced, errKeyRefused)
	orig := sessionNewStream
	var opened atomic.Int32
	sessionNewStream = func(s *Session, ctx context.Context, id librespot.SpotifyId, bitrate int) (*librespotPlayer.Stream, context.CancelFunc, error) {
		stream, cancel, err := orig(s, ctx, id, bitrate)
		if opened.Add(1) == 2 && err != nil { // accepted, and then a newer build
			if rerr := s.Reconnect(context.Background()); rerr != nil {
				t.Error(rerr)
			}
		}
		return stream, cancel, err
	}
	t.Cleanup(func() { quiesce(t); sessionNewStream = orig })

	_, _, _, err := r.p.NewStreamer(testTrackURI)
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("NewStreamer() = %v, want a sign-in request", err)
	}
	if r.sess.signInRefused() || r.sess.generation() != r.gen+1 {
		t.Errorf("the newer build was refused for the older one's error: refused %t, build %d -> %d", r.sess.signInRefused(), r.gen, r.sess.generation())
	}
}

// One recovery per play, whatever its first failure and whatever its second
// (I8; I27′): at most two setups and one rebuild, every second failure
// final, except a refusal, which asks for sign-in for its own build (no
// browser).
func TestOneRecoveryInEveryOrder(t *testing.T) {
	closedErr := fmt.Errorf("failed retrieving audio key: %w", ap.ErrAccesspointClosed)
	firsts := []struct {
		name     string
		err      error
		rebuilds int32
	}{
		{"replaced", errKeyReplaced, 0},
		{"send failed, then a newer connection", errKeySend(1), 0},
		{"closed", closedErr, 1},
		{"refused", errKeyRefused, 1},
	}
	seconds := []struct {
		name      string
		err       error
		needsAuth bool
	}{
		{"replaced", errKeyReplaced, false},
		{"send failed", errKeySend(1), false},
		{"closed", closedErr, false},
		{"refused", errKeyRefused, true},
		{"anything else", errors.New("spotify: metadata: 404"), false},
	}
	for _, f := range firsts {
		for _, sec := range seconds {
			t.Run(f.name+" then "+sec.name, func(t *testing.T) {
				r := newRecoveryKit(t)
				before := func(n int, b *playback) {
					switch {
					case n == 1 && f.name == "send failed, then a newer connection":
						r.k.advance(b) // already newer when the recovery looks
					case sec.name == "closed" && n == 2:
						r.k.closeByItself(b)
					case f.name == "closed" && n == 1:
						r.k.closeByItself(b)
					}
				}
				log := opensFailing(r.k, before, f.err, sec.err)
				_, _, _, err := r.p.NewStreamer(testTrackURI)
				if err == nil || errors.Is(err, playlist.ErrNeedsAuth) != sec.needsAuth {
					t.Errorf("NewStreamer() = %v, want the second failure, needing sign-in %t", err, sec.needsAuth)
				}
				if n := len(log.bundles()); n != 2 || r.b.librespotSessions.Load() != f.rebuilds || r.b.browserFlows.Load() != 0 {
					t.Errorf("%d setups, %d rebuilds, %d browser flows; want 2, %d, 0", n, r.b.librespotSessions.Load(), r.b.browserFlows.Load(), f.rebuilds)
				}
				if r.sess.signInRefused() != sec.needsAuth {
					t.Errorf("refused %t, want %t", r.sess.signInRefused(), sec.needsAuth)
				}
			})
		}
	}
}

// What has no recovery ends the play at once: a setup cut short, the
// provider's own window, sequence numbers run out, a second key provider on
// the access point, or any other failure. Nothing is rebuilt or refused.
func TestFinalFailuresAreNotRecovered(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"cancelled", fmt.Errorf("failed retrieving audio key: %w", context.Canceled)},
		{"the key window ended", fmt.Errorf("failed retrieving audio key: %w", context.DeadlineExceeded)},
		{"sequence numbers exhausted", fmt.Errorf("failed retrieving audio key: %w", audio.ErrKeySequenceExhausted)},
		{"not the access point's key provider", fmt.Errorf("failed retrieving audio key: %w", audio.ErrKeyProviderNotOwner)},
		{"anything else", errors.New("spotify: metadata: 500")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRecoveryKit(t)
			log := opensFailing(r.k, nil, tt.err)
			_, _, _, err := r.p.NewStreamer(testTrackURI)
			if !errors.Is(err, tt.err) || errors.Is(err, playlist.ErrNeedsAuth) {
				t.Errorf("NewStreamer() = %v, want %v as it is", err, tt.err)
			}
			if len(log.bundles()) != 1 || r.b.librespotSessions.Load() != 0 || r.sess.signInRefused() {
				t.Errorf("%d setups, %d rebuilds, refused %t; want 1, 0, false", len(log.bundles()), r.b.librespotSessions.Load(), r.sess.signInRefused())
			}
		})
	}
}

// holdDetach holds a publication or a close (point) where it has detached
// the old bundle and not yet retired it.
func holdDetach(t *testing.T, point string) (reached <-chan struct{}, release func()) {
	t.Helper()
	r, rel := make(chan struct{}), make(chan struct{})
	var once sync.Once
	orig := detachHook
	detachHook = func(p string) {
		if p == point {
			once.Do(func() { close(r); <-rel })
		}
	}
	release = sync.OnceFunc(func() { close(rel) })
	t.Cleanup(func() { release(); quiesce(t); detachHook = orig })
	return r, release
}

// A recovery waiting on a failed write is woken by the session's owner
// changing as soon as the owner changes, in the gap before the old bundle is
// retired (B1, B2; I13, I14, I15): a publication opens the track on the new
// bundle, a close ends the play as closed, both while the old bundle is
// still not retired. The owner channel, closed under the lock at the swap,
// is what wakes it there: nothing else about the old bundle has changed.
func TestOwnerChangeWakesTheWaitBeforeRetirement(t *testing.T) {
	for _, point := range []string{"publish", "close"} {
		t.Run(point, func(t *testing.T) {
			r := newRecoveryKit(t)
			log := opensFailing(r.k, nil, errKeySend(1))
			waiting := signalAt(t, "before-wait")
			result := streamerAsync(t, r.p)
			// After the play: its release runs first at the test's end.
			detached, release := holdDetach(t, point)
			within(t, "the recovery waiting", waiting)
			changed, changeEnded := make(chan error, 1), make(chan struct{})
			go func() {
				defer close(changeEnded)
				if point == "publish" {
					changed <- r.sess.Reconnect(context.Background())
					return
				}
				r.sess.Close()
				changed <- nil
			}()
			// At the test's end, first: the owner change is let go and has
			// finished before anything waits for the bundle tails it starts.
			t.Cleanup(func() {
				release()
				select {
				case <-changeEnded:
				case <-time.After(45 * time.Second):
					t.Error("the owner change did not end")
				}
			})
			within(t, "the owner change held before retirement", detached)

			err := within(t, "NewStreamer, with the old bundle not yet retired", result)
			if r.old.ctx.Err() != nil {
				t.Fatal("the old bundle was retired: the test shows nothing about the gap")
			}
			switch point {
			case "publish":
				on := log.bundles()
				if err != nil || len(on) != 2 || on[1] == r.old {
					t.Errorf("NewStreamer() = %v after %d setups; want the track on the new bundle after 2", err, len(on))
				}
			case "close":
				if !errors.Is(err, errSessionClosed) || len(log.bundles()) != 1 {
					t.Errorf("NewStreamer() = %v after %d setups; want the session's close after 1", err, len(log.bundles()))
				}
			}
			release()
			if err := within(t, "the owner change", changed); err != nil {
				t.Error(err)
			}
		})
	}
}

// stacksWith counts the goroutines whose stacks hold every one of parts.
func stacksWith(parts ...string) int {
	buf := make([]byte, 8<<20)
	n := 0
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		all := true
		for _, part := range parts {
			all = all && strings.Contains(g, part)
		}
		if all {
			n++
		}
	}
	return n
}

// lockWaitLive is how long a deadline that ends while its open waits for the
// session's lock stays live: long enough to see the open blocked first.
const lockWaitLive = 2 * time.Second

// endWhileWaitingForLock holds s's lock, starts an open with ctx and sees
// that open, and only it, blocked on the lock while ctx is still live. Then
// end ends ctx (or its deadline passes), the open is seen still blocked with
// ctx ended, and only then is the lock released. Anything else fails the
// test, with the lock released and the open's wait bounded.
func endWhileWaitingForLock(t *testing.T, s *Session, ctx context.Context, end func()) error {
	t.Helper()
	blockedOpen := []string{"(*Session).holdPlayback(", "sync.(*Mutex).Lock(", "created by github.com/bjarneo/cliamp/external/spotify.openAsync"}
	if n := stacksWith(blockedOpen...); n != 0 {
		t.Fatalf("%d opens were already blocked on a session's lock", n)
	}
	var unlock sync.Once
	s.mu.Lock()
	defer unlock.Do(s.mu.Unlock)
	out := openAsync(s, ctx)
	blocked := func() bool { return len(out) == 0 && stacksWith(blockedOpen...) == 1 }
	deadline := time.Now().Add(5 * time.Second)
	for !blocked() {
		if len(out) != 0 || time.Now().After(deadline) {
			t.Fatal("the open was not seen blocked on the session's lock")
		}
		time.Sleep(time.Millisecond)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("the context had ended (%v) before the open was seen blocked: that is the already-ended case", err)
	}
	end()
	select {
	case <-ctx.Done():
	case <-time.After(lockWaitLive + 5*time.Second):
		t.Fatal("the context did not end")
	}
	if !blocked() {
		t.Fatal("the open was not still blocked on the session's lock when its context ended")
	}
	unlock.Do(s.mu.Unlock)
	return within(t, "the open", out).err
}

// An open whose context has ended, before the call or while it waited for
// the session's lock, ends with its context's error, even on a bundle whose
// access point has closed: no origin error, no librespot work, and nothing
// the provider's recovery acts on (R21-1). The context is looked at under
// the lock, before the closed access point is: an open that was blocked on
// the lock with a live context, which ended before the lock was released,
// gets its context's error too (R22-1).
func TestEndedOpenOnClosedAccessPointIsTheContexts(t *testing.T) {
	for _, tt := range []struct {
		name string
		want error
	}{
		{"cancelled before the call", context.Canceled},
		{"deadline ended before the call", context.DeadlineExceeded},
		{"cancelled while waiting for the lock", context.Canceled},
		{"deadline ended while waiting for the lock", context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			k := newPlayKit(t)
			sess := goodSession()
			k.closeByItself(bundleOf(sess))
			var err error
			switch tt.name {
			case "cancelled before the call":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				_, _, err = sess.NewStream(ctx, testTrack, 320)
			case "deadline ended before the call":
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				_, _, err = sess.NewStream(ctx, testTrack, 320)
			case "cancelled while waiting for the lock":
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				err = endWhileWaitingForLock(t, sess, ctx, cancel)
			default:
				ctx, cancel := context.WithTimeout(context.Background(), lockWaitLive)
				defer cancel()
				err = endWhileWaitingForLock(t, sess, ctx, func() {})
			}
			var oe *originError
			if !errors.Is(err, tt.want) || errors.Is(err, ap.ErrAccesspointClosed) || errors.As(err, &oe) {
				t.Errorf("NewStream() = %v, want the context's %v and no closed-access-point origin error", err, tt.want)
			}
			if k.opens.Load() != 0 {
				t.Errorf("librespot was asked for %d stream setups", k.opens.Load())
			}

			// The provider's recovery does nothing with it: no rebuild, no open.
			var rebuilds atomic.Int32
			orig := sessionReconnect
			sessionReconnect = func(*Session, context.Context, uint64) error { rebuilds.Add(1); return nil }
			t.Cleanup(func() { sessionReconnect = orig })
			opened := false
			_, rerr := recoverStream(sess, err, func() (*spotifyStreamer, error) { opened = true; return nil, errors.New("opened") })
			if !errors.Is(rerr, tt.want) || opened || rebuilds.Load() != 0 {
				t.Errorf("recovery = %v, opened %t, %d rebuilds; want the context's error, nothing opened or rebuilt", rerr, opened, rebuilds.Load())
			}
		})
	}
}
