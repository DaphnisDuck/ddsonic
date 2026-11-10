package model

// ddsonic: the sleep timer (#35). Z cycles Off, 15, 30, 45, 60 and 90
// minutes; each choice counts from the moment it is made. When the time is
// up, playback pauses: the track, its position and the queue stay, and
// Space resumes. Before that the player fades out over [ddsonic]
// sleep_fade_seconds (30 by default; 0 pauses without a fade), ending at
// the deadline. The fade is the player's own gain (player/fade_ddsonic.go):
// the user's volume never changes.
//
// The Model owns the timer and checks its deadline on the ticks the UI
// already runs: no goroutine, so nothing to lock or race. Update calls in
// through two hooks (sleepWatch before a message, sleepUpdate after it).
// The timer does not survive a restart.

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
)

// sleepPresets are the durations Z steps through, after Off.
var sleepPresets = []time.Duration{15 * time.Minute, 30 * time.Minute, 45 * time.Minute, 60 * time.Minute, 90 * time.Minute}

// sleepSeekJump is how far the position must jump during one message for
// the message to count as a seek.
const sleepSeekJump = 1500 * time.Millisecond

type sleepTimer struct {
	preset   int       // 0 is off, else 1 + an index into sleepPresets
	deadline time.Time // when playback pauses; zero when off
	fading   bool      // the player is fading out toward the deadline

	fade time.Duration    // how long the fade lasts; 0 is no fade (config)
	now  func() time.Time // the clock; nil is time.Now (tests set it)
}

// playerFader is the player's fade gain (player/fade_ddsonic.go). The UI
// asks for it rather than widening player.Engine; an engine without it
// pauses at the deadline without a fade.
type playerFader interface {
	FadeOut(d time.Duration)
	CancelFade()
	ResetFade()
}

// SetSleepFade sets the sleep timer's fade-out ([ddsonic]
// sleep_fade_seconds); 0 is no fade.
func (m *Model) SetSleepFade(d time.Duration) { m.sleep.fade = max(d, 0) }

func (m Model) sleepNow() time.Time {
	if m.sleep.now != nil {
		return m.sleep.now()
	}
	return time.Now()
}

func (m *Model) sleepFader() playerFader {
	f, _ := m.player.(playerFader)
	return f
}

// cycleSleepTimer is Z: the next preset, counted from now, or Off after the
// last. A fade under way stops, smoothly.
func (m *Model) cycleSleepTimer() {
	m.stopSleepFade()
	m.sleep.preset = (m.sleep.preset + 1) % (len(sleepPresets) + 1)
	if m.sleep.preset == 0 {
		m.sleep.deadline = time.Time{}
		m.status.Showf(statusTTLDefault, "Sleep timer off")
		return
	}
	d := sleepPresets[m.sleep.preset-1]
	m.sleep.deadline = m.sleepNow().Add(d)
	m.status.Showf(statusTTLDefault, "Sleep timer: pausing in %d min", int(d/time.Minute))
}

// stopSleepFade brings the level back over a short ramp if a fade is under
// way.
func (m *Model) stopSleepFade() {
	if !m.sleep.fading {
		return
	}
	m.sleep.fading = false
	if f := m.sleepFader(); f != nil {
		f.CancelFade()
	}
}

// clearSleep turns the timer off, keeping its configuration.
func (m *Model) clearSleep() {
	m.sleep.preset, m.sleep.deadline, m.sleep.fading = 0, time.Time{}, false
}

func (m *Model) sleepPlaying() bool {
	return m.player != nil && m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering
}

