package model

// ddsonic: the sleep timer (sleep_ddsonic.go, #35).

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

var sleepT0 = time.Date(2026, 11, 10, 23, 0, 0, 0, time.UTC)

// sleepEngine is the playback fake with a volume clamped as the player
// clamps it ([-50, +6] dB), a seek that moves, and the player's fade
// commands, recorded ("until 15m0s": a fade to silence at t0+15m).
type sleepEngine struct {
	*playbackFakeEngine
	volume         float64
	setVolumeCalls int
	fades          []string
}

func (e *sleepEngine) SetVolume(db float64) { e.volume = max(min(db, 6), -50); e.setVolumeCalls++ }
func (e *sleepEngine) Volume() float64      { return e.volume }
func (e *sleepEngine) FadeOutUntil(end time.Time) {
	e.fades = append(e.fades, "until "+end.Sub(sleepT0).String())
}
func (e *sleepEngine) CancelFade() { e.fades = append(e.fades, "cancel") }
func (e *sleepEngine) ResetFade()  { e.fades = append(e.fades, "reset") }
func (e *sleepEngine) Seek(d time.Duration) error {
	e.position += d
	return e.playbackFakeEngine.Seek(d)
}

// sleepRig is a model playing track 2 of 3, a settable clock, and the
// engine.
type sleepRig struct {
	t   *testing.T
	m   Model
	e   *sleepEngine
	now time.Time
}

func newSleepEngine() *sleepEngine {
	return &sleepEngine{playbackFakeEngine: &playbackFakeEngine{playing: true, position: 42 * time.Second}, volume: -7}
}

func newSleepRig(t *testing.T, fade time.Duration) *sleepRig {
	t.Helper()
	m := keybindingTestModel()
	m.plVisible = 10
	m.playlist.Replace(tracksOf("a", "b", "c"))
	m.playlist.SetIndex(1)
	return rigFor(t, m, fade)
}

// rigFor puts a sleep engine and a clock on m.
func rigFor(t *testing.T, m Model, fade time.Duration) *sleepRig {
	r := &sleepRig{t: t, now: sleepT0, e: newSleepEngine()}
	m.player = r.e
	m.SetSleepFade(fade)
	m.sleep.now = func() time.Time { return r.now }
	r.m = m
	return r
}

