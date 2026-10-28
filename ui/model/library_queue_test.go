package model

// ddsonic: the queue view's keys (M7.1). Each passes the library gate and has
// its cliamp effect; overlays open over the queue and close back to it.

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
)

// soundEngine records the mono and speed changes the fake engine drops.
type soundEngine struct {
	*playbackFakeEngine
	mono  bool
	speed float64
}

func (e *soundEngine) ToggleMono()        { e.mono = !e.mono }
func (e *soundEngine) Mono() bool         { return e.mono }
func (e *soundEngine) SetSpeed(s float64) { e.speed = s }
func (e *soundEngine) Speed() float64     { return e.speed }

// queueKey builds the key presses libKey cannot: modified keys.
func queueKey(s string) tea.KeyPressMsg {
	switch s {
	case "shift+up":
		return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}
	case "shift+down":
		return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}
	case "ctrl+z":
		return tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}
	case "ctrl+j":
		return tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	case "ctrl+i":
		return tea.KeyPressMsg{Code: 'i', Mod: tea.ModCtrl}
	}
	return libKey(s)
}

func queuePress(m Model, key string) Model {
	updated, _ := m.Update(queueKey(key))
	return updated.(Model)
}

// newQueueModel plays the Mahler 5 album from the library at its first track
// and shows the queue.
func newQueueModel(t *testing.T) (Model, *soundEngine) {
	t.Helper()
	eng := &soundEngine{playbackFakeEngine: &playbackFakeEngine{}, speed: 1}
	m := newLibraryModel(testLibraryRoot())
	m.player = eng
	for _, k := range []string{"j", "l", "l", "j", "l", "enter", "tab"} {
		m = libPress(t, m, k)
	}
	if m.activeScreen() != screenMain || m.playlist.Len() != 3 {
		t.Fatalf("setup: screen %v, %d tracks; want the queue of 3", m.activeScreen(), m.playlist.Len())
	}
	return m, eng
}

func TestQueueKeysPassGate(t *testing.T) {
	m, _ := newQueueModel(t)
	for _, key := range []string{
		"z", "r", "a", "A", "x", "shift+up", "shift+down", "ctrl+z",
		"e", "[", "]", "i", "y", "ctrl+j", "shift+left", "shift+right",
	} {
		if msg := queueKey(key); msg.String() != key {
			t.Fatalf("queueKey(%q).String() = %q", key, msg.String())
		}
		if _, handled := m.handleLibraryKey(queueKey(key)); handled {
			t.Errorf("queue key %q is swallowed", key)
		}
	}
	for _, key := range []string{"m", "ctrl+i", "<", ">", ",", "."} {
		if _, handled := m.handleLibraryKey(queueKey(key)); !handled {
			t.Errorf("queue key %q reaches cliamp, want it swallowed", key)
		}
	}
}

