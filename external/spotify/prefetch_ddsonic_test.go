package spotify

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// promptly is how long a call the speaker or the UI makes may take while a
// decoder read is held. Nothing in it waits for the network.
const promptly = time.Second

// pcmSource is a decoder whose every sample says where it is: pair i of the
// track is i+1 on both channels, so 0 is silence. A test can hold its next
// read, fail the held read, fail reads from a pair on, fail a seek, or end
// the track.
type pcmSource struct {
	mu          sync.Mutex
	pos         int64 // the next pair
	total       int64 // pairs in the track; 0 is endless
	failAt      int64 // a read starting at or past this pair fails; 0 is never
	failErr     error
	failHeld    error // what the held read returns, if anything
	seekErr     error // every seek is refused with it
	refusedAt   int64 // the position at the last refused seek
	seekHeld    chan struct{}
	seekRelease chan struct{}
	seekHeldErr error // what the held seek returns
	releaseSeek func()
	seeks       []int64
	reads       int
	held        chan struct{}
	heldRelease chan struct{}
	release     func()
	cancelled   bool
}

// hold holds the next read until release, or until the stream is cancelled.
func (s *pcmSource) hold() (held <-chan struct{}, release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, r := make(chan struct{}), make(chan struct{})
	s.held, s.heldRelease = h, r
	s.release = sync.OnceFunc(func() { close(r) })
	return h, s.release
}

// holdSeek holds the next seek until release; it then returns err.
func (s *pcmSource) holdSeek(err error) (held <-chan struct{}, release func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, r := make(chan struct{}), make(chan struct{})
	s.seekHeld, s.seekRelease, s.seekHeldErr = h, r, err
	s.releaseSeek = sync.OnceFunc(func() { close(r) })
	return h, s.releaseSeek
}

// cancel is the stream's cancel: it ends a held read or seek, as cancelling
// the stream's HTTP context ends a waiting chunk request.
func (s *pcmSource) cancel() {
	s.mu.Lock()
	s.cancelled = true
	release, releaseSeek := s.release, s.releaseSeek
	s.mu.Unlock()
	if release != nil {
		release()
	}
	if releaseSeek != nil {
		releaseSeek()
	}
}

func (s *pcmSource) Read(p []float32) (int, error) {
	s.mu.Lock()
	s.reads++
	held, released := s.held, s.heldRelease
	s.held = nil
	s.mu.Unlock()
	if held != nil {
		close(held)
		<-released
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelled {
		return 0, errors.New("pcmSource: cancelled")
	}
	if held != nil && s.failHeld != nil {
		return 0, s.failHeld
	}
	if s.failAt > 0 && s.pos >= s.failAt {
		return 0, s.failErr
	}
	n := int64(len(p) / 2)
	if s.total > 0 {
		n = min(n, s.total-s.pos)
	}
	if n <= 0 {
		return 0, io.EOF
	}
	for i := range n {
		v := float32(s.pos + i + 1)
		p[2*i], p[2*i+1] = v, v
	}
	s.pos += n
	if s.total > 0 && s.pos == s.total && n < int64(len(p)/2) {
		return int(2 * n), io.EOF
	}
	return int(2 * n), nil
}

func (s *pcmSource) SetPositionMs(ms int64) error {
	s.mu.Lock()
	s.seeks = append(s.seeks, ms)
	held, released := s.seekHeld, s.seekRelease
	s.seekHeld = nil
	err := s.seekErr
	if held != nil {
		err = s.seekHeldErr
	}
	s.mu.Unlock()
	if held != nil {
		close(held)
		<-released
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.refusedAt = s.pos // refused: the decoder stays where it was
		return err
	}
	s.pos = ms * spotifySampleRate / 1000
	return nil
}

func (s *pcmSource) PositionMs() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos * 1000 / spotifySampleRate
}

func (s *pcmSource) state() (pos int64, reads int, seeks []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos, s.reads, append([]int64(nil), s.seeks...)
}

