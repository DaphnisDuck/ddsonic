package player

import (
	"sync"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// A compositional check for the Spotify responsiveness fix
// (external/spotify/prefetch_ddsonic.go, whose Stream never waits for the
// network): with a decoder whose Stream never waits, the real Player's
// controls answer while the speaker pulls, a key press before a tick or
// after it. Its negative control is a decoder that waits in Stream, as the
// raw Spotify streamer did on a held network read: TogglePause waits for it.
//
// It is not the Spotify adapter itself (that is external/spotify's tests),
// not the UI and not a device. The speaker is Beep's own lock and a
// goroutine pulling through the player's Ctrl, as Beep's speaker does; the
// device is never suspended or resumed, which needs speaker.Init: the pause
// press finds it suspended already, and the resume press finds it running.

// heldDecoder waits in its first Stream until released.
type heldDecoder struct {
	*playbackTestDecoder
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
}

func (d *heldDecoder) Stream(samples [][2]float64) (int, bool) {
	d.enteredOnce.Do(func() { close(d.entered) })
	<-d.release
	clear(samples)
	return len(samples), true
}

// controlPlayer is a Player playing dec, as playPipeline leaves it, with no
// device.
func controlPlayer(dec beep.StreamSeekCloser) *Player {
	tp := &trackPipeline{decoder: dec, stream: dec, format: beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2}, seekable: true}
	p := &Player{sr: 100, gapless: &gaplessStreamer{}, current: tp, started: true, suspended: true}
	p.ctrl = &beep.Ctrl{Streamer: p.gapless}
	p.gapless.Replace(tp.stream)
	p.playing.Store(true)
	return p
}

// speakerPulls pulls through p's Ctrl under the speaker's lock, as Beep's
// speaker does, until stop is closed; the channel it returns is closed when
// it has stopped.
func speakerPulls(p *Player, stop <-chan struct{}) <-chan struct{} {
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		buf := make([][2]float64, 64)
		for {
			select {
			case <-stop:
				return
			default:
			}
			speaker.Lock()
			p.ctrl.Stream(buf)
			speaker.Unlock()
			time.Sleep(time.Millisecond)
		}
	}()
	return stopped
}

// answersWithin reports whether f returned within d.
func answersWithin(d time.Duration, f func()) bool {
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

func TestPlayerControlsAnswerWhileTheSpeakerPulls(t *testing.T) {
	for _, keyFirst := range []bool{true, false} {
		name := "tick first"
		if keyFirst {
			name = "key first"
		}
		t.Run(name, func(t *testing.T) {
			p := controlPlayer(newPlaybackTestDecoder())
			stop := make(chan struct{})
			pulling := speakerPulls(p, stop)
			defer func() {
				close(stop)
				<-pulling
			}()

			key := func() { p.TogglePause() }
			tick := func() {
				_, _ = p.PositionAndDuration()
				_ = p.Position()
				_ = p.StreamErr()
			}
			steps, names := []func(){tick, key}, []string{"the tick", "the key press"}
			if keyFirst {
				steps, names = []func(){key, tick}, []string{"the key press", "the tick"}
			}
			for i, step := range steps {
				if !answersWithin(time.Second, step) {
					t.Fatalf("%s did not return while the speaker pulled", names[i])
				}
			}
			if !p.IsPaused() || !p.IsPlaying() {
				t.Errorf("after the key press: paused %v, playing %v; want paused, still playing", p.IsPaused(), p.IsPlaying())
			}

			p.suspendMu.Lock()
			p.suspended = false // the resume press finds the device running
			p.suspendMu.Unlock()
			if !answersWithin(time.Second, p.TogglePause) {
				t.Fatal("the resume press did not return")
			}
			if p.IsPaused() || !p.IsPlaying() {
				t.Errorf("after the resume: paused %v, playing %v; want playing", p.IsPaused(), p.IsPlaying())
			}
		})
	}

	// The negative control: a decoder that waits in Stream holds the
	// speaker's lock, and TogglePause waits for it, until it returns.
	t.Run("decoder waiting in Stream", func(t *testing.T) {
		dec := &heldDecoder{playbackTestDecoder: newPlaybackTestDecoder(), entered: make(chan struct{}), release: make(chan struct{})}
		p := controlPlayer(dec)
		stop := make(chan struct{})
		pulling := speakerPulls(p, stop)
		released := sync.OnceFunc(func() { close(dec.release) })
		defer func() {
			released()
			close(stop)
			<-pulling
		}()
		<-dec.entered
		done := make(chan struct{})
		go func() {
			p.TogglePause()
			close(done)
		}()
		select {
		case <-done:
			t.Fatal("TogglePause returned while the decoder waited in Stream: the check does not discriminate")
		case <-time.After(200 * time.Millisecond):
		}
		released()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("TogglePause did not return once the decoder did")
		}
		if !p.IsPaused() {
			t.Error("the delayed key press did not pause")
		}
	})
}
