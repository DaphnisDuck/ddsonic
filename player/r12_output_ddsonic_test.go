package player

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// #83 (R-12): what a listener hears around pause/resume and a manual track
// switch, measured without audio hardware. The real Player (PlayAt,
// TogglePause, Stop) drives a model of the output device that Beep's speaker
// and oto v3.4.1 put behind it. Every frame the model sends to the device is
// recorded and classified, so a silence inside the music, a dropped or
// repeated frame, or a frame of the previous track is counted exactly.
//
// What the model copies from the dependencies ($BEEP = gopxl/beep/v2@v2.1.1,
// $OTO = ebitengine/oto/v3@v3.4.1):
//   - Beep gives half of speaker.Init's buffer to oto's player buffer
//     ($BEEP/speaker/speaker.go:47-48, :64) and the other half to the driver.
//   - oto's mux goroutine reads the source whenever the player buffer holds
//     less than its size, one buffer-size chunk per read, so the buffer holds
//     between one and two chunks ($OTO/internal/mux/mux.go:90-119, :521-567).
//     Beep's reader fills the whole chunk ($BEEP/speaker/speaker.go:167-200),
//     from the chain under the speaker lock (:204-208).
//   - The driver takes one period at a time from that buffer
//     ($OTO/internal/mux/mux.go:141-156, :460-519): on Linux a period is half
//     the driver buffer ($OTO/driver_unix.go:153-160, :214-238); on macOS a
//     quarter ($OTO/driver_darwin.go:30, :101-103, :183-217).
//   - Context.Suspend stops the driver taking periods and clears nothing
//     ($OTO/driver_unix.go:241-256, $OTO/driver_darwin.go:219-230); no Reset
//     of the player buffer ($OTO/internal/mux/mux.go:379-392) is ever called
//     by Beep. Resume takes up where it stopped.
//   - Suspend does not stop the device at once: on Linux it waits for the
//     writer's lock, which the writer holds across a blocking write and can
//     take again first ($OTO/driver_unix.go:214-219, :244); on macOS the
//     loop pauses the queue on its next turn ($OTO/driver_darwin.go:163-198).
//     suspendLag is how many periods the device still takes after Suspend is
//     called; it is a parameter, not a measured value.
//   - The mux is its own goroutine ($OTO/internal/mux/mux.go:68, :90-119),
//     signalled by each driver read (:155); whether it refills before or
//     after a pause or Stop changes the chain, and whether before Suspend,
//     is scheduling. lateRefill puts each refill at the next event (the
//     next write, Suspend or Resume) instead of right after the read: the
//     two orderings bound what the model shows, not every interleaving.
//   - At speaker.Init the player reads one chunk from the empty mixer, which
//     is silence ($OTO/internal/mux/mux.go:298-335), and Player.New suspends
//     right after (player.go:129).
//
// What it leaves out: the driver's own buffer (the ALSA ring, the
// AudioQueue buffers). It is a queue after the player buffer and changes
// when a frame is heard, not which frames are heard or their order, so the
// recorded sequence is what the listener hears, in order. Pause silence
// (the device stopped) is not in the record; silence the source produced is.

// otoModel is the output device as Beep and oto build it (see above).
type otoModel struct {
	pull       func([][2]float64) // Beep's read of the chain
	chunk      int                // oto player buffer, frames
	period     int                // frames the driver takes per write
	suspendLag int                // periods taken after Suspend is called
	suspendErr error              // a device that cannot be suspended
	lateRefill bool               // the mux refills at the next event, not at once

	pending bool // a refill the mux has not done yet (lateRefill)

	drains    []func() // suspends scheduled with drainAfter, not yet fired
	queue     [][2]float64
	out       [][2]float64
	suspended bool
	marks     []mark
}

type mark struct {
	at   int
	name string
}

func (m *otoModel) refill() {
	if len(m.queue) < m.chunk {
		c := make([][2]float64, m.chunk)
		m.pull(c)
		m.queue = append(m.queue, c...)
	}
}

// step is one driver write: a period from the player buffer, then the mux
// refills it.
func (m *otoModel) step() {
	m.runPending()
	if m.suspended {
		return
	}
	n := min(m.period, len(m.queue))
	m.out = append(m.out, m.queue[:n]...)
	for range m.period - n {
		m.out = append(m.out, [2]float64{}) // underrun
	}
	m.queue = append([][2]float64(nil), m.queue[n:]...)
	if m.lateRefill {
		m.pending = true
		return
	}
	m.refill()
}

// runPending is the mux goroutine getting to a refill it was signalled for.
func (m *otoModel) runPending() {
	if m.pending {
		m.pending = false
		m.refill()
	}
}

func (m *otoModel) steps(n int) {
	for range n {
		m.step()
	}
}

func (m *otoModel) Suspend() error {
	if m.suspendErr != nil {
		return m.suspendErr
	}
	m.runPending() // late: the mux refills after the action, before the suspend holds
	m.steps(m.suspendLag)
	m.suspended = true
	return nil
}

func (m *otoModel) Resume() error {
	m.runPending()
	m.suspended = false
	return nil
}

