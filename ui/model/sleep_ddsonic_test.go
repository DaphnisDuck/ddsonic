package model

// ddsonic: the sleep timer (sleep_ddsonic.go, #35).

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/playlist"
)

// sleepEngine is the playback fake with a volume the keys can change and
// the player's fade commands, recorded.
type sleepEngine struct {
	*playbackFakeEngine
	volume         float64
	setVolumeCalls int
	fades          []string
}

func (e *sleepEngine) SetVolume(db float64) { e.volume = db; e.setVolumeCalls++ }
func (e *sleepEngine) Volume() float64      { return e.volume }
func (e *sleepEngine) FadeOut(d time.Duration) {
	e.fades = append(e.fades, "out "+d.String())
}
func (e *sleepEngine) CancelFade() { e.fades = append(e.fades, "cancel") }

// Seek moves the position, as the player does.
func (e *sleepEngine) Seek(d time.Duration) error {
	e.position += d
	return e.playbackFakeEngine.Seek(d)
}
func (e *sleepEngine) ResetFade() { e.fades = append(e.fades, "reset") }

var sleepT0 = time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)

// sleepRig is a model playing track 2 of 3, a settable clock, and the
// engine.
type sleepRig struct {
	t   *testing.T
	m   Model
	e   *sleepEngine
	now time.Time
}

func newSleepRig(t *testing.T, fade time.Duration) *sleepRig {
	t.Helper()
	r := &sleepRig{t: t, now: sleepT0}
	r.e = &sleepEngine{playbackFakeEngine: &playbackFakeEngine{playing: true, position: 42 * time.Second}, volume: -7}
	r.m = keybindingTestModel()
	r.m.player = r.e
	r.m.plVisible = 10
	r.m.playlist.Replace(tracksOf("a", "b", "c"))
	r.m.playlist.SetIndex(1)
	r.m.SetSleepFade(fade)
	r.m.sleep.now = func() time.Time { return r.now }
	return r
}

func (r *sleepRig) send(msg tea.Msg) {
	r.t.Helper()
	updated, _ := r.m.Update(msg)
	r.m = updated.(Model)
}

func (r *sleepRig) key(k string) { r.t.Helper(); r.send(libKey(k)) }

// tick moves the clock to t0+d and delivers the UI tick for that moment.
func (r *sleepRig) tick(d time.Duration) {
	r.t.Helper()
	r.now = sleepT0.Add(d)
	r.send(tickMsg(r.now))
}

// press presses Z n times.
func (r *sleepRig) press(n int) {
	r.t.Helper()
	for range n {
		r.key("Z")
	}
}

func (r *sleepRig) armed() bool { return !r.m.sleep.deadline.IsZero() }

func TestSleepKeyCyclesPresetsFromNow(t *testing.T) {
	r := newSleepRig(t, 30*time.Second)
	want := []struct {
		after  time.Duration // clock when Z is pressed
		left   time.Duration // 0: off
		status string
	}{
		{0, 15 * time.Minute, "Sleep timer: pausing in 15 min"},
		{2 * time.Minute, 30 * time.Minute, "Sleep timer: pausing in 30 min"},
		{3 * time.Minute, 45 * time.Minute, "Sleep timer: pausing in 45 min"},
		{3 * time.Minute, 60 * time.Minute, "Sleep timer: pausing in 60 min"},
		{10 * time.Minute, 90 * time.Minute, "Sleep timer: pausing in 90 min"},
		{11 * time.Minute, 0, "Sleep timer off"},
		{12 * time.Minute, 15 * time.Minute, "Sleep timer: pausing in 15 min"},
	}
	for i, w := range want {
		r.now = sleepT0.Add(w.after)
		r.key("Z")
		if w.left == 0 {
			if r.armed() {
				t.Errorf("press %d: deadline %v, want off", i+1, r.m.sleep.deadline)
			}
		} else if got := r.m.sleep.deadline.Sub(r.now); got != w.left {
			t.Errorf("press %d: deadline in %v, want %v from the press", i+1, got, w.left)
		}
		if r.m.status.text != w.status {
			t.Errorf("press %d: status %q, want %q", i+1, r.m.status.text, w.status)
		}
	}
	if r.e.paused || r.e.stopCalls != 0 || len(r.e.fades) != 0 {
		t.Errorf("Z changed playback: paused %v, stops %d, fades %q", r.e.paused, r.e.stopCalls, r.e.fades)
	}
}

