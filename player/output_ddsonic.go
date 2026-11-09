package player

import (
	"time"

	"github.com/gopxl/beep/v2/speaker"
)

// #83: the output device keeps what it has read ahead across a suspend.
//
// oto's mux reads the chain ahead into its player buffer, half of
// speaker.Init's buffer, up to twice over, and Context.Suspend clears none of
// it; Beep's speaker never empties it and gives no way to. The device plays
// it when it resumes. So two things are never left there:
//
//   - silence the chain made for a pause. A pause that paused the chain and
//     then suspended the device left the music's next frames, then the
//     silence the chain made while the suspend took hold: resume played a
//     moment of music, a gap, then the rest. Pausing suspends the device and
//     leaves the chain running: what it read ahead is the music's own next
//     frames, which resume plays without a gap.
//   - the last frames of a stopped track. Stop suspended the device holding
//     them, and the next track's start played them first. Stop lets the
//     device play out what it holds, which is heard as the stop, and
//     suspends it only then, unless a track has started meanwhile.
//
// One case is left: a track started while paused. The device holds the
// paused track's next frames and plays them before the new track's; nothing
// in Beep's speaker can drop them.

// togglePause is TogglePause.
func (p *Player) togglePause() {
	speaker.Lock()
	if p.ctrl == nil {
		speaker.Unlock()
		return
	}
	pause := !p.paused.Load()
	p.paused.Store(pause)
	if !pause {
		p.ctrl.Paused = false // the fallback below may have paused the chain
	}
	speaker.Unlock()

	if !pause {
		p.resumeSpeaker()
		return
	}
	p.drainGen.Add(1) // a pause suspends now; a pending drain is moot
	p.suspendSpeaker()
	if !p.deviceSuspended() {
		// The device cannot be suspended: silence the chain, as before.
		speaker.Lock()
		p.ctrl.Paused = p.paused.Load()
		speaker.Unlock()
	}
}

// suspendAfterDrain suspends the device once it has played out what it
// holds, unless a track starts or another suspend is asked for first. Stop
// calls it with the chain silenced.
func (p *Player) suspendAfterDrain() {
	gen := p.drainGen.Add(1)
	if p.deviceSuspended() {
		return
	}
	drainAfter(p.drainDelay(), func() {
		p.lifecycleMu.Lock() // a track starting holds it from resume to playing
		defer p.lifecycleMu.Unlock()
		if p.drainGen.Load() != gen || p.playing.Load() {
			return
		}
		p.suspendSpeaker()
	})
}

// drainDelay is how long the device takes to play what it can hold: up to a
// whole buffer in oto's player buffer and half of one in the driver. Twice
// the buffer leaves a margin.
func (p *Player) drainDelay() time.Duration {
	return 2 * p.sr.D(p.speakerBufferFrames)
}

func (p *Player) deviceSuspended() bool {
	p.suspendMu.Lock()
	defer p.suspendMu.Unlock()
	return p.suspended
}
