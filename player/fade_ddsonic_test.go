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

// fadeStage is a volume stage at 0 dB with a fade, as the player builds it.
func fadeStage(f *fadeGain) *volumeStreamer {
	var vol atomic.Uint64 // 0 dB
	return &volumeStreamer{s: ones, vol: &vol, mono: new(atomic.Bool), cachedDB: math.NaN(), fade: f}
}

// run streams n samples through v in buffers of size buf and returns the
// left channel of each.
func run(v *volumeStreamer, n, buf int) []float64 {
	out := make([]float64, 0, n)
	samples := make([][2]float64, buf)
	for len(out) < n {
		k, _ := v.Stream(samples[:min(buf, n-len(out))])
		for _, s := range samples[:k] {
			out = append(out, s[0])
		}
	}
	return out
}

func TestFadeOutRampsToExactSilence(t *testing.T) {
	const total = 1000
	for _, buf := range []int{1, 7, 512, 4096} {
		f := &fadeGain{}
		f.send(0, total)
		got := run(fadeStage(f), total+300, buf)
		if got[0] != 1 {
			t.Errorf("buf %d: first sample %v, want 1 (the ramp starts where the level is)", buf, got[0])
		}
		for i := 1; i < len(got); i++ {
			if got[i] > got[i-1] {
				t.Fatalf("buf %d: sample %d rose from %v to %v", buf, i, got[i-1], got[i])
			}
		}
		if got[total-1] <= 0 {
			t.Errorf("buf %d: silent before the ramp's end (sample %d = %v)", buf, total-1, got[total-1])
		}
		for i := total; i < len(got); i++ {
			if got[i] != 0 {
				t.Fatalf("buf %d: sample %d = %v after the ramp, want exactly 0", buf, i, got[i])
			}
		}
		if !f.silent.Load() {
			t.Errorf("buf %d: silent = false after the ramp", buf)
		}
	}
}

func TestCancelFadeRestoresTheLevel(t *testing.T) {
	const total, back = 1000, 100
	tests := []struct {
		name  string
		after int // samples of fade-out before the cancel
	}{
		{"after silence", total + 50},
		{"mid-fade", total / 2},
		{"at once", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fadeGain{}
			v := fadeStage(f)
			f.send(0, total)
			faded := run(v, tt.after, 64)
			level := 1.0
			if len(faded) > 0 {
				level = faded[len(faded)-1]
				if level >= 1 {
					t.Fatalf("setup: %d samples of fade left the level at %v", tt.after, level)
				}
			}
			f.send(1, back)
			got := run(v, back+200, 64)
			if math.Abs(got[0]-level) > 0.01 {
				t.Errorf("cancel jumped the level from %v to %v: the restore starts where the fade was", level, got[0])
			}
			for i := 1; i < len(got); i++ {
				if got[i] < got[i-1] {
					t.Fatalf("sample %d fell from %v to %v while restoring", i, got[i-1], got[i])
				}
			}
			for i := back; i < len(got); i++ {
				if got[i] != 1 {
					t.Fatalf("sample %d = %v after the restore ramp, want exactly 1", i, got[i])
				}
			}
			if f.silent.Load() {
				t.Error("silent = true after a cancel")
			}
		})
	}
}

// With no fade command, or after a reset, the stage is unity: the output is
// exactly the volume stage's, as before the fade existed.
func TestFadeIdleHasNoEffect(t *testing.T) {
	tests := []struct {
		name string
		f    *fadeGain
		prep func(f *fadeGain)
	}{
		{"no fade stage", nil, nil},
		{"never used", &fadeGain{}, nil},
		{"reset", &fadeGain{}, func(f *fadeGain) { f.send(0, 10); f.send(1, 0) }},
		{"reset after silence", &fadeGain{}, func(f *fadeGain) {
			f.send(0, 10)
			f.apply(make([][2]float64, 20))
			f.send(1, 0)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prep != nil {
				tt.prep(tt.f)
			}
			v := fadeStage(tt.f)
			v.vol.Store(math.Float64bits(-6))
			want := math.Pow(10, -6.0/20)
			for i, got := range run(v, 300, 64) {
				if got != want {
					t.Fatalf("sample %d = %v, want %v (the volume's gain alone)", i, got, want)
				}
			}
		})
	}
}