func TestSleepBadge(t *testing.T) {
	tests := []struct {
		left time.Duration
		want string
	}{
		{90 * time.Minute, "[Sleep 90m]"},
		{22*time.Minute + 10*time.Second, "[Sleep 23m]"},
		{time.Minute, "[Sleep 1m]"},
		{59 * time.Second, "[Sleep 59s]"},
		{200 * time.Millisecond, "[Sleep 1s]"},
		{-time.Second, "[Sleep 0s]"},
	}
	for _, tt := range tests {
		r := newSleepRig(t, 0)
		r.m.sleep.deadline = sleepT0.Add(tt.left)
		if got := r.m.sleepBadge(); got != tt.want {
			t.Errorf("%v left: badge %q, want %q", tt.left, got, tt.want)
		}
	}
	if got := newSleepRig(t, 0).m.sleepBadge(); got != "" {
		t.Errorf("off: badge %q, want none", got)
	}
}

// Z works and the badge shows in the classic queue screen, the library, and
// the library's queue; in the library's search input Z is a letter.
func TestSleepKeyAndBadgeInBothLayouts(t *testing.T) {
	header := func(m Model) string {
		if m.libraryVisible() {
			return ansi.Strip(m.libHeaderLine())
		}
		return ansi.Strip(m.renderPlaybackHeader())
	}
	layouts := []struct {
		name  string
		model func(t *testing.T) Model
	}{
		{"classic", func(*testing.T) Model {
			m := keybindingTestModel()
			m.playlist.Replace(tracksOf("a"))
			return m
		}},
		{"library", func(*testing.T) Model { return newLibraryModel(testLibraryRoot()) }},
		{"library queue", func(t *testing.T) Model { m, _ := newQueueModel(t); return m }},
		{"library search results", func(t *testing.T) Model {
			m, _ := newSearchModel(t)
			m = typeText(t, libPress(t, m, "/"), "mahler")
			m = libPress(t, m, "enter")
			if m.lib.searchInput {
				t.Fatal("setup: still typing")
			}
			return m
		}},
	}
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			m := l.model(t)
			m.sleep.now = func() time.Time { return sleepT0 }
			if strings.Contains(header(m), "Sleep") {
				t.Fatalf("badge before Z: %q", header(m))
			}
			updated, _ := m.Update(libKey("Z"))
			m = updated.(Model)
			if m.sleep.deadline != sleepT0.Add(15*time.Minute) {
				t.Fatalf("Z did not arm the timer (deadline %v)", m.sleep.deadline)
			}
			if h := header(m); !strings.Contains(h, "[Sleep 15m]") {
				t.Errorf("header %q, want [Sleep 15m]", h)
			}
			if bar := ansi.Strip(strings.Join(m.libKeyBarLines(200), " ")); m.libraryEnabled() && !strings.Contains(bar, "Z  Sleep") {
				t.Errorf("the key bar does not list Z: %q", bar)
			}
		})
	}
	t.Run("library search input", func(t *testing.T) {
		m, _ := newSearchModel(t)
		m = typeText(t, libPress(t, m, "/"), "aZ")
		if !m.sleep.deadline.IsZero() {
			t.Error("Z typed in the search input armed the timer")
		}
		if sl, _ := m.libSearchLevel(); sl.Query() != "aZ" {
			t.Errorf("query %q, want Z typed into it", sl.Query())
		}
	})
}