// playbackOf is the stream the player gets for a track decoded by src.
func playbackOf(t *testing.T, src *pcmSource) beep.StreamSeekCloser {
	t.Helper()
	s := newPlaybackStreamer(newSpotifyStreamer(&librespotPlayer.Stream{Source: src}, src.cancel))
	t.Cleanup(func() {
		src.cancel()
		_ = s.Close()
	})
	return s
}

// pull is one pull of the speaker: Beep calls Stream holding its global
// mutex (beep speaker.go, sampleReader.stream), the mutex TogglePause, Seek,
// Stop and the position the UI shows take.
func pull(s beep.Streamer, n int) ([][2]float64, int, bool) {
	buf := make([][2]float64, n)
	speaker.Lock()
	defer speaker.Unlock()
	got, ok := s.Stream(buf)
	return buf, got, ok
}

// returnsIn reports whether f returned within d.
func returnsIn(d time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

func awaitClosed(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never happened", what)
	}
}

// pumpUntil pulls, as the speaker does, until c is closed: it makes room for
// a prefetch, and is the read itself where nothing prefetches. The channel it
// returns is closed when it has stopped.
func pumpUntil(s beep.Streamer, c <-chan struct{}) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-c:
				return
			default:
			}
			pull(s, 512)
			time.Sleep(time.Millisecond)
		}
	}()
	return stopped
}

// fade is how many samples a stream fades in over after silence.
const fade = 64

// audible pulls until a pull carries audio, and returns its left channel
// past the fade-in, where the source's values are untouched.
func audible(t *testing.T, s beep.Streamer) []float64 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		buf, n, ok := pull(s, 512)
		if !ok {
			t.Fatalf("the stream ended while waiting for audio (err %v)", errOf(s))
		}
		if n == len(buf) && buf[n-1][0] != 0 {
			out := make([]float64, 0, n-fade)
			for _, v := range buf[fade:n] {
				out = append(out, v[0])
			}
			return out
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no audio within 5s")
	return nil
}

func errOf(s beep.Streamer) error {
	if e, ok := s.(interface{ Err() error }); ok {
		return e.Err()
	}
	return nil
}

// consecutive reports whether v counts up by one from its first value.
func consecutive(v []float64) bool {
	for i := 1; i < len(v); i++ {
		if v[i] != v[i-1]+1 {
			return false
		}
	}
	return true
}

// drain pulls to the end of the stream and returns the last audio value.
func drain(t *testing.T, s beep.Streamer) (last float64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		buf, n, ok := pull(s, 512)
		for _, v := range buf[:n] {
			if v[0] != 0 {
				last = v[0]
			}
		}
		if !ok || n < len(buf) {
			return last
		}
	}
	t.Fatal("the stream did not end within 10s")
	return last
}

// togglePause is Player.TogglePause's work on the speaker's side
// (player/player.go:476-486), on the player's beep.Ctrl: the pause is
// flipped under the speaker's lock and then published. (Suspending the
// device, which it also does, needs a device.)
func togglePause(ctrl *beep.Ctrl, published *atomic.Bool) {
	speaker.Lock()
	ctrl.Paused = !ctrl.Paused
	paused := ctrl.Paused
	speaker.Unlock()
	published.Store(paused)
}

// tickOf is what a UI tick asks the player of its decoder: the position and
// the length under the speaker's lock (Player.PositionAndDuration,
// player/player.go:841-866), then the stream's error (Player.StreamErr,
// :1064-1075, under the player's own lock only).
func tickOf(s beep.StreamSeekCloser) {
	speaker.Lock()
	_, _ = s.Position(), s.Len()
	speaker.Unlock()
	_ = s.(interface{ Err() error }).Err()
}

