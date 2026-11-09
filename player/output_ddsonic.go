package player

import (
	"time"

	"github.com/gopxl/beep/v2/speaker"
)

// #83: what the output device has read ahead survives a suspend.
//
// oto's mux reads the chain ahead into its player buffer (half of
// speaker.Init's buffer, up to twice over), Context.Suspend clears none of
// it, and Beep's speaker never empties it and gives no way to; the device
// plays it on resume. In the tests' model of Beep and oto, and in a probe of
// oto's own mux code, that made two faults, which this code avoids:
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
// That these are the faults the owner heard (pause stutter, a fragment of
// the previous track) is likely but not confirmed on a real device.
//
// Not fixed: a track started while paused begins with the paused track's
// read-ahead frames. Beep's speaker cannot drop them (#83 stays open).

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