// drain plays what a drainAfter delay of d lets the device play, then fires
// the suspends scheduled with it.
func (m *otoModel) drain(d time.Duration) {
	m.steps(int(math.Ceil(float64(beep.SampleRate(fixtureRate).N(d)) / float64(m.period))))
	fns := m.drains
	m.drains = nil
	for _, f := range fns {
		f()
	}
}

func (m *otoModel) mark(name string) { m.marks = append(m.marks, mark{len(m.out), name}) }

// newModelPlayer is Player.New with the model for a device: the same fields,
// a suspended device holding Init's chunk of silence.
func newModelPlayer(t *testing.T, bufferMs, periodsPerChunk, suspendLag int, lateRefill bool) (*Player, *otoModel) {
	t.Helper()
	sr := beep.SampleRate(fixtureRate)
	frames := sr.N(time.Duration(bufferMs) * time.Millisecond)
	p := &Player{
		sr:                  sr,
		resampleQuality:     4,
		bitDepth:            16,
		tapBufferFrames:     tapRingFrames(frames),
		speakerBufferFrames: frames,
		gapless:             &gaplessStreamer{},
		suspended:           true,
	}
	p.volMin.Store(math.Float64bits(-50))
	p.speed.Store(math.Float64bits(1.0))
	p.gapless.onSwap = p.handleGaplessSwap
	m := &otoModel{
		chunk:      frames / 2,
		period:     frames / 2 / periodsPerChunk,
		suspendLag: suspendLag,
		lateRefill: lateRefill,
		suspended:  true,
	}
	m.queue = make([][2]float64, m.chunk)
	m.pull = func(buf [][2]float64) {
		speaker.Lock()
		defer speaker.Unlock()
		if p.ctrl == nil {
			clear(buf) // the empty mixer
			return
		}
		p.ctrl.Stream(buf)
	}
	oldSuspend, oldResume, oldDrain := speakerSuspend, speakerResume, drainAfter
	speakerSuspend, speakerResume = m.Suspend, m.Resume
	drainAfter = func(_ time.Duration, f func()) *time.Timer {
		m.drains = append(m.drains, f)
		return nil
	}
	t.Cleanup(func() {
		p.Stop()
		speakerSuspend, speakerResume, drainAfter = oldSuspend, oldResume, oldDrain
	})
	return p, m
}

// Fixtures: 16-bit stereo WAV. Left is a 440 Hz sine; right counts frames,
// 1..counterWrap, positive for track A and negative for track B, so no
// frame of a track is zero and every frame says which track and where.
const (
	fixtureRate = 44100
	counterWrap = 8000 // below 2^13, so the 16-bit round trip is exact
)

func writeFixture(t *testing.T, name string, sign float64, seconds float64) string {
	t.Helper()
	n := int(seconds * fixtureRate)
	data := make([]byte, 0, n*4)
	for i := range n {
		l := int16(math.Round(0.5 * math.Sin(2*math.Pi*440*float64(i)/fixtureRate) * 32767))
		r := int16(sign * float64(i%counterWrap+1))
		data = binary.LittleEndian.AppendUint16(data, uint16(l))
		data = binary.LittleEndian.AppendUint16(data, uint16(r))
	}
	var h []byte
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(36+len(data)))
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	h = binary.LittleEndian.AppendUint16(h, 1) // PCM
	h = binary.LittleEndian.AppendUint16(h, 2)
	h = binary.LittleEndian.AppendUint32(h, fixtureRate)
	h = binary.LittleEndian.AppendUint32(h, fixtureRate*4)
	h = binary.LittleEndian.AppendUint16(h, 4)
	h = binary.LittleEndian.AppendUint16(h, 16)
	h = append(h, "data"...)
	h = binary.LittleEndian.AppendUint32(h, uint32(len(data)))
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, append(h, data...), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// segment is a run of the record: silence, or contiguous frames of a track.
type segment struct {
	track      byte // '0' silence, 'A', 'B'
	start, len int  // position in the record
	first      int  // the track's counter at the first frame
}

// frameOf classifies a recorded frame.
func frameOf(f [2]float64) (track byte, counter int) {
	if f[0] == 0 && f[1] == 0 {
		return '0', 0
	}
	c := int(math.Round(f[1] * 32768))
	if c > 0 {
		return 'A', c
	}
	return 'B', -c
}

// segments splits the record into runs; a track's run ends where its
// counter does not advance by one (a dropped or repeated frame) and at each
// break.
func segments(out [][2]float64, breaks ...int) []segment {
	var segs []segment
	prevTrack, prevC := byte(0), 0
	for i, f := range out {
		tr, c := frameOf(f)
		cont := tr == prevTrack && (tr == '0' || c == prevC%counterWrap+1) && !slices.Contains(breaks, i)
		if !cont || len(segs) == 0 {
			segs = append(segs, segment{track: tr, start: i, first: c})
		}
		segs[len(segs)-1].len++
		prevTrack, prevC = tr, c
	}
	return segs
}

func ms(frames int) string {
	return fmt.Sprintf("%.1fms", float64(frames)*1000/fixtureRate)
}