// At the deadline playback pauses, with or without a fade: the track, its
// position and the queue stay, and Space resumes.
func TestSleepExpiryPausesAndSpaceResumes(t *testing.T) {
	for _, fade := range []time.Duration{0, 30 * time.Second} {
		t.Run("fade "+fade.String(), func(t *testing.T) {
			r := newSleepRig(t, fade)
			r.m.playlist.Queue(2)
			r.press(1)
			r.tick(14*time.Minute + 50*time.Second)
			if r.e.paused {
				t.Fatal("paused before the deadline")
			}
			r.tick(15 * time.Minute)
			if !r.e.paused || !r.e.playing {
				t.Fatalf("at the deadline: playing %v, paused %v; want paused", r.e.playing, r.e.paused)
			}
			if r.armed() || r.m.sleepBadge() != "" {
				t.Error("timer still set after it fired")
			}
			if r.m.status.text != "Sleep timer: paused" {
				t.Errorf("status %q", r.m.status.text)
			}
			if r.m.playlist.Index() != 1 || r.m.playlist.Len() != 3 || r.m.playlist.QueueLen() != 1 {
				t.Errorf("queue changed: index %d, %d tracks, %d queued", r.m.playlist.Index(), r.m.playlist.Len(), r.m.playlist.QueueLen())
			}
			if r.e.stopCalls != 0 || len(r.e.playCalls) != 0 || len(r.e.seekCalls) != 0 || r.e.position != 42*time.Second {
				t.Errorf("playback was restarted or moved: stops %d, plays %q, seeks %v", r.e.stopCalls, r.e.playCalls, r.e.seekCalls)
			}
			wantFades := []string(nil)
			if fade > 0 {
				wantFades = []string{"out 10s", "reset"} // the first tick in the fade's window
			}
			if !slices.Equal(r.e.fades, wantFades) {
				t.Errorf("fades %q, want %q", r.e.fades, wantFades)
			}
			r.key("space")
			if r.e.paused {
				t.Error("Space did not resume")
			}
			r.tick(40 * time.Minute)
			if r.e.paused {
				t.Error("paused again after the timer fired")
			}
		})
	}
}

// The fade is timed to end at the deadline: it starts on the first tick in
// its window and lasts what is left, however late that tick is.
func TestSleepFadeEndsAtTheDeadline(t *testing.T) {
	tests := []struct {
		name  string
		fade  time.Duration
		ticks []time.Duration
		want  []string
	}{
		{"on time", 30 * time.Second, []time.Duration{14*time.Minute + 29*time.Second, 14*time.Minute + 30*time.Second, 14*time.Minute + 45*time.Second}, []string{"out 30s"}},
		{"a late tick", 30 * time.Second, []time.Duration{14*time.Minute + 30*time.Second + 400*time.Millisecond}, []string{"out 29.6s"}},
		{"longer than the timer", 20 * time.Minute, []time.Duration{time.Second}, []string{"out 14m59s"}},
		{"no fade", 0, []time.Duration{14*time.Minute + 59*time.Second}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, tt.fade)
			r.press(1)
			for _, d := range tt.ticks {
				r.tick(d)
			}
			if !slices.Equal(r.e.fades, tt.want) {
				t.Errorf("fades %q, want %q", r.e.fades, tt.want)
			}
			if r.e.paused {
				t.Error("paused before the deadline")
			}
		})
	}
}

// Off, or another preset, during the fade brings the level back and keeps
// playing; the old deadline no longer applies.
func TestSleepZDuringTheFade(t *testing.T) {
	tests := []struct {
		name    string
		presses int // presses during the fade
		armed   bool
	}{
		{"off", 1, false},
		{"another preset", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			deadline := 90 * time.Minute
			r.press(5) // 90 min: the next press is Off
			if !tt.armed {
				r.tick(deadline - 20*time.Second)
			} else {
				r.press(2) // Off, then 15 min
				deadline = 15 * time.Minute
				r.tick(deadline - 20*time.Second)
			}
			if !r.m.sleep.fading {
				t.Fatal("setup: not fading")
			}
			r.press(tt.presses)
			if r.m.sleep.fading || !slices.Equal(r.e.fades, []string{"out 20s", "cancel"}) {
				t.Errorf("fading %v, fades %q; want the fade cancelled", r.m.sleep.fading, r.e.fades)
			}
			if r.armed() != tt.armed {
				t.Errorf("armed %v, want %v", r.armed(), tt.armed)
			}
			r.tick(deadline + time.Second)
			if r.e.paused {
				t.Error("paused at the old deadline")
			}
		})
	}
}