// A decoder read held on the network does not hold the speaker: the
// speaker's pull through the player's beep.Ctrl returns, and a key press
// (togglePause) and a tick (tickOf) return at once, whichever comes first.
// The audio plays once the read returns and the pause is lifted. This is the
// player's locking and Ctrl, not the player itself: a Player needs an audio
// device.
func TestPlaybackDoesNotWaitForAHeldRead(t *testing.T) {
	for _, keyFirst := range []bool{true, false} {
		name := "tick first"
		if keyFirst {
			name = "key first"
		}
		t.Run(name, func(t *testing.T) {
			src := &pcmSource{}
			held, release := src.hold()
			s := playbackOf(t, src)
			ctrl := &beep.Ctrl{Streamer: s}
			pulled := make(chan struct{})
			go func() {
				defer close(pulled)
				pull(ctrl, 512)
			}()
			awaitClosed(t, held, "the decoder's read")

			var paused atomic.Bool
			key := func() { togglePause(ctrl, &paused) }
			tick := func() { tickOf(s) }
			steps, names := []func(){tick, key}, []string{"the tick", "the key press"}
			if keyFirst {
				steps, names = []func(){key, tick}, []string{"the key press", "the tick"}
			}
			for i, step := range steps {
				if !returnsIn(promptly, step) {
					t.Fatalf("%s waited for the held read", names[i])
				}
			}
			if !paused.Load() {
				t.Error("the pause was not published")
			}
			select {
			case <-pulled:
			case <-time.After(promptly):
				t.Fatal("the speaker's pull waited for the held read")
			}

			release()
			togglePause(ctrl, &paused) // resume
			if paused.Load() {
				t.Fatal("the resume was not published")
			}
			if v := audible(t, ctrl); v[0] != fade+1 || !consecutive(v) {
				t.Errorf("after the read returned the audio starts at %v, want the track from its start (%d)", v[0], fade+1)
			}
		})
	}
}

// Paused through the player's beep.Ctrl, the speaker's pulls get silence and
// take nothing from the stream: what is decoded meanwhile is queued, the
// position stands, and the resume plays the next sample.
func TestPlaybackPausedConsumesNothing(t *testing.T) {
	src := &pcmSource{}
	s := playbackOf(t, src)
	ctrl := &beep.Ctrl{Streamer: s}
	v := audible(t, ctrl)
	last := v[len(v)-1]
	at := s.Position()

	var paused atomic.Bool
	togglePause(ctrl, &paused)
	stop := make(chan struct{})
	pumping := pumpUntil(ctrl, stop) // the speaker goes on pulling while paused
	time.Sleep(200 * time.Millisecond)
	_, r1, _ := src.state()
	time.Sleep(200 * time.Millisecond)
	_, r2, _ := src.state()
	close(stop)
	awaitClosed(t, pumping, "the paused pulls' end")
	if r1 != r2 {
		t.Errorf("the decoder was read %d more times while paused with a full queue", r2-r1)
	}
	if got := s.Position(); got != at {
		t.Errorf("position moved from %d to %d while paused", at, got)
	}
	if buf, n, ok := pull(ctrl, 512); !ok || n != len(buf) || buf[0][0] != 0 || buf[n-1][0] != 0 {
		t.Errorf("a pull while paused = %d, %v, %v..%v; want silence", n, ok, buf[0][0], buf[n-1][0])
	}

	togglePause(ctrl, &paused)
	buf, n, ok := pull(ctrl, 512)
	if !ok || n != len(buf) || buf[0][0] != last+1 {
		t.Errorf("resume = %d, %v, first sample %v; want %v, the next one", n, ok, buf[0][0], last+1)
	}
}

// A seek made while a read is held returns at once and wins over the queued
// audio and over the held read: the position is the target at once, the
// speaker gets silence, never an end, until the source is there, and then
// the audio of the latest target, none of the old.
func TestPlaybackSeekDropsQueuedAndHeldAudio(t *testing.T) {
	src := &pcmSource{}
	s := playbackOf(t, src)
	audible(t, s)
	held, release := src.hold()
	pumpUntil(s, held)
	awaitClosed(t, held, "the decoder's next read")

	for _, target := range []int{spotifySampleRate, 2 * spotifySampleRate} {
		var err error
		if !returnsIn(promptly, func() { err = s.Seek(target) }) {
			t.Fatalf("Seek(%d) waited for the held read", target)
		}
		if err != nil {
			t.Fatalf("Seek(%d) = %v", target, err)
		}
		if got := s.Position(); got != target {
			t.Errorf("Position() after Seek(%d) = %d", target, got)
		}
	}
	buf, n, ok := pull(s, 512)
	if !ok || n != len(buf) {
		t.Fatalf("while the seek waits for the source, Stream() = %d, %v, want silence", n, ok)
	}
	for _, v := range buf {
		if v[0] != 0 {
			t.Fatalf("while the seek waits for the source the speaker got %v, old audio", v[0])
		}
	}

	release()
	want := float64(2*spotifySampleRate + fade + 1)
	if v := audible(t, s); v[0] != want || !consecutive(v) {
		t.Errorf("after the seeks the audio starts at %v, want %v (the latest target)", v[0], want)
	}
	if _, _, seeks := src.state(); len(seeks) == 0 || seeks[len(seeks)-1] != 2000 {
		t.Errorf("source seeks = %v, want the last at 2000 ms", seeks)
	}
}

