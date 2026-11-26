package player

import (
	"time"
)

// #83: what the output device has read ahead survives a suspend.
//
// oto's mux reads the chain ahead into its player buffer (half of
// speaker.Init's buffer, up to twice over), and Context.Suspend clears none
// of it; the device plays it on resume. Only speaker.Drop (speaker.go,
// upstream's output) empties it. In the tests' model of the output and oto,
// and in a probe of oto's own mux code, that made these faults, which this
// code avoids:
//
//   - pausing silenced the chain, then suspended: silence read in between
//     was played on resume after a moment of music, then the rest. Pausing
//     now suspends the device and leaves the chain running, so what is read
//     ahead is the music's own next frames.
//   - Stop suspended with the stopped track's last frames read ahead, and
//     the next track's start played them first. Stop now lets the device
//     play them out, heard as the stop, and suspends only then, unless a
//     track has started meanwhile.
//
//   - a track started while paused, or after a pause and then Stop, began
//     with the paused track's read-ahead frames; a resume after a seek while
//     paused began with frames from before the seek. The next resume now
//     drops them (resumeSpeaker, markQueuedStale).
//
// That these are the faults the owner heard (pause stutter, a fragment of
// the previous track) is likely but not confirmed on a real device.

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
		p.resumeSpeaker(false)
		return
	}
	p.drainGen.Add(1) // a pause suspends now; a pending drain is moot
	p.suspendSpeaker(false)
	if !p.deviceSuspended() {
		// The device cannot be suspended: silence the chain, as before.
		speaker.Lock()
		p.ctrl.Paused = p.paused.Load()
		speaker.Unlock()
	}
}

// suspendAfterDrain suspends the device once it has played out what it
// holds, unless a track starts or another suspend is asked for first. Stop
// calls it with the chain silenced, under lifecycleMu.
func (p *Player) suspendAfterDrain() {
	gen := p.drainGen.Add(1)
	if p.markQueuedStale() {
		// Paused, then stopped: the device holds the paused track's
		// read-ahead frames, and the next start drops them. One hold of
		// suspendMu, so a start cannot resume between check and mark.
		return
	}
	drainAfter(p.drainDelay(), func() {
		p.lifecycleMu.Lock() // a track starting holds it from resume to playing
		defer p.lifecycleMu.Unlock()
		if p.drainGen.Load() != gen || p.playing.Load() {
			return
		}
		p.suspendSpeaker(false)
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

// markQueuedStale has the next resume drop what a suspended device holds: a
// seek while paused left frames from before the seek there. It does nothing
// while the device plays, where those frames are the seek's usual latency.
// Call it without the speaker lock. It reports whether the device was
// suspended.
func (p *Player) markQueuedStale() bool {
	p.suspendMu.Lock()
	defer p.suspendMu.Unlock()
	if p.suspended {
		p.dropQueued = true
	}
	return p.suspended
}

// dropSilently is speaker.Drop with the chain out of the mixer. Drop refills
// the device's queue from the mixer at once, and it may only read silence:
// the chain keeps running while paused (togglePause), so on a switch while
// paused it would still read the paused track. Taking the chain out leaves
// ctrl.Paused to its owners. Call it under suspendMu, without the speaker
// lock, with the device suspended: oto's queue is then full after the drop,
// and nothing reads the chain until the device resumes.
func (p *Player) dropSilently() {
	p.mu.Lock()
	ctrl := p.ctrl
	p.mu.Unlock()
	speaker.Clear()
	speakerDrop()
	if ctrl != nil {
		speaker.Play(ctrl)
	}
}
