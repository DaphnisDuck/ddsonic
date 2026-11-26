package model

// ddsonic: the sleep timer (#35). Z cycles Off, 15, 30, 45, 60 and 90
// minutes, or the presets [ddsonic] sleep_timer_presets_minutes lists
// (#204); each choice counts from the moment it is made. When the time is
// up, playback pauses: the track, its position and the queue stay, and
// Space resumes. Before that the player fades out over [ddsonic]
// sleep_fade_seconds (30 by default; 0 pauses without a fade), reaching
// silence at the deadline by the clock (player/fade_ddsonic.go). The fade
// is the player's own gain: the user's volume never changes.
//
// The Model owns the timer and checks its deadline on the ticks the UI
// already runs: no goroutine, so nothing to lock or race. During a fade, a
// volume or transport command the user gives cancels the fade and the
// timer, when it is accepted. Most are recognised where playback starts or
// stops: while Update handles a key or a remote (MPRIS, IPC, plugin)
// command, playTrack and stopPlayback call sleepUserPlayback. The commands
// that may change nothing (a volume at its limit, a Play while playing, a
// seek) call sleepCommand where they are accepted, or are recognised as they
// arrive (sleepRemoteCommand); those that start playback later (a load, a
// URL, files, an episode list) call sleepLaterPlay where they are accepted.
// A result arriving later, a refused command, automatic playback (a track
// ending and the next starting) and resuming never cancel. The timer does
// not survive a restart.

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/ui"
)

// sleepPresets are the durations Z steps through, after Off, unless the
// config lists others (SetSleepPresets).
var sleepPresets = []time.Duration{15 * time.Minute, 30 * time.Minute, 45 * time.Minute, 60 * time.Minute, 90 * time.Minute}

type sleepTimer struct {
	preset   int       // 0 is off, else 1 + an index into presets
	deadline time.Time // when playback pauses; zero when off
	fading   bool      // the player's fade gain is ramping down or held at 0
	// pausePending: the deadline passed while a track was starting; it is
	// paused, silent, as soon as the start finishes.
	pausePending bool
	// user: Update is handling a key or a remote command (sleepUserInput).
	user bool

	fade    time.Duration    // how long the fade lasts; 0 is no fade (config)
	presets []time.Duration  // what Z steps through (config); empty is sleepPresets
	now     func() time.Time // the clock; nil is time.Now (tests set it)
}

// playerFader is the player's fade gain (player/fade_ddsonic.go). The UI
// asks for it rather than widening player.Engine; an engine without it
// pauses at the deadline without a fade.
type playerFader interface {
	FadeOutUntil(end time.Time)
	CancelFade()
	ResetFade()
}

// SetSleepFade sets the sleep timer's fade-out ([ddsonic]
// sleep_fade_seconds); 0 is no fade.
func (m *Model) SetSleepFade(d time.Duration) { m.sleep.fade = max(d, 0) }

// SetSleepPresets sets the durations Z steps through ([ddsonic]
// sleep_timer_presets_minutes, validated by the config); empty keeps the
// built-in ones.
func (m *Model) SetSleepPresets(p []time.Duration) { m.sleep.presets = p }

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
	m.endSleepFade()
	presets := m.sleep.presets
	if len(presets) == 0 {
		presets = sleepPresets
	}
	m.sleep.preset = (m.sleep.preset + 1) % (len(presets) + 1)
	if m.sleep.preset == 0 {
		m.sleep.deadline = time.Time{}
		m.status.Showf(statusTTLDefault, "Sleep timer off. Press Z to set.")
		return
	}
	d := presets[m.sleep.preset-1]
	m.sleep.deadline = m.sleepNow().Add(d)
	m.status.Showf(statusTTLDefault, "Sleep timer: %d min. Press Z again for more time.", int(d/time.Minute))
}

// endSleepFade brings the level back over a short ramp if a fade is under
// way, and drops a pause waiting for a track to start.
func (m *Model) endSleepFade() {
	m.sleep.pausePending = false
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
	m.sleep.preset, m.sleep.deadline, m.sleep.fading, m.sleep.pausePending = 0, time.Time{}, false, false
}

// sleepCommand records a volume or transport command the user gave: during
// a fade (or a pause waiting at the deadline) it cancels the fade and the
// timer. Before the fade it changes nothing. Handlers call it when they
// accept the command, whatever it then does: a volume already at its
// limit, a Play while playing, a seek, a play that finishes later.
func (m *Model) sleepCommand() {
	if !m.sleep.fading && !m.sleep.pausePending {
		return
	}
	m.endSleepFade()
	m.clearSleep()
	m.status.Showf(statusTTLDefault, "Sleep timer cancelled")
}

// setVolumeByUser is a volume command from a key.
func (m *Model) setVolumeByUser(db float64) {
	m.sleepCommand()
	m.player.SetVolume(db)
}

// sleepUserPlayback is called where playback starts or stops (playTrack,
// stopPlayback): while Update handles a user's command, that is a transport
// command; otherwise it is automatic and changes nothing.
func (m *Model) sleepUserPlayback() {
	if m.sleep.user {
		m.sleepCommand()
	}
}