func TestPlaybackEndsAndErrors(t *testing.T) {
	errBoom := errors.New("failed reading chunk 3: connection reset")

	// A read error ends the stream after the audio decoded before it.
	t.Run("read error", func(t *testing.T) {
		src := &pcmSource{failAt: 8192, failErr: errBoom}
		s := playbackOf(t, src)
		if last := drain(t, s); last != 8192 || !errors.Is(s.Err(), errBoom) {
			t.Errorf("stream ended after %v with %v, want after 8192 with %v", last, s.Err(), errBoom)
		}
	})

	// A clean end ends the stream after the last sample, with no error.
	t.Run("clean end", func(t *testing.T) {
		src := &pcmSource{total: 8192}
		s := playbackOf(t, src)
		if last := drain(t, s); last != 8192 || s.Err() != nil {
			t.Errorf("stream ended after %v with %v, want after 8192 with none", last, s.Err())
		}
	})

	// An error from the read a seek replaced is not the stream's: the seek
	// is carried out, the stream plays on, and its clean end is clean.
	t.Run("error of a replaced read", func(t *testing.T) {
		src := &pcmSource{total: 6*spotifySampleRate + 9000}
		s := playbackOf(t, src)
		audible(t, s)
		held, release := src.hold()
		src.mu.Lock()
		src.failHeld = errBoom
		src.mu.Unlock()
		pumpUntil(s, held)
		awaitClosed(t, held, "the decoder's next read")
		target := 6 * spotifySampleRate
		if !returnsIn(promptly, func() { _ = s.Seek(target) }) {
			t.Fatal("the seek waited for the held read")
		}
		release()
		if v := audible(t, s); v[0] != float64(target+fade+1) {
			t.Errorf("after the seek the audio starts at %v, want %v", v[0], target+fade+1)
		}
		if last := drain(t, s); last != float64(src.total) || s.Err() != nil {
			t.Errorf("stream ended after %v with %v, want after %d with none", last, s.Err(), src.total)
		}
	})

	// A seek after the decoder reached the end, before the speaker did,
	// plays from the target: the end does not strand it.
	t.Run("seek after the end", func(t *testing.T) {
		src := &pcmSource{total: 8192}
		s := playbackOf(t, src)
		audible(t, s)
		for deadline := time.Now().Add(promptly); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if pos, _, _ := src.state(); pos == src.total {
				break
			}
		}
		if err := s.Seek(441); err != nil { // 10 ms
			t.Fatal(err)
		}
		if v := audible(t, s); v[0] != 441+fade+1 || !consecutive(v) {
			t.Errorf("after the seek the audio starts at %v, want %d", v[0], 441+fade+1)
		}
	})

	// A seek the decoder refuses is not the stream's error (beep's
	// StreamSeeker: the position stays, the error is not Err's): the call
	// returns at once, and the stream plays on from where the decoder is,
	// with no error. The resume of a saved position (playAt) relies on it.
	// An error that wraps net.ErrClosed, from a stream nobody closed, is a
	// refusal too, not a close.
	for name, errSeek := range map[string]error{
		"refused seek plays on":                  errors.New("failed seeking input: chunk unavailable"),
		"refused seek wrapping a close plays on": fmt.Errorf("failed seeking input: failed reading chunk 2: %w", net.ErrClosed),
	} {
		t.Run(name, func(t *testing.T) {
			src := &pcmSource{seekErr: errSeek}
			s := playbackOf(t, src)
			audible(t, s)
			var err error
			if !returnsIn(promptly, func() { err = s.Seek(spotifySampleRate) }) || err != nil {
				t.Fatalf("Seek() = %v, want it accepted at once", err)
			}
			v := audible(t, s)
			src.mu.Lock()
			at := src.refusedAt
			src.mu.Unlock()
			if want := float64(at + fade + 1); v[0] != want || !consecutive(v) {
				t.Errorf("after the refused seek the audio starts at %v, want %v: where the decoder was", v[0], want)
			}
			// The decoder tells its position in milliseconds, as Position
			// always went by it: within one millisecond of the sample.
			if got, want := s.Position(), int(at)+512; got > want || got < want-spotifySampleRate/1000 {
				t.Errorf("Position() = %d, want %d to the millisecond: rebased on the decoder", got, want)
			}
			if s.Err() != nil {
				t.Errorf("Err() = %v after a refused seek, want nil", s.Err())
			}
		})
	}

	// A refused seek that a newer seek overtook changes nothing: the newer
	// target plays.
	t.Run("refused seek overtaken", func(t *testing.T) {
		src := &pcmSource{}
		s := playbackOf(t, src)
		audible(t, s)
		held, release := src.holdSeek(errors.New("failed seeking input: chunk unavailable"))
		if err := s.Seek(spotifySampleRate); err != nil {
			t.Fatal(err)
		}
		awaitClosed(t, held, "the decoder's seek")
		if err := s.Seek(3 * spotifySampleRate); err != nil {
			t.Fatal(err)
		}
		release()
		if v := audible(t, s); v[0] != float64(3*spotifySampleRate+fade+1) || !consecutive(v) {
			t.Errorf("after the overtaken refusal the audio starts at %v, want %d", v[0], 3*spotifySampleRate+fade+1)
		}
		if got := s.Position(); got != 3*spotifySampleRate+512 {
			t.Errorf("Position() = %d, want %d", got, 3*spotifySampleRate+512)
		}
		if _, _, seeks := src.state(); len(seeks) != 2 || seeks[0] != 1000 || seeks[1] != 3000 {
			t.Errorf("decoder seeks = %v, want [1000 3000]", seeks)
		}
	})

	// After a refused seek, a read error is still the stream's end.
	t.Run("read error after a refused seek", func(t *testing.T) {
		const failAt = 60 * 4096 // a whole number of the decoder reads
		src := &pcmSource{seekErr: errors.New("failed seeking input: chunk unavailable"), failAt: failAt, failErr: errBoom}
		s := playbackOf(t, src)
		audible(t, s)
		if err := s.Seek(spotifySampleRate); err != nil {
			t.Fatal(err)
		}
		if last := drain(t, s); last != failAt || !errors.Is(s.Err(), errBoom) {
			t.Errorf("stream ended after %v with %v, want after %d with %v", last, s.Err(), failAt, errBoom)
		}
	})

	// The new model, not a defect of the old: a read error ends the stream,
	// and a seek after it, made before the speaker has had the audio before
	// the error, retries nothing: it returns the error, and the stream still
	// ends with it.
	t.Run("seek after a read error", func(t *testing.T) {
		src := &pcmSource{failAt: 8192, failErr: errBoom}
		s := playbackOf(t, src)
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if _, reads, _ := src.state(); reads >= 3 {
				break // the third read is the failing one
			}
			if time.Now().After(deadline) {
				t.Fatal("the decoder never reached the error")
			}
		}
		time.Sleep(50 * time.Millisecond) // the error is queued behind the audio
		if err := s.Seek(0); !errors.Is(err, errBoom) {
			t.Errorf("Seek() after the read error = %v, want %v", err, errBoom)
		}
		if last := drain(t, s); last != 8192 || !errors.Is(s.Err(), errBoom) {
			t.Errorf("stream ended after %v with %v, want after 8192 with %v", last, s.Err(), errBoom)
		}
		if _, _, seeks := src.state(); len(seeks) != 0 {
			t.Errorf("the decoder was seeked %v after its error", seeks)
		}
	})
}

