package model

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/library"
)

// notedLevel is a list with a note (library.NotedLevel).
type notedLevel struct {
	rows int
	note []string
}

func (notedLevel) Title() string { return "Albums from tracks" }
func (l notedLevel) Load(context.Context) ([]library.Entry, error) {
	entries := make([]library.Entry, l.rows)
	for i := range entries {
		entries[i] = library.Entry{Title: "Album", Open: library.Menu("Album")}
	}
	return entries, nil
}
func (l notedLevel) Note() []string { return l.note }

// A level's note is drawn above its rows, populated or empty, inside the
// body's rows; a body too short for both shows the list alone.
func TestLibraryShowsALevelsNote(t *testing.T) {
	note := []string{"Saved Library Albums are not synchronized.", "Albums may be incomplete."}
	tests := []struct {
		name      string
		rows      int
		visible   int
		wantNote  bool
		wantFirst string // the first line after the note
	}{
		{"populated", 30, 10, true, "Album"},
		{"empty", 0, 10, true, "Nothing here."},
		{"too short", 30, 4, false, "Album"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums from tracks", Open: notedLevel{tt.rows, note}}))
			m = libPress(t, m, "enter")
			m.plVisible = tt.visible
			_, rows := m.bodySize()
			lines := strings.Split(ansi.Strip(m.renderLibraryBody()), "\n")
			if len(lines) != rows {
				t.Fatalf("body is %d lines, want the body's %d:\n%s", len(lines), rows, strings.Join(lines, "\n"))
			}
			shown := strings.Contains(lines[0], note[0]) && len(lines) > 1 && strings.Contains(lines[1], note[1])
			if shown != tt.wantNote {
				t.Fatalf("note shown = %v, want %v:\n%s", shown, tt.wantNote, strings.Join(lines, "\n"))
			}
			first, budget := lines[0], rows
			if tt.wantNote {
				first, budget = lines[len(note)], rows-len(note)
			}
			if !strings.Contains(first, tt.wantFirst) {
				t.Errorf("first list line = %q, want %q", first, tt.wantFirst)
			}
			if got := m.libListBudget(); got != budget {
				t.Errorf("list budget = %d, want %d", got, budget)
			}
		})
	}
}

// A note is wrapped to the panel, never cut short: its qualifications are
// the point of it. A terminal too short for the wrapped note and three rows
// shows the list alone.
func TestLibraryNoteWrapsToThePanel(t *testing.T) {
	note := []string{
		"Saved YouTube Music Library Albums are not synchronized.",
		"Grouped from Liked Music and synced playlists; albums may be incomplete.",
	}
	for _, size := range []struct{ w, h int }{{40, 30}, {56, 30}, {60, 30}, {80, 24}, {120, 40}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums from tracks", Open: notedLevel{60, note}}))
			m = resized(t, libPress(t, m, "enter"), size.w, size.h)
			width, rows := m.bodySize()
			lines := strings.Split(ansi.Strip(m.renderLibraryBody()), "\n")
			if len(lines) != rows {
				t.Fatalf("body is %d lines, want %d", len(lines), rows)
			}
			shown := len(m.libNote())
			if shown == 0 {
				t.Fatalf("no note in a %d-row body", rows)
			}
			for _, l := range lines[:shown] {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("note line is %d columns, panel is %d: %q", w, width, l)
				}
			}
			got := strings.Join(strings.Fields(strings.Join(lines[:shown], " ")), " ")
			if want := strings.Join(note, " "); got != want {
				t.Errorf("note reads %q, want all of %q", got, want)
			}
			if !strings.Contains(lines[shown], "Album") || m.libListBudget() != rows-shown {
				t.Errorf("list starts %q with budget %d, want rows %d less the note's %d", lines[shown], m.libListBudget(), rows, shown)
			}
			checkFrame(t, m, size.w, size.h)
		})
	}
}