func TestQueueKeyEffects(t *testing.T) {
	tests := []struct {
		name  string
		keys  []string
		check func(t *testing.T, m Model, eng *soundEngine)
	}{
		{"z shuffles", []string{"z"}, func(t *testing.T, m Model, _ *soundEngine) {
			if !m.playlist.Shuffled() {
				t.Error("shuffle is off")
			}
		}},
		{"r cycles repeat", []string{"r"}, func(t *testing.T, m Model, _ *soundEngine) {
			if got := m.playlist.Repeat(); got != playlist.RepeatAll {
				t.Errorf("repeat = %v, want all", got)
			}
		}},
		{"a queues the selected track next", []string{"j", "j", "a"}, func(t *testing.T, m Model, _ *soundEngine) {
			if m.playlist.QueueLen() != 1 || m.playlist.QueuePosition(2) < 0 {
				t.Errorf("queued %d, track III at %d; want track III queued", m.playlist.QueueLen(), m.playlist.QueuePosition(2))
			}
		}},
		{"x removes the playing track, Ctrl+Z restores it", []string{"x"}, func(t *testing.T, m Model, _ *soundEngine) {
			if m.playlist.Len() != 2 {
				t.Fatalf("after x: %d tracks, want 2", m.playlist.Len())
			}
			m = queuePress(m, "ctrl+z")
			if m.playlist.Len() != 3 {
				t.Errorf("after Ctrl+Z: %d tracks, want 3", m.playlist.Len())
			}
		}},
		{"shift+down moves the track", []string{"shift+down"}, func(t *testing.T, m Model, _ *soundEngine) {
			if tr, _ := m.playlist.Track(1); tr.Path != "I" || m.plCursor != 1 {
				t.Errorf("track 1 = %q, cursor %d; want I moved down with the cursor", tr.Path, m.plCursor)
			}
		}},
		{"shift+up moves it back", []string{"shift+down", "shift+up"}, func(t *testing.T, m Model, _ *soundEngine) {
			if tr, _ := m.playlist.Track(0); tr.Path != "I" || m.plCursor != 0 {
				t.Errorf("track 0 = %q, cursor %d; want I back on top", tr.Path, m.plCursor)
			}
		}},
		{"e cycles the EQ preset", []string{"e"}, func(t *testing.T, m Model, _ *soundEngine) {
			if m.eqPresetIdx != 0 {
				t.Errorf("preset index = %d, want the first preset", m.eqPresetIdx)
			}
		}},
		{"m does nothing", []string{"m"}, func(t *testing.T, _ Model, eng *soundEngine) {
			if eng.mono {
				t.Error("m turned mono on")
			}
		}},
		{"n plays the next track", []string{"n"}, func(t *testing.T, m Model, _ *soundEngine) {
			if got := m.playlist.Index(); got != 1 {
				t.Errorf("playing index %d, want 1", got)
			}
		}},
		{"p after n plays the first again", []string{"n", "p"}, func(t *testing.T, m Model, _ *soundEngine) {
			if got := m.playlist.Index(); got != 0 {
				t.Errorf("playing index %d, want 0", got)
			}
		}},
		{"] and [ change speed", []string{"]", "]", "["}, func(t *testing.T, _ Model, eng *soundEngine) {
			if eng.speed != 1.25 {
				t.Errorf("speed = %v, want 1.25", eng.speed)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, eng := newQueueModel(t)
			m.eqPresetIdx = -1
			for _, k := range tt.keys {
				m = queuePress(m, k)
			}
			if m.activeScreen() != screenMain {
				t.Errorf("screen = %v, want the queue", m.activeScreen())
			}
			tt.check(t, m, eng)
		})
	}
}

func TestQueueOverlaysReturnToQueue(t *testing.T) {
	tests := []struct {
		key, closeKey string
		screen        topLevelScreen
	}{
		{"A", "esc", screenQueue},
		{"i", "esc", screenInfo},
		{"y", "esc", screenLyrics},
		{"ctrl+j", "esc", screenJump},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m, _ := newQueueModel(t)
			m = queuePress(m, tt.key)
			if got := m.activeScreen(); got != tt.screen {
				t.Fatalf("after %q: screen %v, want %v", tt.key, got, tt.screen)
			}
			m = queuePress(m, tt.closeKey)
			if got := m.activeScreen(); got != screenMain {
				t.Errorf("after closing: screen %v, want the queue", got)
			}
		})
	}
}

// r is repeat in the queue and sync in the library.
func TestQueueRepeatLibrarySync(t *testing.T) {
	m, _ := newQueueModel(t)
	synced := 0
	m.lib.refresh = func(string) { synced++ }
	m = queuePress(m, "r")
	if m.playlist.Repeat() != playlist.RepeatAll || synced != 0 {
		t.Fatalf("queue r: repeat %v, %d syncs; want repeat all and no sync", m.playlist.Repeat(), synced)
	}
	m = libPress(t, m, "tab")
	m = queuePress(m, "r")
	if m.playlist.Repeat() != playlist.RepeatAll || synced != 1 {
		t.Errorf("library r: repeat %v, %d syncs; want repeat unchanged and one sync", m.playlist.Repeat(), synced)
	}
}

// p and n skip from the library views too, and are text in the search input.
func TestSkipKeysInTheLibrary(t *testing.T) {
	m, _ := newQueueModel(t)
	m = libPress(t, m, "tab") // back to the library
	if !m.lib.visible {
		t.Fatal("setup: the library is not in front")
	}
	m = libPress(t, m, "n")
	if got := m.playlist.Index(); got != 1 {
		t.Errorf("n in the library: playing %d, want 1", got)
	}
	m = libPress(t, m, "p")
	if got := m.playlist.Index(); got != 0 {
		t.Errorf("p in the library: playing %d, want 0", got)
	}

	s, _ := newSearchModel(t)
	s = typeText(t, libPress(t, s, "/"), "pn")
	if sl, _ := s.libSearchLevel(); sl.Query() != "pn" {
		t.Errorf("query = %q, want p and n typed", sl.Query())
	}
}
