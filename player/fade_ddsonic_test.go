package player

// ddsonic: the sleep timer's fade gain (fade_ddsonic.go, #35).

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

// ones is a streamer of full-scale samples, so the output is the gain.
var ones = beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
	for i := range samples {
		samples[i] = [2]float64{1, 1}
	}
	return len(samples), true
})

var fadeT0 = time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)

// fadeRig is a volume stage at 0 dB with a fade on a clock the test moves.
// Samples are 1 ms apart.
type fadeRig struct {
	now time.Time
	f   *fadeGain
	v   *volumeStreamer
}

const fadeStep = time.Millisecond

func newFadeRig() *fadeRig {
	r := &fadeRig{now: fadeT0, f: &fadeGain{}}
	r.f.now = func() time.Time { return r.now }
	var vol atomic.Uint64 // 0 dB
	r.v = &volumeStreamer{s: ones, vol: &vol, mono: new(atomic.Bool), cachedDB: math.NaN(), fade: r.f}
	return r
}

// fadeOut is FadeOutUntil(now + d).
func (r *fadeRig) fadeOut(d time.Duration) { r.f.send(0, r.now, r.now.Add(d), fadeStep) }

// cancel is CancelFade.
func (r *fadeRig) cancel() { r.f.send(1, r.now, r.now.Add(cancelFadeRamp), fadeStep) }

// play streams n samples in buffers of buf, with the clock keeping time as
// a real device would, and returns the left channel.
func (r *fadeRig) play(n, buf int) []float64 {
	out := make([]float64, 0, n)
	samples := make([][2]float64, buf)
	for len(out) < n {
		k, _ := r.v.Stream(samples[:min(buf, n-len(out))])
		for _, s := range samples[:k] {
			out = append(out, s[0])
		}
		r.now = r.now.Add(time.Duration(k) * fadeStep)
	}
	return out
}

// The fade reaches exactly 0 at its end instant, whatever the buffer size,
// and stays there.
func TestFadeOutEndsAtItsEndInstant(t *testing.T) {
	const total = 1000 // samples, = 1 s
	for _, buf := range []int{1, 7, 512, 4096} {
		r := newFadeRig()
		r.fadeOut(total * fadeStep)
		got := r.play(total+300, buf)
		if got[0] != 1 {
			t.Errorf("buf %d: first sample %v, want 1", buf, got[0])
		}
		for i := 1; i < len(got); i++ {
			if got[i] > got[i-1] {
				t.Fatalf("buf %d: sample %d rose from %v to %v", buf, i, got[i-1], got[i])
			}
		}
		if got[total-1] <= 0 {
			t.Errorf("buf %d: silent before the end (sample %d = %v)", buf, total-1, got[total-1])
		}
		for i := total; i < len(got); i++ {
			if got[i] != 0 {
				t.Fatalf("buf %d: sample %d = %v at or after the end, want exactly 0", buf, i, got[i])
			}
		}
	}
}

// A stall (no samples processed while the clock runs) or a command taken up
// late cannot make the fade end late: the first samples processed at or
// after the end are exactly 0.
func TestFadeIsSilentAtTheEndAfterAStall(t *testing.T) {
	tests := []struct {
		name   string
		before int           // samples played before the stall
		stall  time.Duration // clock time with nothing processed
		want0  int           // index of the first sample after the stall that must be 0
	}{
		{"stall past the end", 100, 2 * time.Second, 0},
		{"stall to the end exactly", 100, 900 * time.Millisecond, 0},
		{"stall to just before the end", 100, 899 * time.Millisecond, 1},
		{"command taken up after the end", 0, 5 * time.Second, 0},
		{"command taken up mid-fade", 0, 500 * time.Millisecond, 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig()
			r.fadeOut(time.Second)
			r.play(tt.before, 64)
			r.now = r.now.Add(tt.stall)
			got := r.play(tt.want0+50, 64)
			if tt.want0 > 0 && got[tt.want0-1] <= 0 {
				t.Errorf("sample %d = %v, want still audible before the end", tt.want0-1, got[tt.want0-1])
			}
			for i := tt.want0; i < len(got); i++ {
				if got[i] != 0 {
					t.Fatalf("sample %d = %v, want exactly 0 at or after the end", i, got[i])
				}
			}
		})
	}
	t.Run("gain at the end with no samples at all", func(t *testing.T) {
		r := newFadeRig()
		r.fadeOut(time.Second)
		r.now = r.now.Add(time.Second)
		r.f.applied = r.f.cmd.Load() // taken up, not yet streamed
		if g := r.f.gainAt(r.f.leftAt(r.now)); g != 0 {
			t.Errorf("gain at the end = %v, want 0", g)
		}
	})
}

