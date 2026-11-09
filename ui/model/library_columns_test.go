package model

// #63: every browsable row of a library list has the same fields: the
// detail starts at one cell whatever the title, rows end at one cell, and
// no row is wider than the panel. #64: what does not fit is cut at a word.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

const (
	colsLong      = "The Dark Side of the Moon Experience Immersion Box Set Remastered"
	colsCJK       = "夜に駆ける 夜に駆ける 夜に駆ける 夜に駆ける"
	colsCombining = "Café Tacvba Re Live Edition and the Unplugged Sessions"
	colsEmoji     = "👩‍🎤 Rock Star Anthology of the Seventies and Eighties"
	colsOneWord   = "Supercalifragilisticexpialidociousandthensomemoreletters"
)

// colsKinds are lists shaped like each library level: titles short, long,
// wide, with combining marks and emoji sequences, and details likewise.
func colsKinds() map[string][]library.Entry {
	open := library.Menu("x")
	titles := []string{"Abbey Road", colsLong, colsCJK, colsCombining, colsEmoji, colsOneWord, "Rumours"}
	row := func(title, detail string) library.Entry {
		return library.Entry{Title: title, Detail: detail, Open: open}
	}
	kinds := map[string][]library.Entry{}
	albumDetails := []string{"The Beatles · 1969", "Pink Floyd and the Orchestra of the Age of Enlightenment · 1973",
		"ヨアソビ · 2020", "Beyoncé · 2016", "👩‍🎤 Singer · 2001", "", "Fleetwood Mac · 1977"}
	for i, title := range titles {
		kinds["albums"] = append(kinds["albums"], row(title, albumDetails[i]))
		kinds["artists"] = append(kinds["artists"], row(title, fmt.Sprintf("%d albums", 1+i*7)))
		kinds["genres"] = append(kinds["genres"], row(title, []string{"3 albums", "", "120 albums"}[i%3]))
		kinds["playlists"] = append(kinds["playlists"], row(title, []string{"42 tracks", "Spotify · 1,204 tracks", "ヨアソビ mix"}[i%3]))
	}
	fav := row("Rumours", "Fleetwood Mac · 1977")
	fav.Favorite = true
	kinds["albums"] = append(kinds["albums"], fav)
	tr := playlist.Track{Path: "/m/1.mp3", Title: colsLong, DurationSecs: 300}
	for i, title := range titles {
		section := []string{"Albums", "Artists", "Playlists"}[i%3]
		kinds["search"] = append(kinds["search"], library.Entry{Title: title, Detail: []string{"Spotify", "Local · 3 albums", "YouTube · from tracks"}[i%3], Open: open, Section: section})
	}
	kinds["search"] = append(kinds["search"], library.Entry{Title: tr.Title, Detail: "Local", Track: &tr, Section: "Tracks"})
	return kinds
}

// colsField is the start cell of the field after a title (the first run
// of two or more spaces), or -1.
var colsField = regexp.MustCompile(`^(.*?\S)  +(\S)`)

// assertFieldCut fails unless shown is full, or a whole-word run of full
// with "…", or, when full's first word does not fit, a cut inside it.
func assertFieldCut(t *testing.T, shown, full string) {
	t.Helper()
	if shown == full {
		return
	}
	run, ok := strings.CutSuffix(shown, "…")
	whole := ok && strings.HasPrefix(full, run) && !strings.HasSuffix(run, " ") &&
		(run == "" || full[len(run)] == ' ' || !strings.Contains(run, " "))
	if !whole {
		t.Errorf("%q is not a whole-word cut of %q", shown, full)
	}
}

// checkColsRows checks one list's rendered rows (cursor cells included)
// against its entries: one detail start, one end, a word cut per field, and
// no row wider than panel.
func checkColsRows(t *testing.T, label string, lines []string, entries []library.Entry, panel int) {
	t.Helper()
	detailAt, endAt, i := -1, -1, 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue // the body's padding
		}
		if strings.HasPrefix(strings.TrimLeft(l, " "), "─") {
			continue // a section heading
		}
		content := l[2:] // the cursor cells
		e := entries[i]
		i++
		if w := lipgloss.Width(l); w > panel {
			t.Errorf("%s: row %q is %d cells, panel %d", label, l, w, panel)
		}
		if libIsTrackRow(e) {
			continue // label after the name (libTrackRow), not a field
		}
		body := strings.TrimRight(content, " ")
		if e.Open != nil {
			end := lipgloss.Width(body)
			if endAt >= 0 && end != endAt {
				t.Errorf("%s: rows end at %d and %d", label, endAt, end)
			}
			endAt = end
			body = strings.TrimRight(strings.TrimSuffix(body, " ›"), " ")
		}
		title := library.CleanText(e.Title)
		if e.Favorite {
			title = "★ " + title
		}
		if e.Detail == "" {
			assertFieldCut(t, body, title)
			continue
		}
		mm := colsField.FindStringSubmatchIndex(body)
		if mm == nil {
			t.Errorf("%s: row %q: no detail field", label, body)
			continue
		}
		at := lipgloss.Width(body[:mm[4]])
		if detailAt >= 0 && at != detailAt {
			t.Errorf("%s: details start at %d and %d: %q", label, detailAt, at, body)
		}
		detailAt = at
		if at < 4+libDetailGap {
			t.Errorf("%s: the title field is %d cells", label, at-libDetailGap)
		}
		assertFieldCut(t, body[:mm[3]], title)
		assertFieldCut(t, body[mm[4]:], e.Detail)
	}
	if i != len(entries) {
		t.Fatalf("%s: %d rows for %d entries", label, i, len(entries))
	}
}

func TestLibraryFieldsStayFixed(t *testing.T) {
	for kind, entries := range colsKinds() {
		for _, w := range []int{40, 56, 80, 100, 120, 300} {
			label := fmt.Sprintf("%s at %d", kind, w)
			m := resized(t, newLibraryModel(library.Menu("Music", entries...)), w, 40)
			checkFrame(t, m, w, 40)
			lines := strings.Split(stripAnsi(m.renderLibraryList(30)), "\n")
			checkColsRows(t, label, lines, entries, ui.PanelWidth)
		}
	}
}

// The library's lists draw in the frame, not the two-column playback
// screen's narrower list column, but its rows hold at that width too.
func TestLibraryFieldsAtTheTwoColumnWidth(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 48}} {
		panel := newColumnTestModel(size[0], size[1]).layout.playlistWidth
		for kind, entries := range colsKinds() {
			cols := measureLibCols(entries)
			restore := ui.WithPanelWidth(panel)
			var lines []string
			for i, e := range entries {
				lines = append(lines, stripAnsi(cursorLine(libEntryLabel(e, i+1, cols), false)))
			}
			restore()
			checkColsRows(t, fmt.Sprintf("%s at the %dx%d list column (%d)", kind, size[0], size[1], panel), lines, entries, panel)
		}
	}
}

// The measure and the render are one rule: at its own table width a list's
// fields are its columns.
func TestLibColsTableIsItsColumns(t *testing.T) {
	for kind, entries := range colsKinds() {
		c := measureLibCols(entries)
		title, detail := c.fields(c.table() - libCursorPrefix - c.suffixWidth())
		if title != max(c.title, 4) || detail != c.detail {
			t.Errorf("%s: fields at the table = %d, %d; columns %d, %d", kind, title, detail, c.title, c.detail)
		}
	}
}
