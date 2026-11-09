package player

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/wav"
)

// #83 (R-12) regression tests, on the output model of
// r12_output_ddsonic_test.go. Each case runs on both device shapes (a Linux
// period is half the driver buffer, a macOS one a quarter), with the device
// still taking 0-2 periods after Suspend is called, from every point of the
// player buffer's refill cycle, and for two sources: a local WAV file, and
// the same audio through a registered StreamerFactory, the path Spotify's
// stream takes into the player (pipeline.go buildPipeline). The second is
// not Spotify's prefetch adapter and not real Spotify audio.

const r12BufferMs = 250 // the default buffer_ms (config/config.go)

type r12Case struct {
	name       string
	periods    int // driver periods per player-buffer chunk
	suspendLag int
	before     int // driver writes between the start and the action
	source     string
}

func r12Cases() []r12Case {
	var cases []r12Case
	for _, source := range []string{"local", "factory"} {
		for _, dev := range []struct {
			name    string
			periods int
		}{{"linux", 2}, {"macos", 4}} {
			for lag := range 3 {
				for before := 6; before <= 6+dev.periods; before++ {
					cases = append(cases, r12Case{
						name:       fmt.Sprintf("%s/%s/lag%d/before%d", source, dev.name, lag, before),
						periods:    dev.periods,
						suspendLag: lag,
						before:     before,
						source:     source,
					})
				}
			}
		}
	}
	return cases
}

// r12Tracks returns paths for tracks A and B as the case's source plays them.
func r12Tracks(t *testing.T, p *Player, c r12Case) (string, string) {
	t.Helper()
	a := writeFixture(t, "a.wav", 1, 3)
	b := writeFixture(t, "b.wav", -1, 3)
	if c.source == "local" {
		return a, b
	}
	p.RegisterStreamerFactory("r12:", func(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
		path := map[string]string{"r12:a": a, "r12:b": b}[uri]
		f, err := os.Open(path)
		if err != nil {
			return nil, beep.Format{}, 0, err
		}
		s, format, err := wav.Decode(f)
		if err != nil {
			return nil, beep.Format{}, 0, err
		}
		return s, format, 0, nil
	})
	return "r12:a", "r12:b"
}

// heardAfter returns the segments of the record from frame at on.
func heardAfter(m *otoModel, at int) []segment {
	return segments(m.out[at:])
}

func TestR12PauseResumeIsSeamless(t *testing.T) {
	for _, c := range r12Cases() {
		t.Run(c.name, func(t *testing.T) {
			p, m := newModelPlayer(t, r12BufferMs, c.periods, c.suspendLag)
			a, _ := r12Tracks(t, p, c)
			if err := p.PlayAt(a, 0, 0); err != nil {
				t.Fatal(err)
			}
			m.steps(c.before)
			m.mark("pause")
			p.TogglePause()
			if !p.IsPaused() {
				t.Fatal("not paused after the first toggle")
			}
			m.steps(10)
			m.mark("resume")
			p.TogglePause()
			if p.IsPaused() {
				t.Fatal("still paused after the second toggle")
			}
			m.steps(10)
			// Heard: Init's silence, then track A from its first frame to
			// the end, with nothing between.
			segs := segments(m.out)
			if len(segs) != 2 || segs[0].track != '0' || segs[1].track != 'A' || segs[1].first != 1 {
				t.Errorf("not one unbroken run of A:%s", describe(m))
			}
		})
	}
}

func TestR12SwitchAfterStopPlaysNoOldAudio(t *testing.T) {
	for _, c := range r12Cases() {
		for _, wait := range []string{"drained", "at-once"} {
			t.Run(c.name+"/"+wait, func(t *testing.T) {
				p, m := newModelPlayer(t, r12BufferMs, c.periods, c.suspendLag)
				a, b := r12Tracks(t, p, c)
				if err := p.PlayAt(a, 0, 0); err != nil {
					t.Fatal(err)
				}
				m.steps(c.before)
				m.mark("stop")
				p.Stop() // as the library does before playing a selection
				if wait == "drained" {
					m.drain(2 * r12BufferMs * time.Millisecond)
				} else {
					m.steps(1)
				}
				m.mark("playB")
				at := len(m.out)
				if err := p.PlayAt(b, 0, 0); err != nil {
					t.Fatal(err)
				}
				m.drain(2 * r12BufferMs * time.Millisecond) // a suspend still scheduled must not stop B
				m.steps(4)
				// Heard: Init's silence, A unbroken from its first frame, maybe
				// silence, then B unbroken from its first frame. A frame of A
				// after silence would be the stopped track coming back.
				segs := segments(m.out)
				if len(segs) == 4 && segs[2].track == '0' {
					segs = append(segs[:2], segs[3])
				}
				if len(segs) != 3 || segs[0].track != '0' || segs[1].track != 'A' || segs[1].first != 1 ||
					segs[2].track != 'B' || segs[2].first != 1 {
					t.Errorf("not silence, A, silence, B, each unbroken:%s", describe(m))
				}
				// Once drained, nothing of A is left to hear when B is asked
				// for. (Asked for at once, A's last frames are still playing
				// straight on: that is the device's latency, heard as the stop.)
				if wait == "drained" {
					for _, s := range heardAfter(m, at) {
						if s.track == 'A' {
							t.Errorf("A heard after B was asked for:%s", describe(m))
							break
						}
					}
				}
			})
		}
	}
}

func TestR12PauseFallsBackWhenTheDeviceCannotSuspend(t *testing.T) {
	p, m := newModelPlayer(t, r12BufferMs, 2, 0)
	a, _ := r12Tracks(t, p, r12Case{source: "local"})
	if err := p.PlayAt(a, 0, 0); err != nil {
		t.Fatal(err)
	}
	m.steps(6)
	m.suspendErr = errors.New("device lost")
	p.TogglePause()
	if !p.IsPaused() {
		t.Fatal("not paused")
	}
	m.steps(10) // the device runs on
	quiet := m.out[len(m.out)-4*m.period:]
	for _, f := range quiet {
		if f != ([2]float64{}) {
			t.Fatalf("the device that could not be suspended still plays music while paused:%s", describe(m))
		}
	}
	at := len(m.out)
	p.TogglePause()
	m.steps(4)
	// The music comes back where it stopped: the frame after the last one
	// heard before the silence.
	before := segments(m.out[:at])
	last := before[len(before)-2] // the last A run, before the pause's silence
	after := heardAfter(m, at)
	if after[0].track == '0' {
		after = after[1:]
	}
	want := (last.first+last.len-1)%counterWrap + 1
	if len(after) != 1 || after[0].track != 'A' || after[0].first != want {
		t.Errorf("after resuming, want one run of A from %d:%s", want, describe(m))
	}
}
