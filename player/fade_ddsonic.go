// ddsonic: a fade gain, applied in the volume stage after the user's volume
// (#35, the sleep timer). It is separate from the volume: fading never
// changes Volume(), so the volume shown in the UI, sent over MPRIS and IPC,
// and saved stays what the user set.
//
// Time in the fade is an audio-time cursor: the instant of the next sample
// to be processed. It advances by one sample's duration per sample, so the
// gain is smooth across buffers however the audio thread pulls them (in
// bursts ahead of real time, or with the clock not moving between pulls).
// The cursor is reconciled with the wall clock only forward: when the
// clock is ahead of it (a stall, a pause, the first buffer), it jumps to
// the clock; it never moves back. So a fade-out still reaches exactly zero
// for every sample processed at or after its end by the clock, and may
// reach it a little earlier when audio is read ahead, never later. The
// guarantee is about processing time; the audio device plays a buffer some
// milliseconds after it is processed (its own latency).
//
// A new command takes effect at the cursor where the audio thread picks it
// up: its ramp starts there, from the gain the previous command had there,
// so a change never steps the level. A fade-out ends at the instant it
// names; a cancel ramps back to normal over cancelFadeRamp from pickup.
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

// fadeCmd is one fade command: ramp the gain to `to`, from wherever it is
// when the audio thread picks the command up. The ramp ends at `end` (wall
// time) when end is set, else `ramp` after pickup. Commands are never
// changed once sent.
type fadeCmd struct {
	to   float64
	end  time.Time
	ramp time.Duration
	step time.Duration // one sample's duration at the output rate
}

// fadeGain is the fade stage. Its zero value is unity gain.
type fadeGain struct {
	cmd atomic.Pointer[fadeCmd] // the latest command, written by the UI
	now func() time.Time        // the clock; nil is time.Now (tests set it)

	// The audio goroutine's own state.
	applied    *fadeCmd  // the command in effect; nil is unity
	from, to   float64   // the ramp's gains
	start, end time.Time // the ramp, in audio time
	cursor     time.Time // the next sample's instant; never moves back
	settled    bool      // the ramp has reached `to` and `to` is unity
}

func (f *fadeGain) clock() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

// gain is the gain `left` before the ramp's end.
func (f *fadeGain) gain(left time.Duration) float64 {
	if f.applied == nil {
		return 1
	}
	span := f.end.Sub(f.start)
	if left <= 0 || span <= 0 {
		return f.to
	}
	if left >= span {
		return f.from
	}
	// x runs from 1 to 0 over the ramp. The square makes the fall even to
	// the ear: most of the drop in level comes early, and the end is a
	// gentle tail into silence rather than a sudden cut.
	x := float64(left) / float64(span)
	return f.to + (f.from-f.to)*x*x
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
	if now := f.clock(); now.After(f.cursor) {
		f.cursor = now // forward only: after a stall, a pause, or at first
	}
	if c != f.applied {
		f.from = f.gain(f.end.Sub(f.cursor))
		f.applied, f.to, f.start, f.settled = c, c.to, f.cursor, false
		f.end = c.end
		if c.end.IsZero() {
			f.end = f.cursor.Add(c.ramp)
		}
	}
	left := f.end.Sub(f.cursor)
	for i := range samples {
		g := f.gain(left - time.Duration(i)*c.step)
		samples[i][0] *= g
		samples[i][1] *= g
	}
	advance := time.Duration(len(samples)) * c.step
	f.cursor = f.cursor.Add(advance)
	f.settled = f.to == 1 && left-advance <= 0
}

// send hands the audio goroutine a new command.
func (f *fadeGain) send(c fadeCmd) { f.cmd.Store(&c) }

// FadeOutUntil ramps the output down, from the level it is at, to silence
// at end by the clock. From end on the output is silent until the fade is
// cancelled or reset, including after a pause and a resume. The user's
// volume is not changed.
func (p *Player) FadeOutUntil(end time.Time) {
	p.fade.send(fadeCmd{to: 0, end: end, step: p.sampleStep()})
}

// CancelFade brings the output back to normal over cancelFadeRamp, from
// the level it is at when the audio thread picks the command up.
func (p *Player) CancelFade() {
	p.fade.send(fadeCmd{to: 1, ramp: cancelFadeRamp, step: p.sampleStep()})
}

// ResetFade brings the output back to normal at once: for when playback is
// paused or stopped, and the next sound is a fresh start anyway.
func (p *Player) ResetFade() {
	p.fade.send(fadeCmd{to: 1, step: p.sampleStep()})
}

// sampleStep is one sample's duration at the output rate.
func (p *Player) sampleStep() time.Duration {
	if p.sr <= 0 {
		return 0
	}
	return p.sr.D(1)
}
