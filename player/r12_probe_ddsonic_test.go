//go:build r12probe

package player

// #83 diagnostic probe (not committed). The real Player, in real time,
// behind oto v3.4.1's own mux code (player/internal/r12otomux: oto's
// internal/mux/mux.go copied with only its package line changed), running
// on its own goroutine as in oto. Only the driver is a stand-in: a Linux-
// style writer that takes one period per period of wall time and holds its
// lock across the write ($OTO/driver_unix.go:214-238); Suspend takes that
// lock and sets a flag ($OTO/driver_unix.go:241-256). Beep's speaker reader
// is reproduced ($BEEP/speaker/speaker.go:152-208), under Beep's own lock.
//
// Run: go test -tags r12probe -count=1 -run TestR12Probe -v ./player/

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/player/internal/r12otomux"
	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// probeReader is Beep's sampleReader: the mixer under the speaker lock,
// clamped and converted to 16-bit little-endian.
type probeReader struct {
	mix *beep.Mixer
	buf [][2]float64
}

func (r *probeReader) Read(b []byte) (int, error) {
	ns := len(b) / 4
	if len(r.buf) < ns {
		r.buf = make([][2]float64, ns)
	}
	speaker.Lock()
	r.mix.Stream(r.buf[:ns])
	speaker.Unlock()
	for i := range r.buf[:ns] {
		for c := range 2 {
			v := min(max(r.buf[i][c], -1), 1)
			x := int16(v * (1<<15 - 1))
			b[i*4+c*2] = byte(x)
			b[i*4+c*2+1] = byte(x >> 8)
		}
	}
	return ns * 4, nil
}

var _ io.Reader = (*probeReader)(nil)

type pacedDriver struct {
	mux    *r12otomux.Mux
	period int
	dur    time.Duration

	cond      *sync.Cond
	suspended bool
	stopped   bool

	outMu sync.Mutex
	out   [][2]float64
}

func (d *pacedDriver) loop() {
	buf := make([]float32, d.period*2)
	next := time.Now()
	for {
		d.cond.L.Lock()
		for d.suspended && !d.stopped {
			d.cond.Wait()
			next = time.Now()
		}
		if d.stopped {
			d.cond.L.Unlock()
			return
		}
		d.mux.ReadFloat32s(buf)
		d.outMu.Lock()
		for i := 0; i < len(buf); i += 2 {
			d.out = append(d.out, [2]float64{float64(buf[i]), float64(buf[i+1])})
		}
		d.outMu.Unlock()
		next = next.Add(d.dur)
		time.Sleep(time.Until(next)) // a blocking write, lock held
		d.cond.L.Unlock()
	}
}

func (d *pacedDriver) Suspend() error {
	d.cond.L.Lock()
	d.suspended = true
	d.cond.L.Unlock()
	return nil
}

func (d *pacedDriver) Resume() error {
	d.cond.L.Lock()
	d.suspended = false
	d.cond.Signal()
	d.cond.L.Unlock()
	return nil
}

func (d *pacedDriver) mark() int {
	d.outMu.Lock()
	defer d.outMu.Unlock()
	return len(d.out)
}

func (d *pacedDriver) snapshot() [][2]float64 {
	d.outMu.Lock()
	defer d.outMu.Unlock()
	return append([][2]float64(nil), d.out...)
}

// probeFrame decodes a frame after the 16-bit round trip, which turns
// counter c into c-1 (int16 truncation of c*32767/32768). Counter 1 becomes
// 0 on the right channel: it belongs to the track before it.
func probeFrame(f [2]float64, prevTrack byte) (byte, int) {
	r := math.Round(f[1] * 32768)
	switch {
	case r > 0:
		return 'A', int(r) + 1
	case r < 0:
		return 'B', int(-r) + 1
	case f[0] != 0 && (prevTrack == 'A' || prevTrack == 'B'):
		return prevTrack, 1
	}
	return '0', 0
}

type probeSeg struct {
	track      byte
	start, len int
	first      int
}