func TestCancelFadeRestoresTheLevel(t *testing.T) {
	tests := []struct {
		name  string
		after int // samples of fade-out before the cancel
	}{
		{"after silence", 1050},
		{"mid-fade", 500},
		{"at once", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig()
			r.fadeOut(time.Second)
			faded := r.play(tt.after, 64)
			level := 1.0
			if len(faded) > 0 {
				level = faded[len(faded)-1]
				if level >= 1 {
					t.Fatalf("setup: %d samples of fade left the level at %v", tt.after, level)
				}
			}
			r.cancel()
			ramp := int(cancelFadeRamp / fadeStep)
			got := r.play(ramp+200, 64)
			if math.Abs(got[0]-level) > 0.01 {
				t.Errorf("cancel jumped the level from %v to %v: the restore starts where the fade was", level, got[0])
			}
			for i := 1; i < len(got); i++ {
				if got[i] < got[i-1] {
					t.Fatalf("sample %d fell from %v to %v while restoring", i, got[i-1], got[i])
				}
			}
			for i := ramp; i < len(got); i++ {
				if got[i] != 1 {
					t.Fatalf("sample %d = %v after the %v restore, want exactly 1", i, got[i], cancelFadeRamp)
				}
			}
		})
	}
}

// With no fade command, or after a reset, the stage is unity: the output is
// exactly the volume stage's, as before the fade existed.
func TestFadeIdleHasNoEffect(t *testing.T) {
	tests := []struct {
		name string
		prep func(r *fadeRig)
	}{
		{"never used", func(*fadeRig) {}},
		{"reset", func(r *fadeRig) { r.fadeOut(time.Second); r.f.send(1, r.now, r.now, fadeStep) }},
		{"reset after silence", func(r *fadeRig) {
			r.fadeOut(10 * fadeStep)
			r.play(20, 20)
			r.f.send(1, r.now, r.now, fadeStep)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig()
			tt.prep(r)
			r.v.vol.Store(math.Float64bits(-6))
			want := math.Pow(10, -6.0/20)
			for i, got := range r.play(300, 64) {
				if got != want {
					t.Fatalf("sample %d = %v, want %v (the volume's gain alone)", i, got, want)
				}
			}
		})
	}
	t.Run("no fade stage", func(t *testing.T) {
		v := &volumeStreamer{s: ones, vol: new(atomic.Uint64), mono: new(atomic.Bool), cachedDB: math.NaN()}
		samples := make([][2]float64, 64)
		v.Stream(samples)
		if samples[63][0] != 1 {
			t.Errorf("sample = %v, want 1", samples[63][0])
		}
	})
}

// The fade never touches the user's volume: Volume() before, during and
// after a fade, a cancel and a reset is what the user set.
func TestFadeLeavesTheUserVolumeAlone(t *testing.T) {
	p := &Player{sr: 44100}
	p.SetVolumeMin(-50)
	p.SetVolume(-7)
	v := &volumeStreamer{s: ones, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN(), fade: &p.fade}
	samples := make([][2]float64, 128)
	steps := []struct {
		name string
		do   func()
	}{
		{"fade out", func() { p.FadeOutUntil(time.Now().Add(10 * time.Millisecond)) }},
		{"ramp", func() { v.Stream(samples) }},
		{"cancel", p.CancelFade},
		{"restore", func() { v.Stream(samples) }},
		{"reset", p.ResetFade},
	}
	for _, s := range steps {
		s.do()
		if got := p.Volume(); got != -7 {
			t.Fatalf("after %s: Volume() = %v, want -7", s.name, got)
		}
	}
}

