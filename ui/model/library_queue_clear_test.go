package model

// ddsonic: X clears the whole queue from the library's Queue view, Ctrl+Z
// undoes it once, and the queue's undo holds only while the playlist is as
// the change left it (#22).

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
)

// clearedQueueModel is the Queue view with the album playing at track I,
// track III queued next, shuffle and repeat on, and a saved playlist loaded;
// before is its playlist state.
func clearedQueueModel(t *testing.T) (m Model, eng *soundEngine, saver *playlistManagerTestProvider, before playlist.Snapshot) {
	t.Helper()
	m, eng = newQueueModel(t)
	m = queuePress(queuePress(queuePress(m, "j"), "j"), "a") // III next
	m = queuePress(queuePress(m, "z"), "r")                  // shuffle, repeat all
	m = queuePress(m, "g")
	saver = &playlistManagerTestProvider{}
	m.localProvider = saver
	m.loadedPlaylist = "Saved"
	return m, eng, saver, m.playlist.Snapshot()
}

func TestQueueXClearsTheWholeQueue(t *testing.T) {
	m, eng, saver, _ := clearedQueueModel(t)
	if !eng.playing || m.playlist.QueueLen() != 1 {
		t.Fatalf("setup: playing %t, Up next %d; want playing with one queued", eng.playing, m.playlist.QueueLen())
	}
	stops, clears, gen := eng.stopCalls, eng.clearPreloadCalls, m.requests.stream
	m = queuePress(m, "X")
	if m.playlist.Len() != 0 || m.playlist.QueueLen() != 0 {
		t.Fatalf("after X: %d tracks, %d up next; want none", m.playlist.Len(), m.playlist.QueueLen())
	}
	if eng.playing || eng.stopCalls != stops+1 || m.requests.stream == gen {
		t.Errorf("playback: playing %t, stops %d (was %d), stream generation %d (was %d); want stopped, generation bumped",
			eng.playing, eng.stopCalls, stops, m.requests.stream, gen)
	}
	if eng.clearPreloadCalls <= clears || eng.hasPreload {
		t.Errorf("gapless preload: %d clears (was %d), armed %t; want cleared", eng.clearPreloadCalls, clears, eng.hasPreload)
	}
	if m.loadedPlaylist != "" || saver.saved != nil || saver.saveName != "" {
		t.Errorf("loaded %q, saved %q %v; want the playlist detached and its file untouched", m.loadedPlaylist, saver.saveName, saver.saved)
	}
	if !strings.Contains(m.status.text, "Cleared 3 tracks") || !strings.Contains(m.status.text, "Ctrl+Z to undo") {
		t.Errorf("status %q, want the count and the undo hint", m.status.text)
	}
}

func TestQueueCtrlZRestoresTheClearedQueue(t *testing.T) {
	m, eng, saver, before := clearedQueueModel(t)
	m = queuePress(m, "X")
	plays := len(eng.playCalls)
	m = queuePress(m, "ctrl+z")
	if got := m.playlist.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Errorf("after Ctrl+Z the playlist is\n%+v\nwant\n%+v", got, before)
	}
	if m.playlist.Index() != 0 || !m.playlist.Shuffled() || m.playlist.Repeat() != playlist.RepeatAll || m.playlist.QueueLen() != 1 {
		t.Errorf("restored index %d, shuffle %t, repeat %v, Up next %d", m.playlist.Index(), m.playlist.Shuffled(), m.playlist.Repeat(), m.playlist.QueueLen())
	}
	if m.loadedPlaylist != "Saved" || saver.saved != nil {
		t.Errorf("loaded %q, saved %v; want Saved reattached, its file untouched", m.loadedPlaylist, saver.saved)
	}
	if eng.playing || len(eng.playCalls) != plays {
		t.Errorf("Ctrl+Z restarted playback: playing %t, plays %d (was %d)", eng.playing, len(eng.playCalls), plays)
	}
	// One level: a second Ctrl+Z has nothing to undo.
	m = queuePress(m, "ctrl+z")
	if m.status.text != "Nothing to undo" || !reflect.DeepEqual(m.playlist.Snapshot(), before) {
		t.Errorf("second Ctrl+Z: status %q; want Nothing to undo and the queue as restored", m.status.text)
	}
}