func describe(m *otoModel) string {
	var b strings.Builder
	mi := 0
	var breaks []int
	for _, mk := range m.marks {
		breaks = append(breaks, mk.at)
	}
	for _, s := range segments(m.out, breaks...) {
		for mi < len(m.marks) && m.marks[mi].at <= s.start {
			fmt.Fprintf(&b, " <%s>", m.marks[mi].name)
			mi++
		}
		if s.track == '0' {
			fmt.Fprintf(&b, " sil %s", ms(s.len))
		} else {
			fmt.Fprintf(&b, " %c@%d %s", s.track, s.first, ms(s.len))
		}
	}
	for ; mi < len(m.marks); mi++ {
		fmt.Fprintf(&b, " <%s>", m.marks[mi].name)
	}
	return b.String()
}

// TestR12ModelMatchesHandComputedCase checks the model itself against a case
// worked out by hand from the oto code cited above: a chunk of 8 frames, a
// period of 4, a source that counts 1, 2, 3...
func TestR12ModelMatchesHandComputedCase(t *testing.T) {
	// The buffer goes [1..8] -> write 1..4, refill -> [5..16] -> write 5..8,
	// no refill -> [9..16] -> write 9..12, refill -> [13..24] -> ...
	tests := []struct {
		name   string
		before int   // driver writes before the source goes silent
		late   bool  // lateRefill
		want   []int // recorded counters; 0 is silence
	}{
		{
			// Buffer [9..16] (one chunk) at the pause: the lagging write
			// takes 9..12, 4 frames are left, the mux reads a chunk of
			// silence behind 13..16. On resume: 13..16, 8 frames of
			// silence, then 17...
			name:   "silence enters",
			before: 2,
			want:   []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0, 0, 0, 0, 0, 0, 0, 0, 17, 18, 19, 20},
		},
		{
			// Buffer [13..24] (one and a half chunks) at the pause: the
			// lagging write takes 13..16, 8 frames are left, the mux reads
			// nothing. On resume the music continues.
			name:   "no silence",
			before: 3,
			want:   []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24},
		},
		{
			// As "no silence", but the mux refills late. The third write
			// leaves [13..16] with a refill pending; the mux gets to it
			// after the source went silent, so a chunk of silence follows
			// 13..16. On resume: 8 frames of silence, then 17...
			name:   "late refill, silence enters",
			before: 3,
			late:   true,
			want:   []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 0, 0, 0, 0, 0, 0, 0, 0, 17, 18, 19, 20},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next, paused := 1, false
			m := &otoModel{chunk: 8, period: 4, suspendLag: 1, lateRefill: tt.late}
			m.pull = func(buf [][2]float64) {
				for i := range buf {
					if paused {
						buf[i] = [2]float64{}
						continue
					}
					buf[i] = [2]float64{float64(next), 0}
					next++
				}
			}
			m.refill()
			m.steps(tt.before)
			paused = true // ctrl.Paused = true, then Suspend
			_ = m.Suspend()
			m.steps(5) // suspended: nothing is taken
			paused = false
			_ = m.Resume()
			for len(m.out) < len(tt.want) {
				m.step()
			}
			got := make([]int, len(tt.want))
			for i := range got {
				got[i] = int(m.out[i][0])
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("record\n got %v\nwant %v", got, tt.want)
			}
		})
	}
}

// TestR12Measure records the scenarios of #83 and logs what is heard. It
// asserts nothing and runs only with -v.
func TestR12Measure(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("a measurement: run with -v")
	}
	a := writeFixture(t, "a.wav", 1, 3)
	b := writeFixture(t, "b.wav", -1, 3)
	type scenario struct {
		name string
		run  func(p *Player, m *otoModel)
	}
	scenarios := []scenario{
		{"pause-resume", func(p *Player, m *otoModel) {
			m.mark("pause")
			p.TogglePause()
			m.steps(10)
			m.mark("resume")
			p.TogglePause()
			m.steps(10)
		}},
		{"stop-then-play", func(p *Player, m *otoModel) {
			m.mark("stop")
			p.Stop()
			m.steps(4)
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			m.mark("playB")
			m.steps(10)
		}},
		{"next-while-playing", func(p *Player, m *otoModel) {
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			m.mark("playB")
			m.steps(10)
		}},
		{"next-while-paused", func(p *Player, m *otoModel) {
			m.mark("pause")
			p.TogglePause()
			m.steps(4)
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			m.mark("playB")
			m.steps(10)
		}},
	}
	for _, sc := range scenarios {
		for _, dev := range []struct {
			name    string
			periods int
		}{{"linux", 2}, {"macos", 4}} {
			for lag := range 3 {
				for before := 6; before < 6+dev.periods+1; before++ {
					name := fmt.Sprintf("%s/%s/lag%d/before%d", sc.name, dev.name, lag, before)
					t.Run(name, func(t *testing.T) {
						p, m := newModelPlayer(t, 250, dev.periods, lag, false)
						if err := p.PlayAt(a, 0, 0); err != nil {
							t.Fatal(err)
						}
						m.mark("playA")
						m.steps(before)
						sc.run(p, m)
						t.Logf("%s:%s", name, describe(m))
					})
				}
			}
		}
	}
}
