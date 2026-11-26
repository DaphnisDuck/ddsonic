package player

// ddsonic: the sleep timer's fade gain (fade_ddsonic.go, #35). The tests
// drive it only through the Player's commands and an injected clock, as
// the UI and the audio thread do.

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

// fadeRig is a player at 1000 samples a second (1 ms a sample) and 0 dB,
// its volume stage, and a clock the test moves.
type fadeRig struct {
	t   *testing.T
	now time.Time
	p   *Player
	v   *volumeStreamer
	out []float64 // every sample pulled, in order
}

func newFadeRig(t *testing.T) *fadeRig {
	r := &fadeRig{t: t, now: fadeT0, p: &Player{sr: 1000}}
	r.p.fade.now = func() time.Time { return r.now }
	r.v = &volumeStreamer{s: ones, vol: &r.p.volume, mono: &r.p.mono, cachedDB: math.NaN(), fade: &r.p.fade}
	return r
}

// pull has the audio thread process n samples in one buffer, the clock
// not moving, and returns them.
func (r *fadeRig) pull(n int) []float64 {
	samples := make([][2]float64, n)
	k, _ := r.v.Stream(samples)
	got := make([]float64, k)
	for i := range k {
		got[i] = samples[i][0]
	}
	r.out = append(r.out, got...)
	return got
}

// play processes n samples in buffers of buf, the clock keeping time as a
// device playing them would.
func (r *fadeRig) play(n, buf int) []float64 {
	var got []float64
	for len(got) < n {
		k := min(buf, n-len(got))
		got = append(got, r.pull(k)...)
		r.now = r.now.Add(time.Duration(k) * time.Millisecond)
	}
	return got
}

func (r *fadeRig) wait(d time.Duration) { r.now = r.now.Add(d) }

// smooth fails if any two consecutive samples of s differ by more than
// maxStep: the level never steps.
func smooth(t *testing.T, what string, s []float64, maxStep float64) {
	t.Helper()
	for i := 1; i < len(s); i++ {
		if d := math.Abs(s[i] - s[i-1]); d > maxStep {
			t.Fatalf("%s: samples %d→%d step %v→%v (by %v), more than %v", what, i-1, i, s[i-1], s[i], d, maxStep)
		}
	}
}

func falling(t *testing.T, what string, s []float64) {
	t.Helper()
	for i := 1; i < len(s); i++ {
		if s[i] > s[i-1] {
			t.Fatalf("%s: sample %d rose from %v to %v", what, i, s[i-1], s[i])
		}
	}
}

// The steepest a ramp of `span` samples gets, per sample: the square curve
// starts at twice the average slope. A little margin for rounding.
func slope(span int) float64 { return 2.0/float64(span) + 1e-9 }