// tickSleep runs on every UI tick: it starts the fade when its time has
// come, and pauses at the deadline.
func (m *Model) tickSleep(now time.Time) {
	if m.sleep.deadline.IsZero() {
		return
	}
	if left := m.sleep.deadline.Sub(now); left > 0 {
		// Fade over what is left, so the fade ends at the deadline even when
		// the tick that starts it comes late, or the fade is longer than the
		// time that was left.
		if !m.sleep.fading && m.sleep.fade > 0 && left <= m.sleep.fade && m.sleepPlaying() {
			if f := m.sleepFader(); f != nil {
				f.FadeOut(left)
				m.sleep.fading = true
			}
		}
		return
	}
	if m.buffering {
		return // a track is starting: decide once it has, or has failed
	}
	fading := m.sleep.fading
	m.clearSleep()
	if m.sleepPlaying() {
		m.togglePlayerPause()
		m.notifyPlayback()
		m.publishIPCRuntimeState() // Update published it before the pause
		m.status.Showf(statusTTLDefault, "Sleep timer: paused")
	} else {
		m.status.Showf(statusTTLDefault, "Sleep timer ended")
	}
	if fading {
		if f := m.sleepFader(); f != nil {
			f.ResetFade() // paused or stopped: the next sound starts fresh
		}
	}
}

// sleepState is what a command can change about playback.
type sleepState struct {
	watched bool
	volume  float64
	playing bool
	paused  bool
	index   int
	path    string
	pos     time.Duration
}

func (m *Model) sleepPlaybackState() sleepState {
	track, _ := m.currentPlaybackTrack()
	return sleepState{
		watched: true,
		volume:  m.player.Volume(),
		playing: m.player.IsPlaying(),
		paused:  m.player.IsPaused(),
		index:   m.playlist.Index(),
		path:    track.Path,
		pos:     m.player.Position(),
	}
}

// sleepCommand reports whether msg is something the user did: a key, the
// mouse, or a command from MPRIS or IPC. Ticks and the results of work
// already under way (a track ending and the next starting) are not.
func sleepCommand(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.MouseMsg,
		playback.PlayPauseMsg, playback.PlayMsg, playback.PauseMsg, playback.StopMsg,
		playback.NextMsg, playback.PrevMsg, playback.SeekMsg, playback.SetPositionMsg,
		playback.SetVolumeMsg, V2RequestMsg:
		return true
	}
	return false
}

// sleepWatch runs before Update handles msg: during a fade, it notes the
// playback state a command could change.
func (m *Model) sleepWatch(msg tea.Msg) sleepState {
	if !m.sleep.fading || m.player == nil || !sleepCommand(msg) {
		return sleepState{}
	}
	return m.sleepPlaybackState()
}

// sleepUpdate runs after Update has handled msg. A tick advances the timer.
// During a fade, a command that changed the volume, paused, resumed,
// stopped, skipped or seeked cancels the fade and the timer: someone is
// awake.
func (m *Model) sleepUpdate(msg tea.Msg, was sleepState) {
	if t, ok := msg.(tickMsg); ok {
		m.tickSleep(time.Time(t))
		return
	}
	if !was.watched || !m.sleep.fading {
		return
	}
	now := m.sleepPlaybackState()
	jump := now.pos - was.pos
	if now.volume == was.volume && now.playing == was.playing && now.paused == was.paused &&
		now.index == was.index && now.path == was.path && jump < sleepSeekJump && jump > -sleepSeekJump {
		return
	}
	m.stopSleepFade()
	m.clearSleep()
	m.status.Showf(statusTTLDefault, "Sleep timer cancelled")
}

// sleepBadge is the time left, for the headers: "[Sleep 23m]", and seconds
// in the last minute. Empty when the timer is off.
func (m Model) sleepBadge() string {
	if m.sleep.deadline.IsZero() {
		return ""
	}
	left := max(m.sleep.deadline.Sub(m.sleepNow()), 0)
	if left >= time.Minute {
		return fmt.Sprintf("[Sleep %dm]", (left+time.Minute-1)/time.Minute)
	}
	return fmt.Sprintf("[Sleep %ds]", (left+time.Second-1)/time.Second)
}

// joinHeaderBadges joins the library header's badges that are present.
func joinHeaderBadges(badges ...string) string {
	out := ""
	for _, b := range badges {
		if b == "" {
			continue
		}
		if out != "" {
			out += "  "
		}
		out += b
	}
	return out
}
