package model

// ddsonic: q quits from every library screen, and Esc steps back without ever
// quitting (M8.1).

import (
	"testing"
)

func quitsOn(m Model, key string) bool {
	updated, _ := m.Update(queueKey(key))
	return updated.(Model).quitting
}

func TestQQuitsFromEveryLibraryScreen(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) Model
	}{
		{"root", func(t *testing.T) Model { return newLibraryModel(testLibraryRoot()) }},
		{"nested level", func(t *testing.T) Model {
			m := newLibraryModel(testLibraryRoot())
			for _, k := range []string{"j", "l", "l"} {
				m = libPress(t, m, k)
			}
			if len(m.lib.stack) != 3 {
				t.Fatalf("setup: %d frames", len(m.lib.stack))
			}
			return m
		}},
		{"search results", func(t *testing.T) Model {
			m, _ := newSearchModel(t)
			m = typeText(t, libPress(t, m, "/"), "mahler")
			return libPress(t, m, "enter")
		}},
		{"queue", func(t *testing.T) Model { m, _ := newQueueModel(t); return m }},
		{"up next over the queue", func(t *testing.T) Model {
			m, _ := newQueueModel(t)
			return queuePress(m, "A")
		}},
		{"track info over the queue", func(t *testing.T) Model {
			m, _ := newQueueModel(t)
			return queuePress(m, "i")
		}},
		{"lyrics over the queue", func(t *testing.T) Model {
			m, _ := newQueueModel(t)
			m.lyrics.visible = true
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if m := tt.setup(t); !quitsOn(m, "q") {
				t.Error("q did not quit")
			}
		})
	}
}

// Where the keyboard is typing, q is a letter.
func TestQIsTextInInputs(t *testing.T) {
	m, _ := newSearchModel(t)
	m = libPress(t, m, "/")
	if quitsOn(m, "q") {
		t.Error("q in the library search input quit")
	}

	// The keymap's filter, opened with Ctrl+K over track info.
	k, _ := newQueueModel(t)
	k = queuePress(k, "i")
	k.openKeymap()
	k.keymap.searching = true
	if quitsOn(k, "q") {
		t.Error("q in the keymap filter quit")
	}

	for _, open := range []string{"/", "ctrl+j"} { // queue filter, jump to time
		m, _ := newQueueModel(t)
		m = queuePress(m, open)
		if quitsOn(m, "q") {
			t.Errorf("q after %s in the queue quit", open)
		}
	}
}

// Esc steps back from anywhere to the Library root, then does nothing, and
// never quits.
func TestEscGoesBackToTheRootAndNeverQuits(t *testing.T) {
	m, _ := newQueueModel(t)
	m = queuePress(m, "i") // track info over the queue
	for range 12 {
		m = libPress(t, m, "esc")
		if m.quitting {
			t.Fatal("esc quit")
		}
	}
	if !m.lib.visible || len(m.lib.stack) != 1 || m.showInfo {
		t.Errorf("after esc: library %v, %d frames, info %v; want the root", m.lib.visible, len(m.lib.stack), m.showInfo)
	}

	s, _ := newSearchModel(t)
	s = libPress(t, typeText(t, libPress(t, s, "/"), "x"), "enter")
	s = libPress(t, s, "enter") // open the album result
	for range 6 {
		s = libPress(t, s, "esc")
		if s.quitting {
			t.Fatal("esc quit from search")
		}
	}
	if len(s.lib.stack) != 1 {
		t.Errorf("after esc from search: %d frames, want the root", len(s.lib.stack))
	}
}