// The fade never touches the user's volume: Volume() before, during and
// after a fade, a cancel and a reset is what the user set.
func TestFadeLeavesTheUserVolumeAlone(t *testing.T) {
	p := &Player{sr: 44100}
	p.SetVolumeMin(-50)
	p.SetVolume(-7)
	v := &volumeStreamer{s: ones, vol: &p.volume, mono: &p.mono, cachedDB: math.NaN(), fade: &p.fade}
	steps := []struct {
		name string
		do   func()
	}{
		{"fade out", func() { p.FadeOut(10 * time.Millisecond) }},
		{"ramp", func() { run(v, 1000, 128) }},
		{"cancel", p.CancelFade},
		{"restore", func() { run(v, 5000, 128) }},
		{"fade again", func() { p.FadeOut(time.Second); run(v, 100, 100) }},
		{"reset", p.ResetFade},
	}
	for _, s := range steps {
		s.do()
		if got := p.Volume(); got != -7 {
			t.Fatalf("after %s: Volume() = %v, want -7", s.name, got)
		}
	}
	if p.FadeSilent() {
		t.Error("FadeSilent after a reset")
	}
}

func TestPlayerFadeLengthsFollowTheSampleRate(t *testing.T) {
	p := &Player{sr: 48000}
	p.FadeOut(30 * time.Second)
	if c := p.fade.cmd.Load(); c.to != 0 || c.total != 30*48000 {
		t.Errorf("FadeOut(30s) = %+v, want to 0 over 1440000 samples", *c)
	}
	p.CancelFade()
	if c := p.fade.cmd.Load(); c.to != 1 || c.total != 48000/20 {
		t.Errorf("CancelFade = %+v, want to 1 over 2400 samples (50ms)", *c)
	}
	p.ResetFade()
	if c := p.fade.cmd.Load(); c.to != 1 || c.total != 0 {
		t.Errorf("ResetFade = %+v, want to 1 at once", *c)
	}
}

// The volume stage is on the audio hot path: with a fade running, or idle,
// streaming a buffer allocates nothing.
func TestFadeStageDoesNotAllocate(t *testing.T) {
	samples := make([][2]float64, 512)
	for _, tt := range []struct {
		name string
		prep func(f *fadeGain)
	}{
		{"idle", func(*fadeGain) {}},
		{"fading", func(f *fadeGain) { f.send(0, 1<<40) }},
		{"silent", func(f *fadeGain) { f.send(0, 1) }},
		{"restoring", func(f *fadeGain) { f.send(0, 1); f.apply(samples); f.send(1, 1<<40) }},
	} {
		f := &fadeGain{}
		tt.prep(f)
		v := fadeStage(f)
		v.Stream(samples) // take up the command
		if allocs := testing.AllocsPerRun(1000, func() { v.Stream(samples) }); allocs != 0 {
			t.Errorf("%s: %v allocations per Stream, want 0", tt.name, allocs)
		}
	}
}

func BenchmarkVolumeStageFading(b *testing.B) {
	f := &fadeGain{}
	f.send(0, 1<<40)
	v := fadeStage(f)
	samples := make([][2]float64, 512)
	b.ReportAllocs()
	for b.Loop() {
		v.Stream(samples)
	}
}

func BenchmarkVolumeStageIdle(b *testing.B) {
	v := fadeStage(&fadeGain{})
	samples := make([][2]float64, 512)
	b.ReportAllocs()
	for b.Loop() {
		v.Stream(samples)
	}
}