func probeSegments(out [][2]float64) []probeSeg {
	var segs []probeSeg
	prevT, prevC := byte(0), 0
	for i, f := range out {
		tr, c := probeFrame(f, prevT)
		cont := tr == prevT && (tr == '0' || c == prevC%counterWrap+1)
		if !cont || len(segs) == 0 {
			segs = append(segs, probeSeg{track: tr, start: i, first: c})
		}
		segs[len(segs)-1].len++
		prevT, prevC = tr, c
	}
	// A track's first frame (sine 0, counter 1 -> 0) reads as one frame of
	// silence before the run that starts at counter 2: it is that run's.
	for i := 0; i+1 < len(segs); i++ {
		if segs[i].track == '0' && segs[i].len == 1 && segs[i+1].first == 2 {
			segs[i+1].start--
			segs[i+1].len++
			segs[i+1].first = 1
			segs = append(segs[:i], segs[i+1:]...)
		}
	}
	return segs
}

func probeDescribe(out [][2]float64, marks map[int]string) string {
	var b strings.Builder
	for _, s := range probeSegments(out) {
		for at, name := range marks {
			if at >= s.start && at < s.start+s.len {
				fmt.Fprintf(&b, " <%s@%s>", name, ms(at-s.start))
			}
		}
		if s.track == '0' {
			fmt.Fprintf(&b, " sil %s", ms(s.len))
		} else {
			fmt.Fprintf(&b, " %c@%d %s", s.track, s.first, ms(s.len))
		}
	}
	return b.String()
}

// probeMetrics: gaps = silences between two music frames after mark; oldAfter
// = frames of A after mark (mark is B's request; 0 when there is no B).
func probeMetrics(out [][2]float64, mark int, countOld bool) (gaps []int, oldAfter int) {
	segs := probeSegments(out[mark:])
	for i, s := range segs {
		if s.track == 'A' && countOld {
			oldAfter += s.len
		}
		if s.track == '0' && i > 0 && i < len(segs)-1 {
			gaps = append(gaps, s.len)
		}
	}
	return gaps, oldAfter
}

func newProbePlayer(t *testing.T) (*Player, *pacedDriver, func()) {
	t.Helper()
	sr := beep.SampleRate(fixtureRate)
	frames := sr.N(250 * time.Millisecond)
	mx := r12otomux.New(fixtureRate, 2, r12otomux.FormatSignedInt16LE)
	mix := &beep.Mixer{}
	pl := mx.NewPlayer(&probeReader{mix: mix})
	pl.SetBufferSize(frames / 2 * 4) // $BEEP/speaker/speaker.go:48,64
	pl.Play()                        // Init: one chunk from the empty mixer
	d := &pacedDriver{
		mux:    mx,
		period: frames / 4, // half of the driver's half ($OTO/driver_unix.go:153-160)
		dur:    sr.D(frames / 4),
		cond:   sync.NewCond(&sync.Mutex{}),
	}
	go d.loop()
	_ = d.Suspend() // Player.New suspends at once
	p := &Player{
		sr: sr, resampleQuality: 4, bitDepth: 16,
		tapBufferFrames: tapRingFrames(frames), speakerBufferFrames: frames,
		gapless: &gaplessStreamer{}, suspended: true,
	}
	p.volMin.Store(math.Float64bits(-50))
	p.speed.Store(math.Float64bits(1.0))
	p.gapless.onSwap = p.handleGaplessSwap
	oldS, oldR, oldX := speakerSuspend, speakerResume, speakerReset
	speakerSuspend, speakerResume = d.Suspend, d.Resume
	speakerReset = func() { pl.Reset(); pl.Play() } // oto's own Reset and Play (copied mux)
	r12ResetOnPausedStart = os.Getenv("R12_RESET") == "1"
	t.Cleanup(func() {
		p.Stop()
		time.Sleep(600 * time.Millisecond) // let a drain-suspend fire on this device
		d.cond.L.Lock()
		d.stopped = true
		d.cond.Signal()
		d.cond.L.Unlock()
		pl.Close()
		speakerSuspend, speakerResume, speakerReset = oldS, oldR, oldX
		r12ResetOnPausedStart = false
	})
	// The first PlayAt builds p.ctrl; Beep's speaker.Play adds it to the mixer.
	addCtrl := func() {
		speaker.Lock()
		mix.Add(p.ctrl)
		speaker.Unlock()
	}
	return p, d, addCtrl
}