// Stopping, and skipping to the next track, while a read is held: Stop
// takes the speaker's lock and then closes the old stream, which ends the
// read; the next track plays.
func TestPlaybackStopAndSkipWhileAReadIsHeld(t *testing.T) {
	src := &pcmSource{}
	held, _ := src.hold()
	s := playbackOf(t, src)
	pumpUntil(s, held)
	awaitClosed(t, held, "the decoder's read")

	var cleared atomic.Bool
	stop := func() {
		speaker.Lock()
		cleared.Store(true) // Stop clears the speaker's source under its lock
		speaker.Unlock()
		_ = s.Close()
	}
	if !returnsIn(promptly, stop) || !cleared.Load() {
		t.Fatal("stopping waited for the held read")
	}
	if n, ok := s.Stream(make([][2]float64, 16)); ok || n != 0 {
		t.Errorf("Stream() after Close = %d, %v, want 0, false", n, ok)
	}
	next := playbackOf(t, &pcmSource{})
	if v := audible(t, next); v[0] != fade+1 {
		t.Errorf("the next track starts at %v, want %d", v[0], fade+1)
	}
}

// The decoder of an accepted MP3 or FLAC stream is closed once when the
// stream is closed, also when Close comes while the decoder is reading, and
// only after that read (RC4-S3).
func TestPlaybackClosesTheDecoderOnce(t *testing.T) {
	var mu sync.Mutex
	var closes []io.Closer
	orig := closeAcceptedDecoder
	closeAcceptedDecoder = func(c io.Closer) {
		mu.Lock()
		closes = append(closes, c)
		mu.Unlock()
		orig(c)
	}
	t.Cleanup(func() { closeAcceptedDecoder = orig })
	closed := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(closes)
	}

	t.Run("mp3, closed during a read", func(t *testing.T) {
		mu.Lock()
		closes = nil
		mu.Unlock()
		in := &gatedInput{Reader: fixture(t, "tone.mp3.bin")}
		d := newMP3(t, in)
		entered, release := in.arm()
		s := newPlaybackStreamer(newSpotifyStreamer(&librespotPlayer.Stream{Source: d}, release))
		t.Cleanup(release)
		pumpUntil(s, entered)
		awaitClosed(t, entered, "the decoder's read")
		h, _, _ := mp3Native(d)

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() {
				if !returnsIn(5*time.Second, func() { _ = s.Close() }) {
					t.Error("Close did not return")
				}
			})
		}
		wg.Wait()
		if n := closed(); n != 1 {
			t.Errorf("the decoder was closed %d times, want once", n)
		}
		if after, cell, mh := mp3Native(d); resolves(h) || after != 0 || cell || mh {
			t.Error("the MP3 decoder's native handles were not released")
		}
	})

	t.Run("flac", func(t *testing.T) {
		mu.Lock()
		closes = nil
		mu.Unlock()
		d, err := flac.New(&librespot.NullLogger{}, fixture(t, "tone.flac.bin"), 1)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if closed() == 0 {
				_ = d.Close()
			}
		})
		s := newPlaybackStreamer(newSpotifyStreamer(&librespotPlayer.Stream{Source: d}, nil))
		pull(s, 512)
		for range 2 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
		if n := closed(); n != 1 {
			t.Errorf("the decoder was closed %d times, want once", n)
		}
	})
}

// The player plays the stream decoded away from the speaker, not the raw
// streamer read under the speaker's lock.
func TestThePlayerGetsThePrefetchedStream(t *testing.T) {
	stubStreams(t, func() error { return nil }, nil)
	p := New(goodSession(), "client", 320)
	s, _, _, err := p.PlaybackStreamer(testTrackURI)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, raw := s.(*spotifyStreamer); raw {
		t.Error("the player gets the raw streamer, which reads under the speaker's lock")
	}
}