// During the fade, a volume or transport command, from a key, MPRIS or IPC,
// cancels the fade and the timer. Other keys and the player's own track
// changes do not.
func TestSleepCommandDuringTheFade(t *testing.T) {
	tests := []struct {
		name    string
		msg     tea.Msg
		cancels bool
	}{
		{"volume up", libKey("+"), true},
		{"volume down", libKey("-"), true},
		{"pause key", libKey("space"), true},
		{"stop key", libKey("s"), true},
		{"next key", libKey(">"), true},
		{"previous key", libKey("<"), true},
		{"seek key", tea.KeyPressMsg{Code: tea.KeyRight}, true},
		{"MPRIS pause", playback.PauseMsg{}, true},
		{"MPRIS play/pause", playback.PlayPauseMsg{}, true},
		{"MPRIS stop", playback.StopMsg{}, true},
		{"MPRIS next", playback.NextMsg{}, true},
		{"MPRIS previous", playback.PrevMsg{}, true},
		{"MPRIS volume", playback.SetVolumeMsg{VolumeDB: -3}, true},
		{"cursor key", libKey("j"), false},
		{"MPRIS play while playing", playback.PlayMsg{}, false},
		{"a tick", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			r.e.seekable = true
			r.e.duration = 5 * time.Minute
			r.press(1)
			r.tick(14*time.Minute + 40*time.Second)
			if !r.m.sleep.fading {
				t.Fatal("setup: not fading")
			}
			if tt.msg == nil {
				r.tick(14*time.Minute + 45*time.Second)
			} else {
				r.m.focus = focusPlaylist
				r.send(tt.msg)
			}
			cancelled := !r.armed() && !r.m.sleep.fading
			if cancelled != tt.cancels {
				t.Fatalf("cancelled %v, want %v (fades %q)", cancelled, tt.cancels, r.e.fades)
			}
			if tt.cancels {
				if !slices.Equal(r.e.fades, []string{"out 20s", "cancel"}) {
					t.Errorf("fades %q, want the fade cancelled", r.e.fades)
				}
				if r.m.status.text != "Sleep timer cancelled" {
					t.Errorf("status %q", r.m.status.text)
				}
			}
		})
	}
}

// Before the fade, changing track (skipping, the queue moving on, a gapless
// switch) leaves the timer as it is.
func TestSleepTrackChangesBeforeTheFadeKeepTheTimer(t *testing.T) {
	tests := []struct {
		name string
		do   func(r *sleepRig)
	}{
		{"next key", func(r *sleepRig) { r.key(">") }},
		{"previous key", func(r *sleepRig) { r.key("<") }},
		{"MPRIS next", func(r *sleepRig) { r.send(playback.NextMsg{}) }},
		{"volume key", func(r *sleepRig) { r.key("+") }},
		{"pause and resume", func(r *sleepRig) { r.key("space"); r.key("space") }},
		{"gapless switch", func(r *sleepRig) { r.e.gaplessAdvanced = true; r.tick(5 * time.Minute) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			r.press(1)
			r.tick(time.Minute)
			tt.do(r)
			if r.m.sleep.deadline != sleepT0.Add(15*time.Minute) {
				t.Fatalf("deadline %v, want unchanged", r.m.sleep.deadline)
			}
			r.e.paused = false
			r.e.playing = true
			r.tick(14*time.Minute + 31*time.Second)
			r.tick(15 * time.Minute)
			if !r.e.paused {
				t.Error("did not pause at the deadline")
			}
		})
	}
}