func TestPlayerFadeCommands(t *testing.T) {
	now := fadeT0
	p := &Player{sr: 48000}
	p.fade.now = func() time.Time { return now }
	end := now.Add(30 * time.Second)
	p.FadeOutUntil(end)
	if c := p.fade.cmd.Load(); c.to != 0 || !c.start.Equal(now) || !c.end.Equal(end) || c.step != time.Second/48000 {
		t.Errorf("FadeOutUntil = %+v, want to 0 from now to the deadline, 1/48000 s per sample", *c)
	}
	p.CancelFade()
	if c := p.fade.cmd.Load(); c.to != 1 || c.end.Sub(c.start) != cancelFadeRamp {
		t.Errorf("CancelFade = %+v, want to 1 over 50ms", *c)
	}
	p.ResetFade()
	if c := p.fade.cmd.Load(); c.to != 1 || !c.end.Equal(c.start) {
		t.Errorf("ResetFade = %+v, want to 1 at once", *c)
	}
	(&Player{}).FadeOutUntil(end) // no sample rate yet: no division by zero
}

// The volume stage is on the audio hot path: with the real clock, a fade
// running or idle, streaming a buffer allocates nothing.
func TestFadeStageDoesNotAllocate(t *testing.T) {
	samples := make([][2]float64, 512)
	for _, tt := range []struct {
		name string
		prep func(f *fadeGain)
	}{
		{"idle", func(*fadeGain) {}},
		{"fading", func(f *fadeGain) { now := time.Now(); f.send(0, now, now.Add(time.Hour), fadeStep) }},
		{"silent", func(f *fadeGain) { now := time.Now(); f.send(0, now, now, fadeStep) }},
		{"restoring", func(f *fadeGain) { now := time.Now(); f.send(1, now, now.Add(time.Hour), fadeStep) }},
		{"restored", func(f *fadeGain) { now := time.Now(); f.send(1, now, now, fadeStep) }},
	} {
		f := &fadeGain{}
		tt.prep(f)
		var vol atomic.Uint64
		v := &volumeStreamer{s: ones, vol: &vol, mono: new(atomic.Bool), cachedDB: math.NaN(), fade: f}
		v.Stream(samples) // take up the command
		if allocs := testing.AllocsPerRun(1000, func() { v.Stream(samples) }); allocs != 0 {
			t.Errorf("%s: %v allocations per Stream, want 0", tt.name, allocs)
		}
	}
}

func benchStage(b *testing.B, prep func(f *fadeGain)) {
	f := &fadeGain{}
	prep(f)
	var vol atomic.Uint64
	v := &volumeStreamer{s: ones, vol: &vol, mono: new(atomic.Bool), cachedDB: math.NaN(), fade: f}
	samples := make([][2]float64, 512)
	b.ReportAllocs()
	for b.Loop() {
		v.Stream(samples)
	}
}

func BenchmarkVolumeStageFading(b *testing.B) {
	benchStage(b, func(f *fadeGain) { now := time.Now(); f.send(0, now, now.Add(time.Hour), fadeStep) })
}

func BenchmarkVolumeStageIdle(b *testing.B) { benchStage(b, func(*fadeGain) {}) }

// BenchmarkVolumeStageNoFade is the stage without a fade stage, as before
// the sleep timer: the baseline for the idle cost.
func BenchmarkVolumeStageNoFade(b *testing.B) {
	var vol atomic.Uint64
	v := &volumeStreamer{s: ones, vol: &vol, mono: new(atomic.Bool), cachedDB: math.NaN()}
	samples := make([][2]float64, 512)
	b.ReportAllocs()
	for b.Loop() {
		v.Stream(samples)
	}
}