// assertUndoDropped presses Ctrl+Z and fails unless it says "Nothing to
// undo" and leaves the playlist as it is.
func assertUndoDropped(t *testing.T, m Model) {
	t.Helper()
	before := m.playlist.Snapshot()
	m = queuePress(m, "ctrl+z")
	if m.status.text != "Nothing to undo" {
		t.Errorf("Ctrl+Z: status %q, want Nothing to undo", m.status.text)
	}
	if !reflect.DeepEqual(m.playlist.Snapshot(), before) {
		t.Error("Ctrl+Z changed the playlist")
	}
}

// Any change to the queue after X drops the undo.
func TestQueueUndoOfXDroppedByAChange(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(Model) Model
	}{
		{"an add", func(m Model) Model {
			m.playlist.Add(playlist.Track{Path: "added", Title: "Added"})
			return m
		}},
		{"playing from the library", func(m Model) Model {
			return queuePress(queuePress(m, "tab"), "enter") // replaces the queue
		}},
		{"shuffle", func(m Model) Model { return queuePress(m, "z") }},
		{"repeat", func(m Model) Model { return queuePress(m, "r") }},
		{"another clear", func(m Model) Model {
			m.playlist.Add(playlist.Track{Path: "added", Title: "Added"})
			return queuePress(m, "X") // its own undo restores "Added", not the album
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _, _, _ := clearedQueueModel(t)
			m = queuePress(m, "X")
			if !m.playlistUndo.active {
				t.Fatal("X armed no undo")
			}
			m = tt.change(m)
			if m.lib.visible {
				m = queuePress(m, "tab")
			}
			if tt.name == "another clear" {
				m = queuePress(m, "ctrl+z")
				if tr, _ := m.playlist.Track(0); m.playlist.Len() != 1 || tr.Path != "added" {
					t.Fatalf("Ctrl+Z after the second clear: %d tracks; want only the added one", m.playlist.Len())
				}
				return
			}
			assertUndoDropped(t, m)
		})
	}
}

// The same holds for x's undo: the stale-undo bug, where Ctrl+Z after
// another list was played brought back the list x had edited.
func TestQueueUndoOfRemoveDroppedByAChange(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(Model) Model
	}{
		{"a replacement, then an add", func(m Model) Model {
			m.replacePlaylist([]playlist.Track{{Path: "n1", Title: "New 1"}, {Path: "n2", Title: "New 2"}})
			m.playlist.Add(playlist.Track{Path: "a", Title: "Added"})
			return m
		}},
		{"playing from the library", func(m Model) Model { return queuePress(queuePress(m, "tab"), "enter") }},
		{"a reorder", func(m Model) Model { return queuePress(queuePress(m, "g"), "shift+down") }},
		{"Up next", func(m Model) Model { return queuePress(m, "a") }},
		{"shuffle", func(m Model) Model { return queuePress(m, "z") }},
		{"repeat", func(m Model) Model { return queuePress(m, "r") }},
		{"the next track (n)", func(m Model) Model { return queuePress(m, "n") }},
		{"playing another track", func(m Model) Model { return queuePress(queuePress(m, "G"), "enter") }},
		{"the track ending", func(m Model) Model {
			m.nextTrack()
			return m
		}},
		{"a gapless advance", func(m Model) Model {
			m.player.(*soundEngine).gaplessAdvanced = true
			updated, _ := m.Update(tickMsg(time.Now()))
			return updated.(Model)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newQueueModel(t)
			m = queuePress(queuePress(m, "j"), "x") // remove II; I plays on
			if m.playlist.Len() != 2 {
				t.Fatalf("setup: %d tracks after x, want 2", m.playlist.Len())
			}
			m = tt.change(m)
			if m.lib.visible {
				m = queuePress(m, "tab")
			}
			assertUndoDropped(t, m)
		})
	}
}

