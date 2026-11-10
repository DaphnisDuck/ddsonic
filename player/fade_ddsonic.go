// ddsonic: a fade gain, applied in the volume stage after the user's volume
// (#35, the sleep timer). It is separate from the volume: fading never
// changes Volume(), so the volume shown in the UI, sent over MPRIS and IPC,
// and saved stays what the user set.
//
// A fade is anchored to wall time, not to how many samples have played: the
// UI names the moment the fade must end, and each buffer's gain comes from
// the clock when the buffer is processed, sample by sample. A stalled
// decoder, a late command or a slow tick cannot make the fade end late: any
// sample processed at or after the end is multiplied by exactly the target
// gain. What the guarantee covers is processing time; the audio device
// plays a buffer some milliseconds later (its own buffer latency).
//
// Cost on the audio path: with no fade, one atomic load and a branch per
// buffer; during a ramp, a clock read per buffer and a few float operations
// per sample. It never allocates and takes no lock.

package player

import (
	"sync/atomic"
	"time"
)

// cancelFadeRamp is how long CancelFade takes to bring the gain back to
// normal: short enough to feel immediate, long enough not to click.
const cancelFadeRamp = 50 * time.Millisecond

// fadeCmd is one fade command: ramp the gain from wherever it is to `to`,
// reaching it at `end` (wall time). Commands are never changed once sent.
type fadeCmd struct {
	to         float64
	start, end time.Time     // the ramp; end <= start is at once
	step       time.Duration // one sample's duration at the output rate
}

// fadeGain is the fade stage. Its zero value is unity gain.
type fadeGain struct {
	cmd atomic.Pointer[fadeCmd] // the latest command, written by the UI
	now func() time.Time        // the clock; nil is time.Now (tests set it)

	// The audio goroutine's own state.
	applied *fadeCmd // the command in effect; nil is unity
	from    float64  // the gain when applied arrived
	settled bool     // applied has reached `to` and `to` is unity
}

func (f *fadeGain) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// gainAt is the gain under the applied command `left` before its end.
func (f *fadeGain) gainAt(left time.Duration) float64 {
	c := f.applied
	if c == nil {
		return 1
	}
	span := c.end.Sub(c.start)
	if left <= 0 || span <= 0 {
		return c.to
	}
	if left >= span {
		return f.from
	}
	// x runs from 1 to 0 over the ramp. The square makes the fall even to
	// the ear: most of the drop in level comes early, and the end is a
	// gentle tail into silence rather than a sudden cut.
	x := float64(left) / float64(span)
	return c.to + (f.from-c.to)*x*x
}

// apply multiplies samples by the fade gain. It runs on the audio
// goroutine. A nil fadeGain, or one at unity, leaves samples alone.
func (f *fadeGain) apply(samples [][2]float64) {
	if f == nil {
		return
	}
	c := f.cmd.Load()
	if c == f.applied && (c == nil || f.settled) {
		return
	}
	now := f.clock()
	if c != f.applied {
		f.from = f.gainAt(f.leftAt(now))
		f.applied, f.settled = c, false
	}
	left := c.end.Sub(now)
	for i := range samples {
		g := f.gainAt(left - time.Duration(i)*c.step)
		samples[i][0] *= g
		samples[i][1] *= g
	}
	f.settled = c.to == 1 && left-time.Duration(len(samples))*c.step <= 0
}

// leftAt is how long before the applied command's end `now` is.
func (f *fadeGain) leftAt(now time.Time) time.Duration {
	if f.applied == nil {
		return 0
	}
	return f.applied.end.Sub(now)
}

// send hands the audio goroutine a new command.
func (f *fadeGain) send(to float64, start, end time.Time, step time.Duration) {
	f.cmd.Store(&fadeCmd{to: to, start: start, end: end, step: step})
}

// FadeOutUntil ramps the output down from the level it is at now to
// silence at end, measured by the clock. At and after end the output is
// silent until the fade is cancelled or reset, including after a pause and
// a resume. The user's volume is not changed.
func (p *Player) FadeOutUntil(end time.Time) {
	now := p.fade.clock()
	p.fade.send(0, now, end, p.sampleStep())
}

// CancelFade brings the output back to normal over a short ramp.
func (p *Player) CancelFade() {
	now := p.fade.clock()
	p.fade.send(1, now, now.Add(cancelFadeRamp), p.sampleStep())
}

// ResetFade brings the output back to normal at once: for when playback is
// paused or stopped, and the next sound is a fresh start anyway.
func (p *Player) ResetFade() {
	now := p.fade.clock()
	p.fade.send(1, now, now, p.sampleStep())
}

// sampleStep is one sample's duration at the output rate.
func (p *Player) sampleStep() time.Duration {
	if p.sr <= 0 {
		return 0
	}
	return p.sr.D(1)
}
