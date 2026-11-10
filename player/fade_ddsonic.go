// ddsonic: a fade gain, applied in the volume stage after the user's volume
// (#35, the sleep timer). It is separate from the volume: fading never
// changes Volume(), so the volume shown in the UI, sent over MPRIS and IPC,
// and saved stays what the user set.
//
// The UI sends commands (FadeOut, CancelFade, ResetFade); the audio
// goroutine ramps the gain one sample at a time, so the ramp is smooth at
// any UI tick rate. Applying it never allocates.

package player

import (
	"sync/atomic"
	"time"
)

// cancelFadeRamp is how long CancelFade takes to bring the gain back to
// normal: short enough to feel immediate, long enough not to click.
const cancelFadeRamp = 50 * time.Millisecond

// fadeCmd is one fade command: ramp the gain from wherever it is to `to`
// over `total` samples (0: at once). Commands are never changed once sent.
type fadeCmd struct {
	to    float64
	total int64
}

// fadeGain is the fade stage. Its zero value is unity gain.
type fadeGain struct {
	cmd atomic.Pointer[fadeCmd] // the latest command, written by the UI

	// The audio goroutine's own state.
	applied *fadeCmd // the command being ramped; nil is unity
	from    float64  // the gain when applied arrived
	pos     int64    // samples ramped under applied

	silent atomic.Bool // the gain has reached 0 and stays there
}

// gain is the gain at the current sample.
func (f *fadeGain) gain() float64 {
	c := f.applied
	if c == nil {
		return 1
	}
	if f.pos >= c.total {
		return c.to
	}
	// x runs from 1 to 0 over the ramp. The square makes the fall even to
	// the ear: most of the drop in level comes early, and the end is a
	// gentle tail into silence rather than a sudden cut.
	x := float64(c.total-f.pos) / float64(c.total)
	return c.to + (f.from-c.to)*x*x
}

// apply multiplies samples by the fade gain, advancing the ramp. It runs on
// the audio goroutine. A nil fadeGain, or one at unity, leaves samples alone.
func (f *fadeGain) apply(samples [][2]float64) {
	if f == nil {
		return
	}
	if c := f.cmd.Load(); c != f.applied {
		f.from = f.gain()
		f.applied, f.pos = c, 0
	}
	c := f.applied
	if c == nil || (f.pos >= c.total && c.to == 1) {
		return
	}
	for i := range samples {
		g := f.gain()
		samples[i][0] *= g
		samples[i][1] *= g
		if f.pos < c.total {
			f.pos++
		}
	}
	f.silent.Store(c.to == 0 && f.pos >= c.total)
}

// send hands the audio goroutine a new command.
func (f *fadeGain) send(to float64, total int64) {
	f.silent.Store(false)
	f.cmd.Store(&fadeCmd{to: to, total: max(total, 0)})
}

// FadeOut ramps the output down to silence over d, from the level it is at.
// The user's volume is not changed. Nothing is heard until the fade is
// cancelled or reset, including after a pause and a resume.
func (p *Player) FadeOut(d time.Duration) {
	p.fade.send(0, int64(p.sr.N(d)))
}

// CancelFade brings the output back to normal over a short ramp.
func (p *Player) CancelFade() {
	p.fade.send(1, int64(p.sr.N(cancelFadeRamp)))
}

// ResetFade brings the output back to normal at once: for when playback is
// paused or stopped, and the next sound is a fresh start anyway.
func (p *Player) ResetFade() {
	p.fade.send(1, 0)
}

// FadeSilent reports whether a FadeOut has reached silence.
func (p *Player) FadeSilent() bool {
	return p.fade.silent.Load()
}