// When the deadline comes and nothing is playing, the timer just ends.
func TestSleepExpiryWithNothingPlaying(t *testing.T) {
	tests := []struct {
		name  string
		setup func(r *sleepRig)
	}{
		{"paused", func(r *sleepRig) { r.e.paused = true }},
		{"stopped", func(r *sleepRig) { r.e.playing = false }},
		{"empty queue", func(r *sleepRig) { r.e.playing = false; r.m.playlist.Replace(nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			tt.setup(r)
			playing, paused := r.e.playing, r.e.paused
			r.press(1)
			if !r.armed() {
				t.Fatal("Z did not arm the timer")
			}
			r.tick(14*time.Minute + 45*time.Second)
			r.tick(15 * time.Minute)
			if r.armed() {
				t.Error("timer still set")
			}
			if r.e.playing != playing || r.e.paused != paused || r.e.stopCalls != 0 || len(r.e.playCalls) != 0 {
				t.Errorf("playback changed: playing %v paused %v, stops %d, plays %q", r.e.playing, r.e.paused, r.e.stopCalls, r.e.playCalls)
			}
			if len(r.e.fades) != 0 {
				t.Errorf("fades %q with nothing playing", r.e.fades)
			}
			if r.m.status.text != "Sleep timer ended" {
				t.Errorf("status %q", r.m.status.text)
			}
		})
	}
}

// A track still starting at the deadline is paused once it plays.
func TestSleepExpiryWaitsForATrackStarting(t *testing.T) {
	r := newSleepRig(t, 0)
	r.press(1)
	r.m.buffering = true
	r.tick(15 * time.Minute)
	if !r.armed() || r.e.paused {
		t.Fatalf("armed %v, paused %v: want the timer waiting", r.armed(), r.e.paused)
	}
	r.m.buffering = false
	r.tick(15*time.Minute + time.Second)
	if r.armed() || !r.e.paused {
		t.Errorf("armed %v, paused %v: want paused once the track plays", r.armed(), r.e.paused)
	}
}

// Repeat-one plays the same track over and over; the timer still pauses it.
func TestSleepRepeatOne(t *testing.T) {
	r := newSleepRig(t, 30*time.Second)
	r.m.playlist.SetRepeat(playlist.RepeatOne)
	r.press(2)
	r.tick(29*time.Minute + 31*time.Second)
	r.tick(30 * time.Minute)
	if !r.e.paused || r.m.playlist.Repeat() != playlist.RepeatOne || r.m.playlist.Index() != 1 {
		t.Errorf("paused %v, repeat %v, index %d", r.e.paused, r.m.playlist.Repeat(), r.m.playlist.Index())
	}
}

// The timer never sets the user's volume: arming, fading, pausing, Off and
// a cancel all leave it as the user set it.
func TestSleepNeverTouchesTheUserVolume(t *testing.T) {
	r := newSleepRig(t, 30*time.Second)
	r.press(1)
	r.tick(14*time.Minute + 31*time.Second)
	r.tick(15 * time.Minute)
	r.key("space")
	r.press(5)
	r.tick(105*time.Minute - 10*time.Second)
	r.press(1) // Off during the fade
	if r.e.setVolumeCalls != 0 || r.e.volume != -7 {
		t.Errorf("SetVolume called %d times, volume %v; want untouched -7", r.e.setVolumeCalls, r.e.volume)
	}
	if !slices.Equal(r.e.fades, []string{"out 29s", "reset", "out 10s", "cancel"}) {
		t.Errorf("fades %q", r.e.fades)
	}
}

// An engine without the fade (none in this build of the fake) still pauses
// at the deadline.
func TestSleepWithoutAFader(t *testing.T) {
	r := newSleepRig(t, 30*time.Second)
	plain := r.e.playbackFakeEngine
	r.m.player = plain
	r.press(1)
	r.tick(14*time.Minute + 45*time.Second)
	r.tick(15 * time.Minute)
	if !plain.paused {
		t.Error("did not pause")
	}
}