// Up next's own undo (d, c) is dropped the same way.
func TestUpNextUndoDroppedByAChange(t *testing.T) {
	m, _ := newQueueModel(t)
	m = queuePress(queuePress(m, "j"), "a")
	m = queuePress(m, "A") // Up next
	m = queuePress(m, "c") // clear it
	m = queuePress(m, "z") // shuffle
	assertUndoDropped(t, m)
}

// Moving the cursor, seeking, pausing, the volume, EQ, speed and the tick
// change no playlist state, so the undo holds.
func TestQueueUndoKeptAcrossNonChanges(t *testing.T) {
	keys := []string{"j", "k", "G", "g", "down", "up", "right", "left", "space", "space", "+", "-", "e", "]", "["}
	t.Run("x", func(t *testing.T) {
		m, _ := newQueueModel(t)
		m = queuePress(queuePress(m, "j"), "x")
		rev := m.playlist.Revision()
		for _, k := range keys {
			m = queuePress(m, k)
		}
		updated, _ := m.Update(tickMsg(time.Now()))
		m = updated.(Model)
		if m.playlist.Revision() != rev {
			t.Fatalf("revision %d after cursor, seek, pause, volume, EQ, speed and a tick; was %d", m.playlist.Revision(), rev)
		}
		if m = queuePress(m, "ctrl+z"); m.playlist.Len() != 3 {
			t.Errorf("Ctrl+Z: %d tracks, want 3 (status %q)", m.playlist.Len(), m.status.text)
		}
	})
	t.Run("X", func(t *testing.T) {
		m, _, _, before := clearedQueueModel(t)
		if m = queuePress(m, "X"); m.playlist.Len() != 0 {
			t.Fatal("X did not clear the queue")
		}
		for _, k := range []string{"j", "k", "G", "g", "+", "-", "e", "]", "["} {
			m = queuePress(m, k)
		}
		if m = queuePress(m, "ctrl+z"); !reflect.DeepEqual(m.playlist.Snapshot(), before) {
			t.Errorf("Ctrl+Z did not restore the queue (status %q)", m.status.text)
		}
	})
}

func TestQueueXOnAnEmptyQueueKeepsTheUndo(t *testing.T) {
	m, _, _, before := clearedQueueModel(t)
	m = queuePress(queuePress(m, "X"), "X")
	if m.status.text != "The queue is empty" {
		t.Errorf("second X: status %q", m.status.text)
	}
	if m = queuePress(m, "ctrl+z"); !reflect.DeepEqual(m.playlist.Snapshot(), before) {
		t.Errorf("Ctrl+Z after X on the empty queue did not restore it (status %q)", m.status.text)
	}
}

// X is text in the queue's filter, the jump input and the library's search.
func TestQueueXIsTextInInputs(t *testing.T) {
	t.Run("filter", func(t *testing.T) {
		m, _ := newQueueModel(t)
		m = queuePress(queuePress(m, "/"), "X")
		if !m.search.active || m.search.query != "X" || m.playlist.Len() != 3 {
			t.Errorf("filter active %t, query %q, %d tracks; want X typed", m.search.active, m.search.query, m.playlist.Len())
		}
	})
	t.Run("jump", func(t *testing.T) {
		m, _ := newQueueModel(t)
		m = queuePress(queuePress(m, "ctrl+j"), "X")
		if !m.jumping || m.playlist.Len() != 3 {
			t.Errorf("jumping %t, %d tracks; want the input open and the queue whole", m.jumping, m.playlist.Len())
		}
	})
	t.Run("library search", func(t *testing.T) {
		m, _ := newSearchModel(t)
		m.playlist.Add(playlist.Track{Path: "q", Title: "Queued"})
		m = libPress(t, m, "/")
		m = libPress(t, m, "X")
		sl, ok := m.libSearchLevel()
		if !ok || !m.lib.searchInput || m.playlist.Len() != 1 {
			t.Fatalf("search open %t, input %t, %d tracks", ok, m.lib.searchInput, m.playlist.Len())
		}
		if sl.Query() != "X" {
			t.Errorf("query %q, want X", sl.Query())
		}
	})
}