// sleepUserInput reports whether msg is the user's: a key, or a command from
// MPRIS, IPC or a plugin. The results of work they started, ticks and the
// player's own events are not. PluginQueueMsg is left out: of its
// operations only jump is a transport command (plugin_queue.go).
func sleepUserInput(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.KeyPressMsg,
		playback.PlayPauseMsg, playback.PlayMsg, playback.PauseMsg, playback.StopMsg,
		playback.NextMsg, playback.PrevMsg, playback.SeekMsg, playback.SetPositionMsg,
		playback.SetVolumeMsg,
		ipc.LoadMsg, ipc.QueueMsg, ipc.QueueRequestMsg, ipc.LibraryRequestMsg,
		ipc.URLRequestMsg, V2RequestMsg:
		return true
	}
	return false
}

// sleepLaterPlay accepts a command whose playback starts when its result
// arrives (a load, an episode list, files, a URL): it is a transport command
// when it asks to play now, or when nothing plays, so the result will start
// playback. The result itself never cancels.
func (m *Model) sleepLaterPlay(playNow bool) {
	if (m.sleep.fading || m.sleep.pausePending) && (playNow || !m.player.IsPlaying()) {
		m.sleepCommand()
	}
}

// libSleepCommand: Enter on a library row that plays is a transport
// command; one that opens a list is navigation.
func (m *Model) libSleepCommand(e library.Entry) {
	if e.Open == nil && (e.Track != nil || e.Play != nil) {
		m.sleepCommand()
	}
}

// sleepRemote runs before Update handles msg: it opens the user's scope
// for sleepUserPlayback (sleepTick closes it), and a remote command that
// may change nothing, or plays a URL later, is a command as it arrives.
func (m *Model) sleepRemote(msg tea.Msg) {
	m.sleep.user = sleepUserInput(msg)
	if (m.sleep.fading || m.sleep.pausePending) && m.sleepRemoteCommand(msg) {
		m.sleepCommand()
	}
}

// sleepRemoteCommand reports whether msg is an MPRIS or IPC command that
// counts even when it changes nothing: the volume, pause, play, stop, skip
// and seek. A URL load counts when the URL will play: asked to, or with
// nothing playing. Everything else that starts or stops playback is caught
// there (sleepUserPlayback).
func (m *Model) sleepRemoteCommand(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case playback.PlayPauseMsg, playback.PlayMsg, playback.PauseMsg, playback.StopMsg,
		playback.NextMsg, playback.PrevMsg, playback.SeekMsg, playback.SetPositionMsg,
		playback.SetVolumeMsg:
		return true
	case ipc.URLRequestMsg:
		return msg.Play || !m.player.IsPlaying()
	case V2RequestMsg:
		request, err := v2OperationRequest(msg.Request)
		if err != nil {
			return false
		}
		switch request.Cmd {
		case "play", "pause", "toggle", "stop", "next", "prev",
			"volume", "volume.adjust", "seek", "seek.absolute":
			return true
		case "url.load":
			return request.Play || !m.player.IsPlaying()
		}
	}
	return false
}

func (m *Model) sleepPlaying() bool {
	return m.player != nil && m.player.IsPlaying() && !m.player.IsPaused() && !m.buffering
}

// sleepTick runs after Update has handled msg: it closes the user's scope,
// and on a UI tick it starts the fade when its time has come, and pauses at
// the deadline.
func (m *Model) sleepTick(msg tea.Msg) {
	m.sleep.user = false
	if t, ok := msg.(tickMsg); ok {
		m.tickSleep(time.Time(t))
	}
}

func (m *Model) tickSleep(now time.Time) {
	if m.sleep.pausePending {
		if !m.buffering {
			m.finishSleep()
		}
		return
	}
	if m.sleep.deadline.IsZero() {
		return
	}
	if left := m.sleep.deadline.Sub(now); left > 0 {
		// The fade ends at the deadline by the clock, so a late tick, or a
		// fade longer than the time left, shortens it rather than moving
		// its end.
		if !m.sleep.fading && m.sleep.fade > 0 && left <= m.sleep.fade && m.sleepPlaying() {
			if f := m.sleepFader(); f != nil {
				f.FadeOutUntil(m.sleep.deadline)
				m.sleep.fading = true
			}
		}
		return
	}
	if m.buffering {
		// A track is starting. Hold the output silent from now (with a
		// fade it already is), and pause as soon as the start finishes:
		// the new track is never heard.
		if f := m.sleepFader(); f != nil && !m.sleep.fading {
			f.FadeOutUntil(now)
			m.sleep.fading = true
		}
		m.sleep.preset, m.sleep.deadline, m.sleep.pausePending = 0, time.Time{}, true
		return
	}
	m.finishSleep()
}

// finishSleep is the deadline: pause what is playing, end the timer, and
// put the level back for when playback resumes.
func (m *Model) finishSleep() {
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

// sleepSepHeader is sepHeader for the library's header line with the
// sleep timer's countdown kept whole at its end: the rest of the label is
// shortened first (Unicode-aware, with "…") to leave it room.
func (m Model) sleepSepHeader(label string) string {
	badge := m.sleepBadge()
	if badge == "" {
		return sepHeader(label)
	}
	// "── " + label + "  " + badge + " ", as labeledSeparator draws it.
	room := ui.PanelWidth - lipgloss.Width("── ") - 2 - lipgloss.Width(badge) - 1
	if room <= 0 {
		return sepHeader(badge)
	}
	return sepHeader(truncate(label, room) + "  " + badge)
}

// sepLabelN is sepHeaderN's label: the position counter after the label.
func sepLabelN(label string, pos, total int) string {
	if total <= 0 {
		return label
	}
	return fmt.Sprintf("%s  %d/%d", label, pos, total)
}