func TestR12Probe(t *testing.T) {
	a := writeFixture(t, "a.wav", 1, 6)
	b := writeFixture(t, "b.wav", -1, 6)
	reps := 8
	r12DrainOnPause = os.Getenv("R12_DRAIN_ON_PAUSE") == "1"
	if n, err := strconv.Atoi(os.Getenv("R12_PROBE_REPS")); err == nil {
		reps = n
	}
	only := os.Getenv("R12_PROBE_ONLY")
	scenarios := []struct {
		name string
		old  bool
		run  func(t *testing.T, p *Player, d *pacedDriver) int
	}{
		{"pause-resume", false, func(t *testing.T, p *Player, d *pacedDriver) int {
			probePauseMark = d.mark()
			p.TogglePause()
			time.Sleep(800 * time.Millisecond)
			m := d.mark()
			p.TogglePause()
			return m
		}},
		{"stop-then-play", true, func(t *testing.T, p *Player, d *pacedDriver) int {
			p.Stop()
			time.Sleep(800 * time.Millisecond)
			m := d.mark()
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			return m
		}},
		{"stop-then-play-quick", true, func(t *testing.T, p *Player, d *pacedDriver) int {
			p.Stop()
			time.Sleep(30 * time.Millisecond)
			m := d.mark()
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			return m
		}},
		{"pause-seek-resume", false, func(t *testing.T, p *Player, d *pacedDriver) int {
			probePauseMark = d.mark()
			p.TogglePause()
			time.Sleep(800 * time.Millisecond)
			if err := p.Seek(2500 * time.Millisecond); err != nil {
				t.Fatal(err)
			}
			m := d.mark()
			p.TogglePause()
			return m
		}},
		{"next-while-playing", false, func(t *testing.T, p *Player, d *pacedDriver) int {
			m := d.mark()
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			return m
		}},
		{"pause-resume-quick", false, func(t *testing.T, p *Player, d *pacedDriver) int {
			probePauseMark = d.mark()
			p.TogglePause()
			time.Sleep(150 * time.Millisecond)
			m := d.mark()
			p.TogglePause()
			return m
		}},
		{"next-while-paused", true, func(t *testing.T, p *Player, d *pacedDriver) int {
			probePauseMark = d.mark()
			p.TogglePause()
			time.Sleep(800 * time.Millisecond)
			m := d.mark()
			if err := p.PlayAt(b, 0, 0); err != nil {
				t.Fatal(err)
			}
			return m
		}},
	}
	for _, sc := range scenarios {
		if only != "" && sc.name != only {
			continue
		}
		var withGap, withOld int
		for rep := range reps {
			t.Run(fmt.Sprintf("%s/%d", sc.name, rep), func(t *testing.T) {
				p, d, addCtrl := newProbePlayer(t)
				if err := p.PlayAt(a, 0, 0); err != nil {
					t.Fatal(err)
				}
				addCtrl()
				time.Sleep(700*time.Millisecond + time.Duration(rand.IntN(63))*time.Millisecond)
				probePauseMark = -1
				m := sc.run(t, p, d)
				time.Sleep(700 * time.Millisecond)
				out := d.snapshot()
				gaps, old := probeMetrics(out, m, sc.old)
				if sc.name == "pause-seek-resume" {
					pre := probeSegments(out[:m])
					last := pre[len(pre)-1]
					lastC := (last.first-1+last.len-1)%counterWrap + 1
					if after := probeSegments(out[m:]); len(after) > 0 && after[0].track == 'A' && after[0].first == lastC%counterWrap+1 {
						old = after[0].len
					}
				}
				if len(gaps) > 0 {
					withGap++
				}
				if old > 0 {
					withOld++
				}
				tail, lead := -1, -1
				if pm := probePauseMark; pm >= 0 && pm <= m {
					tail = 0
					for _, sg := range probeSegments(out[pm:m]) {
						if sg.track == '0' {
							break
						}
						tail += sg.len
					}
				}
				if after := probeSegments(out[m:]); len(after) > 1 && after[0].track == '0' {
					lead = after[0].len
				} else {
					lead = 0
				}
				t.Logf("%s rep%d: gaps=%v oldAfter=%s pauseTail=%s resumeLead=%s |%s", sc.name, rep, gapsMs(gaps), ms(old), ms(max(tail, 0)), ms(lead), probeDescribe(out, map[int]string{m: "mark"}))
			})
		}
		t.Logf("SUMMARY %s: %d/%d with a gap after the mark, %d/%d with old-track audio after the mark", sc.name, withGap, reps, withOld, reps)
	}
}

func gapsMs(g []int) []string {
	var s []string
	for _, n := range g {
		s = append(s, ms(n))
	}
	return s
}

// probePauseMark is the record length when a scenario pressed pause (-1: none).
var probePauseMark = -1