func (r *sleepRig) send(msg tea.Msg) {
	r.t.Helper()
	updated, cmd := r.m.Update(msg)
	r.m = libRun(r.t, updated.(Model), cmd)
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

// fading arms 15 minutes and ticks into the fade's last 20 seconds.
func (r *sleepRig) fading() {
	r.t.Helper()
	r.press(1)
	r.tick(14*time.Minute + 40*time.Second)
	if !r.m.sleep.fading || !slices.Equal(r.e.fades, []string{"until 15m0s"}) {
		r.t.Fatalf("setup: fading %v, fades %q", r.m.sleep.fading, r.e.fades)
	}
}

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

// A running countdown stays whole in the header at narrow widths: the
// other badges, or the library's breadcrumb and counter, give way first.
func TestSleepCountdownSurvivesNarrowHeaders(t *testing.T) {
	for _, width := range []int{40, 56, 80} {
		t.Run(fmt.Sprintf("classic %d", width), func(t *testing.T) {
			m := newColumnTestModel(width, 24)
			m.playlist.Queue(1)
			m.playlist.ToggleBookmark(2)
			m.favSet = map[string]struct{}{"/tmp/track-3.mp3": {}}
			m.sleep.now = func() time.Time { return sleepT0 }
			m.sleep.deadline = sleepT0.Add(15 * time.Minute)
			pane := m.layout.playlistWidth
			if pane <= 0 {
				pane = ui.PanelWidth
			}
			restore := ui.WithPanelWidth(pane)
			header := ansi.Strip(m.renderPlaylistHeader())
			restore()
			if !strings.Contains(header, "[Sleep 15m]") {
				t.Errorf("width %d (pane %d): header %q lost the countdown", width, pane, header)
			}
			if got := lipgloss.Width(header); got > pane {
				t.Errorf("width %d: header is %d cells, wider than the pane's %d", width, got, pane)
			}
		})
		t.Run(fmt.Sprintf("library %d", width), func(t *testing.T) {
			long := "A very long 音楽 album title that will not fit in a narrow terminal"
			root := library.Menu("Music", library.Entry{Title: long, Open: library.Menu(long,
				library.Entry{Title: "one", Open: library.Menu("one")}, library.Entry{Title: "two", Open: library.Menu("two")})})
			m := newLibraryModel(root)
			m = libPress(t, m, "enter")
			m.sleep.now = func() time.Time { return sleepT0 }
			m.sleep.deadline = sleepT0.Add(15 * time.Minute)
			restore := ui.WithPanelWidth(width)
			header := ansi.Strip(m.libHeaderLine())
			restore()
			if !strings.HasSuffix(strings.TrimRight(header, "─ "), "[Sleep 15m]") {
				t.Errorf("width %d: header %q, want the countdown whole at its end", width, header)
			}
			if got := lipgloss.Width(header); got > width {
				t.Errorf("width %d: header is %d cells", width, got)
			}
			if width < 80 && !strings.Contains(header, "…") {
				t.Errorf("width %d: header %q, want the breadcrumb shortened with …", width, header)
			}
		})
	}
	t.Run("library without the timer is unchanged", func(t *testing.T) {
		m := newLibraryModel(testLibraryRoot())
		if got, want := m.libHeaderLine(), sepHeaderN(m.libBreadcrumb(), 1, len(m.libTop().entries)); got != want {
			t.Errorf("header %q, want %q", got, want)
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
				wantFades = []string{"until 15m0s", "reset"}
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

// The fade always ends at the deadline: it starts on the first tick in its
// window, and a late tick or a fade longer than the timer only shortens it.
func TestSleepFadeEndsAtTheDeadline(t *testing.T) {
	type step struct {
		at    time.Duration
		fades int // fade commands sent by then
	}
	tests := []struct {
		name  string
		fade  time.Duration
		steps []step
	}{
		{"on time", 30 * time.Second, []step{{14*time.Minute + 29*time.Second, 0}, {14*time.Minute + 30*time.Second, 1}, {14*time.Minute + 45*time.Second, 1}}},
		{"a late tick", 30 * time.Second, []step{{14*time.Minute + 29*time.Second, 0}, {14*time.Minute + 30*time.Second + 400*time.Millisecond, 1}}},
		{"longer than the timer", 20 * time.Minute, []step{{time.Second, 1}}},
		{"no fade", 0, []step{{14*time.Minute + 59*time.Second, 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, tt.fade)
			r.press(1)
			for _, s := range tt.steps {
				r.tick(s.at)
				if len(r.e.fades) != s.fades {
					t.Fatalf("at %v: fades %q, want %d", s.at, r.e.fades, s.fades)
				}
			}
			for _, f := range r.e.fades {
				if f != "until 15m0s" {
					t.Errorf("fade %q, want one ending at the deadline (until 15m0s)", f)
				}
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
	for _, tt := range []struct {
		name  string
		armed bool
	}{{"off", false}, {"another preset", true}} {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			r.press(5) // 90 min: the next press is Off
			deadline := 90 * time.Minute
			if tt.armed {
				r.press(2) // Off, then 15 min; the next press is 30 min
				deadline = 15 * time.Minute
			}
			r.tick(deadline - 20*time.Second)
			if !r.m.sleep.fading {
				t.Fatal("setup: not fading")
			}
			r.press(1)
			if r.m.sleep.fading || len(r.e.fades) != 2 || r.e.fades[1] != "cancel" {
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

// v2 is an IPC request as the socket delivers it.
func v2(t *testing.T, op, params string) V2RequestMsg {
	t.Helper()
	jobs := ipc.NewJobStore()
	t.Cleanup(jobs.CancelAll)
	job, err := jobs.Create(op)
	if err != nil {
		t.Fatal(err)
	}
	msg := V2RequestMsg{Request: ipc.V2Request{Operation: op}, Jobs: jobs, JobID: job.ID}
	if params != "" {
		msg.Request.Params = json.RawMessage(params)
	}
	return msg
}

// During the fade, every volume or transport command cancels the fade and
// the timer, whatever it then does: a volume already at its limit, the
// same value again, a 1-second seek, Play while playing. Moving the cursor,
// ticks and automatic advancement do not.
func TestSleepCommandDuringTheFade(t *testing.T) {
	press := func(keys ...string) func(r *sleepRig) {
		return func(r *sleepRig) {
			for _, k := range keys {
				r.key(k)
			}
		}
	}
	send := func(msg func(t *testing.T) tea.Msg) func(r *sleepRig) {
		return func(r *sleepRig) { r.send(msg(r.t)) }
	}
	msg := func(m tea.Msg) func(*testing.T) tea.Msg { return func(*testing.T) tea.Msg { return m } }
	ipcOp := func(op, params string) func(*testing.T) tea.Msg {
		return func(t *testing.T) tea.Msg { return v2(t, op, params) }
	}
	tests := []struct {
		name    string
		prep    func(r *sleepRig)
		do      func(r *sleepRig)
		cancels bool
	}{
		{"volume up", nil, press("+"), true},
		{"volume up (=)", nil, press("="), true},
		{"volume down", nil, press("-"), true},
		{"+ at the maximum", func(r *sleepRig) { r.e.volume = 6 }, press("+"), true},
		{"- at the floor", func(r *sleepRig) { r.e.volume = -50 }, press("-"), true},
		{"pause key", nil, press("space"), true},
		{"stop key", nil, press("s"), true},
		{"next key", nil, press(">"), true},
		{"previous key", nil, press("<"), true},
		{"seek key", nil, func(r *sleepRig) { r.send(tea.KeyPressMsg{Code: tea.KeyRight}) }, true},
		{"seek to 50% (5j)", nil, press("5", "j"), true},
		{"jump to a time", nil, func(r *sleepRig) {
			r.send(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
			for _, k := range "1:00" {
				r.key(string(k))
			}
			r.key("enter")
		}, true},
		{"Enter on another track", func(r *sleepRig) { r.m.plCursor = 2 }, press("enter"), true},
		{"MPRIS pause", nil, send(msg(playback.PauseMsg{})), true},
		{"MPRIS play while playing", nil, send(msg(playback.PlayMsg{})), true},
		{"MPRIS play/pause", nil, send(msg(playback.PlayPauseMsg{})), true},
		{"MPRIS stop", nil, send(msg(playback.StopMsg{})), true},
		{"MPRIS next", nil, send(msg(playback.NextMsg{})), true},
		{"MPRIS previous", nil, send(msg(playback.PrevMsg{})), true},
		{"MPRIS 1 s seek", nil, send(msg(playback.SeekMsg{Offset: time.Second})), true},
		{"MPRIS set position", nil, send(msg(playback.SetPositionMsg{Position: 43 * time.Second})), true},
		{"MPRIS volume, same value", nil, send(msg(playback.SetVolumeMsg{VolumeDB: -7})), true},
		{"IPC pause", nil, send(ipcOp("player.pause", "")), true},
		{"IPC play while playing", nil, send(ipcOp("player.play", "")), true},
		{"IPC toggle", nil, send(ipcOp("player.toggle", "")), true},
		{"IPC next", nil, send(ipcOp("player.next", "")), true},
		{"IPC volume, same value", nil, send(ipcOp("player.volume.set", `{"value":-7}`)), true},
		{"IPC 1 s seek", nil, send(ipcOp("player.seek.relative", `{"value":1}`)), true},
		{"cursor keys", nil, press("j", "k", "g"), false},
		{"open and close the jump prompt", nil, func(r *sleepRig) {
			r.send(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
			r.key("esc")
		}, false},
		{"IPC state read", nil, send(ipcOp("runtime.state", "")), false},
		{"a tick", nil, func(r *sleepRig) { r.tick(14*time.Minute + 45*time.Second) }, false},
		{"gapless advance", nil, func(r *sleepRig) {
			r.e.gaplessAdvanced = true
			r.tick(14*time.Minute + 45*time.Second)
		}, false},
		{"track ends, next starts", nil, func(r *sleepRig) {
			r.e.drained = true
			r.tick(14*time.Minute + 45*time.Second)
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, 30*time.Second)
			r.m.focus = focusPlaylist
			r.e.seekable = true
			r.e.duration = 5 * time.Minute
			if tt.prep != nil {
				tt.prep(r)
			}
			r.fading()
			tt.do(r)
			cancelled := !r.armed() && !r.m.sleep.fading
			if cancelled != tt.cancels {
				t.Fatalf("cancelled %v, want %v (fades %q, status %q)", cancelled, tt.cancels, r.e.fades, r.m.status.text)
			}
			if tt.cancels && !slices.Equal(r.e.fades, []string{"until 15m0s", "cancel"}) {
				t.Errorf("fades %q, want the fade cancelled", r.e.fades)
			}
			if !tt.cancels && len(r.e.fades) != 1 {
				t.Errorf("fades %q, want the fade left alone", r.e.fades)
			}
		})
	}
}

// The same rule in the library: a row that plays is a command, even one
// whose tracks load later; opening a list, and text typed into search, are
// not.
func TestSleepLibraryCommandsDuringTheFade(t *testing.T) {
	loading := make(chan struct{})
	t.Cleanup(func() { close(loading) })
	root := library.Menu("Music",
		library.Entry{Title: "Albums", Open: library.Menu("Albums", library.Entry{Title: "inside"})},
		library.Entry{Title: "Late album", Play: func(ctx context.Context) ([]playlist.Track, error) {
			<-loading
			return tracksOf("x"), nil
		}},
		library.Entry{Title: "Track", Track: &playlist.Track{Path: "t", Title: "t"}},
	)
	tests := []struct {
		name    string
		do      func(r *sleepRig)
		cancels bool
	}{
		{"open a list", func(r *sleepRig) { r.send(libKey("enter")) }, false},
		{"play a track", func(r *sleepRig) { r.key("j"); r.key("j"); r.send(libKey("enter")) }, true},
		{"play what loads later", func(r *sleepRig) {
			r.key("j")
			updated, _ := r.m.Update(libKey("enter")) // the load is not run: still loading
			r.m = updated.(Model)
		}, true},
		{"next", func(r *sleepRig) { r.key("n") }, true},
		{"previous", func(r *sleepRig) { r.key("p") }, true},
		{"pause", func(r *sleepRig) { r.key("space") }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLibraryModel(root)
			m.playlist.Replace(tracksOf("a", "b", "c"))
			m.playlist.SetIndex(1)
			r := rigFor(t, m, 30*time.Second)
			r.fading()
			tt.do(r)
			if cancelled := !r.armed() && !r.m.sleep.fading; cancelled != tt.cancels {
				t.Fatalf("cancelled %v, want %v (fades %q)", cancelled, tt.cancels, r.e.fades)
			}
		})
	}
	t.Run("search text", func(t *testing.T) {
		m, _ := newSearchModel(t)
		m.playlist.Replace(tracksOf("a", "b", "c"))
		m.playlist.SetIndex(1)
		r := rigFor(t, m, 30*time.Second)
		r.fading()
		r.m = typeText(t, libPress(t, r.m, "/"), "+- spn")
		if !r.m.lib.searchInput {
			t.Fatal("setup: not typing in search")
		}
		if !r.m.sleep.fading || !r.armed() {
			t.Errorf("text typed into search cancelled the fade (fades %q)", r.e.fades)
		}
	})
}

// IPC and MPRIS operations, as sleepRemoteCommand classifies them.
func TestSleepRemoteCommands(t *testing.T) {
	commands := []string{"player.play", "player.pause", "player.toggle", "player.stop", "player.next", "player.prev",
		"player.previous", "player.volume.set", "player.volume.adjust", "player.seek.relative", "player.seek.absolute",
		"runtime.play", "runtime.pause", "runtime.toggle", "runtime.stop", "runtime.next", "runtime.prev",
		"runtime.volume", "runtime.seek", "runtime.playlist.play", "runtime.queue.play"}
	for _, op := range commands {
		if !sleepRemoteCommand(v2(t, op, "")) {
			t.Errorf("%s is not a command", op)
		}
	}
	others := []string{"runtime.state", "player.speed.set", "runtime.playlist.get", "runtime.queue.list", "runtime.queue.enqueue", "bogus"}
	for _, op := range others {
		if sleepRemoteCommand(v2(t, op, "")) {
			t.Errorf("%s is a command", op)
		}
	}
	if !sleepRemoteCommand(v2(t, "url.load", `{"path":"x","play":true}`)) || sleepRemoteCommand(v2(t, "url.load", `{"path":"x"}`)) {
		t.Error("url.load is a command exactly when it plays")
	}
	for _, m := range []tea.Msg{playback.PlayMsg{}, playback.PauseMsg{}, playback.PlayPauseMsg{}, playback.StopMsg{},
		playback.NextMsg{}, playback.PrevMsg{}, playback.SeekMsg{}, playback.SetPositionMsg{}, playback.SetVolumeMsg{}} {
		if !sleepRemoteCommand(m) {
			t.Errorf("%T is not a command", m)
		}
	}
	for _, m := range []tea.Msg{tickMsg{}, playback.QuitMsg{}, libKey("+")} {
		if sleepRemoteCommand(m) {
			t.Errorf("%T is a remote command", m)
		}
	}
}

// Before the fade, changing track (skipping, the queue moving on, a gapless
// switch) or the volume leaves the timer as it is.
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

// A track starting when the deadline comes is never heard: the output is
// held silent from the deadline, and the track pauses as soon as it has
// started. If it fails to start, the timer just ends.
func TestSleepExpiryWhileATrackStarts(t *testing.T) {
	tests := []struct {
		name     string
		fade     time.Duration
		started  bool // the start succeeds
		command  bool // the user gives a command while it starts
		want     []string
		paused   bool
		statusIs string
	}{
		{"no fade", 0, true, false, []string{"until 15m0s", "reset"}, true, "Sleep timer: paused"},
		{"after a fade", 30 * time.Second, true, false, []string{"until 15m0s", "reset"}, true, "Sleep timer: paused"},
		{"the start fails", 30 * time.Second, false, false, []string{"until 15m0s", "reset"}, false, "Sleep timer ended"},
		{"a command while it starts", 0, true, true, []string{"until 15m0s", "cancel"}, false, ""}, // Next's own status follows
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newSleepRig(t, tt.fade)
			r.press(1)
			r.tick(14*time.Minute + 45*time.Second)
			r.m.buffering = true
			r.e.playing = false
			r.tick(15 * time.Minute)
			if r.armed() || r.m.sleepBadge() != "" || !r.m.sleep.pausePending {
				t.Fatalf("at the deadline: armed %v, pending %v; want the badge gone and a pause pending", r.armed(), r.m.sleep.pausePending)
			}
			if len(r.e.fades) == 0 || r.e.fades[0] != "until 15m0s" {
				t.Fatalf("fades %q, want the output silent from the deadline", r.e.fades)
			}
			if tt.command {
				r.send(playback.NextMsg{})
			}
			r.m.buffering = false
			r.e.playing = tt.started
			r.tick(15*time.Minute + 50*time.Millisecond)
			if r.e.paused != tt.paused {
				t.Errorf("paused %v, want %v", r.e.paused, tt.paused)
			}
			if !slices.Equal(r.e.fades, tt.want) {
				t.Errorf("fades %q, want %q", r.e.fades, tt.want)
			}
			if r.m.sleep.pausePending || r.m.sleep.fading || r.armed() {
				t.Error("timer state left behind")
			}
			if tt.statusIs != "" && r.m.status.text != tt.statusIs {
				t.Errorf("status %q, want %q", r.m.status.text, tt.statusIs)
			}
		})
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
	if !slices.Equal(r.e.fades, []string{"until 15m0s", "reset", "until 1h45m0s", "cancel"}) {
		t.Errorf("fades %q", r.e.fades)
	}
}

// An engine without the fade still pauses at the deadline.
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