// The fade reaches exactly 0 at its end instant, whatever the buffer size,
// and stays there.
func TestFadeOutEndsAtItsEndInstant(t *testing.T) {
	const total = 1000 // samples, = 1 s
	for _, buf := range []int{1, 7, 512, 4096} {
		r := newFadeRig(t)
		r.p.FadeOutUntil(fadeT0.Add(time.Second))
		got := r.play(total+300, buf)
		if got[0] != 1 {
			t.Errorf("buf %d: first sample %v, want 1", buf, got[0])
		}
		falling(t, "fade-out", got)
		smooth(t, "fade-out", got, slope(total))
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

// However the audio thread pulls buffers, the level stays continuous and
// falling: the clock frozen between pulls (the Reviewer's first probe), a
// clock that goes backwards, a burst far ahead of real time.
func TestFadeIsContinuousAcrossBuffers(t *testing.T) {
	tests := []struct {
		name string
		run  func(r *fadeRig)
	}{
		{"clock frozen between pulls", func(r *fadeRig) { r.pull(512); r.pull(512) }},
		{"clock frozen after real time", func(r *fadeRig) { r.play(300, 64); r.pull(100); r.pull(100); r.play(300, 64) }},
		{"clock goes backwards", func(r *fadeRig) { r.play(300, 64); r.wait(-200 * time.Millisecond); r.play(300, 64) }},
		{"a burst ahead of real time", func(r *fadeRig) {
			for range 20 {
				r.pull(32)
			}
			r.wait(640 * time.Millisecond)
			r.play(400, 64)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig(t)
			r.p.FadeOutUntil(fadeT0.Add(time.Second))
			tt.run(r)
			falling(t, tt.name, r.out)
			smooth(t, tt.name, r.out, slope(1000))
		})
	}
}

// A stall (no samples processed while the clock runs), or a command picked
// up late, cannot make the fade end late: every sample processed at or
// after the end is exactly 0.
func TestFadeIsSilentAtTheEndAfterAStall(t *testing.T) {
	tests := []struct {
		name   string
		before int           // samples played before the stall
		stall  time.Duration // clock time with nothing processed
		want0  int           // first sample after the stall that must be 0
	}{
		{"stall past the end", 100, 2 * time.Second, 0},
		{"stall to the end exactly", 100, 900 * time.Millisecond, 0},
		{"stall to just before the end", 100, 899 * time.Millisecond, 1},
		{"command picked up after the end", 0, 5 * time.Second, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig(t)
			r.p.FadeOutUntil(fadeT0.Add(time.Second))
			r.play(tt.before, 64)
			r.wait(tt.stall)
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
}

// A cancel brings the level back to exactly 1 over 50 ms, starting from
// the level the fade had where the cancel is picked up: no step, rising.
// The Reviewer's second probe: a cancel picked up 25 ms after it was given,
// with those 25 ms already read ahead.
func TestCancelFadeRestoresSmoothly(t *testing.T) {
	ramp := int(cancelFadeRamp / time.Millisecond)
	tests := []struct {
		name string
		run  func(r *fadeRig)
	}{
		{"mid-fade", func(r *fadeRig) { r.play(500, 64); r.p.CancelFade() }},
		{"picked up 25 ms late, read ahead", func(r *fadeRig) {
			r.play(500, 64)
			r.pull(25) // read ahead; the clock has not caught up
			r.p.CancelFade()
			r.wait(25 * time.Millisecond)
		}},
		{"at silence", func(r *fadeRig) { r.play(1100, 64); r.p.CancelFade() }},
		{"at once", func(r *fadeRig) { r.p.CancelFade() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig(t)
			r.p.FadeOutUntil(fadeT0.Add(time.Second))
			tt.run(r)
			before := len(r.out)
			r.play(ramp+200, 16)
			smooth(t, tt.name, r.out, slope(ramp))
			restore := r.out[before:]
			for i := 1; i < len(restore); i++ {
				if restore[i] < restore[i-1] {
					t.Fatalf("sample %d fell from %v to %v while restoring", i, restore[i-1], restore[i])
				}
			}
			for i := ramp; i < len(restore); i++ {
				if restore[i] != 1 {
					t.Fatalf("restore sample %d = %v, want exactly 1 after %v", i, restore[i], cancelFadeRamp)
				}
			}
		})
	}
}

// A new fade-out replacing one in progress continues from the level the
// first had reached, and ends at its own instant.
func TestFadeReplacedMidway(t *testing.T) {
	r := newFadeRig(t)
	r.p.FadeOutUntil(fadeT0.Add(time.Second))
	r.play(300, 64)
	r.p.FadeOutUntil(fadeT0.Add(600 * time.Millisecond))
	r.play(400, 64)
	falling(t, "replaced", r.out)
	smooth(t, "replaced", r.out, slope(300))
	for i := 600; i < len(r.out); i++ {
		if r.out[i] != 0 {
			t.Fatalf("sample %d = %v after the new end, want 0", i, r.out[i])
		}
	}
	if r.out[599] <= 0 {
		t.Errorf("silent before the new end")
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
		{"reset", func(r *fadeRig) { r.p.FadeOutUntil(fadeT0.Add(time.Second)); r.p.ResetFade() }},
		{"reset after silence", func(r *fadeRig) {
			r.p.FadeOutUntil(fadeT0.Add(10 * time.Millisecond))
			r.play(20, 20)
			r.p.ResetFade()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newFadeRig(t)
			tt.prep(r)
			r.p.SetVolumeMin(-50)
			r.p.SetVolume(-6)
			start := len(r.out)
			r.play(300, 64)
			want := math.Pow(10, -6.0/20)
			for i, got := range r.out[start:] {
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
	r := newFadeRig(t)
	r.p.SetVolumeMin(-50)
	r.p.SetVolume(-7)
	steps := []struct {
		name string
		do   func()
	}{
		{"fade out", func() { r.p.FadeOutUntil(fadeT0.Add(100 * time.Millisecond)) }},
		{"ramp", func() { r.play(200, 32) }},
		{"cancel", r.p.CancelFade},
		{"restore", func() { r.play(100, 32) }},
		{"reset", r.p.ResetFade},
	}
	for _, s := range steps {
		s.do()
		if got := r.p.Volume(); got != -7 {
			t.Fatalf("after %s: Volume() = %v, want -7", s.name, got)
		}
	}
	(&Player{}).FadeOutUntil(fadeT0) // no sample rate yet: no division by zero
}

// The volume stage is on the audio hot path: with the real clock, a fade
// running or idle, streaming a buffer allocates nothing.
func TestFadeStageDoesNotAllocate(t *testing.T) {
	samples := make([][2]float64, 512)
	for _, tt := range []struct {
		name string
		prep func(p *Player)
	}{
		{"idle", func(*Player) {}},
		{"fading", func(p *Player) { p.FadeOutUntil(time.Now().Add(time.Hour)) }},
		{"silent", func(p *Player) { p.FadeOutUntil(time.Now()) }},
		{"restoring", func(p *Player) { p.FadeOutUntil(time.Now()); p.CancelFade() }},
		{"restored", func(p *Player) { p.ResetFade() }},
	} {
		p := &Player{sr: 44100}
		tt.prep(p)
		v := &volumeStreamer{s: ones, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN(), fade: &p.fade}
		v.Stream(samples) // pick up the command
		if allocs := testing.AllocsPerRun(1000, func() { v.Stream(samples) }); allocs != 0 {
			t.Errorf("%s: %v allocations per Stream, want 0", tt.name, allocs)
		}
	}
}

func benchStage(b *testing.B, prep func(p *Player)) {
	p := &Player{sr: 44100}
	prep(p)
	v := &volumeStreamer{s: ones, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN(), fade: &p.fade}
	samples := make([][2]float64, 512)
	b.ReportAllocs()
	for b.Loop() {
		v.Stream(samples)
	}
}

func BenchmarkVolumeStageFading(b *testing.B) {
	benchStage(b, func(p *Player) { p.FadeOutUntil(time.Now().Add(time.Hour)) })
}

func BenchmarkVolumeStageIdle(b *testing.B) { benchStage(b, func(*Player) {}) }

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