// X does nothing outside the Queue view: the library's lists and the
// overlays over the queue.
func TestQueueXOnlyInTheQueueView(t *testing.T) {
	for _, tt := range []struct {
		name string
		open []string
	}{
		{"the library", []string{"tab"}},
		{"Up next", []string{"A"}},
		{"track info", []string{"i"}},
		{"lyrics", []string{"y"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, eng := newQueueModel(t)
			for _, k := range tt.open {
				m = queuePress(m, k)
			}
			m = queuePress(m, "X")
			if m.playlist.Len() != 3 || !eng.playing || m.playlistUndo.active {
				t.Errorf("X over %s: %d tracks, playing %t, undo %t; want nothing done", tt.name, m.playlist.Len(), eng.playing, m.playlistUndo.active)
			}
		})
	}
}

// Ctrl+K lists X in the Queue view, and the key bar does
// (TestLibraryKeyBarListsTable).
func TestQueueKeymapListsX(t *testing.T) {
	m, _ := newQueueModel(t)
	for _, e := range m.buildKeymapEntries() {
		if e.key == "X" && e.action == "Clear queue" {
			return
		}
	}
	t.Error("the keymap does not list X, Clear queue")
}

// A key press the library and cliamp read as X: Shift+x as well as "X".
func TestQueueShiftXClears(t *testing.T) {
	m, _ := newQueueModel(t)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'x', ShiftedCode: 'X', Mod: tea.ModShift})
	if m = updated.(Model); m.playlist.Len() != 0 {
		t.Errorf("Shift+x: %d tracks, want the queue cleared", m.playlist.Len())
	}
}

// Replacing the queue drops the undo even when the new list is the one the
// queue already holds, so the revision does not move (review round 1).
func TestQueueUndoDroppedByAnEqualReplacement(t *testing.T) {
	for _, tt := range []struct {
		name    string
		arm     func(Model) Model
		replace func(Model) Model
	}{
		{"x, then the same list played from the library", removeSecond, func(m Model) Model {
			m.libraryPlayTracks(m.playlist.Tracks(), m.playlist.Index(), "")
			return m
		}},
		{"x, then the same saved playlist loaded", func(m Model) Model {
			m.localProvider = nil // x edits the live queue only
			m.loadedPlaylist = "Saved"
			return removeSecond(m)
		}, func(m Model) Model {
			m.plManager.tracks, m.plManager.selPlaylist = m.playlist.Tracks(), "Saved"
			m.plMgrLoadAndPlay(m.playlist.Index())
			return m
		}},
		{"X, then the empty queue cleared over IPC", func(m Model) Model { return queuePress(m, "X") }, func(m Model) Model {
			reply := make(chan ipc.Response, 1)
			updated, _ := m.Update(ipc.QueueRequestMsg{Op: "queue.clear", Reply: reply})
			return updated.(Model)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, _ := newQueueModel(t)
			m = tt.arm(m)
			if !m.playlistUndo.active {
				t.Fatal("setup armed no undo")
			}
			rev := m.playlist.Revision()
			m = tt.replace(m)
			if m.playlist.Revision() != rev {
				t.Logf("the replacement moved the revision (%d to %d); the case is meant to keep it", rev, m.playlist.Revision())
			}
			assertUndoDropped(t, m)
		})
	}
}

// Picking the track already playing, or moving the cursor, replaces
// nothing: the undo holds.
func TestQueueUndoKeptByEnterOnTheCurrentTrack(t *testing.T) {
	m, _ := newQueueModel(t)
	m = removeSecond(m)
	m = queuePress(queuePress(queuePress(m, "g"), "enter"), "j")
	if m = queuePress(m, "ctrl+z"); m.playlist.Len() != 3 {
		t.Errorf("Ctrl+Z after Enter on the playing track: %d tracks, want 3 (status %q)", m.playlist.Len(), m.status.text)
	}
}

// removeSecond removes track II with x while track I plays on.
func removeSecond(m Model) Model {
	return queuePress(queuePress(queuePress(m, "g"), "j"), "x")
}
