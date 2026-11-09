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
	for i, title := range titles {
		section := []string{"Albums", "Artists", "Playlists"}[i%3]
		kinds["search"] = append(kinds["search"], library.Entry{Title: title, Detail: []string{"Spotify", "Local · 3 albums", "YouTube · from tracks"}[i%3], Open: open, Section: section})
	}
	// Track rows: an album's (no label, numbers past 9, durations of
	// every width and none) and search results' (each with its source).
	secs := []int{245, 3725, 0, 61, 599, 1200, 30}
	for n := range 12 {
		tr := playlist.Track{Path: fmt.Sprint("/m/", n), Title: titles[n%len(titles)], DurationSecs: secs[n%len(secs)]}
		kinds["album tracks"] = append(kinds["album tracks"], library.Entry{Title: tr.Title, Track: &tr})
	}
	for i, title := range titles {
		tr := playlist.Track{Path: fmt.Sprint("/s/", i), Title: title, DurationSecs: secs[i]}
		kinds["search"] = append(kinds["search"], library.Entry{Title: title, Detail: []string{"Local", "YouTube · from tracks", "Spotify", "ヨアソビ"}[i%4], Track: &tr, Section: "Tracks"})
	}
	return kinds
}

// colsDuration is a track row's duration at its end, and colsNumber its
// number.
var (
	colsDuration = regexp.MustCompile(` (\d+:)?\d+:\d\d$`)
	colsNumber   = regexp.MustCompile(`^ *\d+\. `)
)

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
// against its entries: one detail start and one end for its browsable rows;
// one name, source and duration start for its track rows; a word cut per
// field; and no row wider than panel.
func checkColsRows(t *testing.T, label string, lines []string, entries []library.Entry, panel int) {
	t.Helper()
	detailAt, endAt, i := -1, -1, 0
	nameAt, labelAt, durationAt := -1, -1, -1
	same := func(what string, at *int, got int, row string) {
		if *at >= 0 && got != *at {
			t.Errorf("%s: %s start at %d and %d: %q", label, what, *at, got, row)
		}
		*at = got
	}
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
		body := strings.TrimRight(content, " ")
		if libIsTrackRow(e) {
			if d := colsDuration.FindStringIndex(body); d != nil {
				same("durations", &durationAt, lipgloss.Width(body[:d[0]+1]), body)
				body = strings.TrimRight(body[:d[0]], " ")
			} else if e.Track.DurationSecs > 0 {
				t.Errorf("%s: row %q has no duration", label, body)
			}
			num := colsNumber.FindString(body)
			if num == "" || strings.TrimSpace(num) != fmt.Sprintf("%d.", libTrackNumbers(entries)[i-1]) {
				t.Errorf("%s: row %q does not start with its number", label, body)
				continue
			}
			same("names", &nameAt, lipgloss.Width(num), body)
			body = body[len(num):]
			name := library.CleanText(trackViewName(*e.Track))
			if e.Detail == "" {
				assertFieldCut(t, body, name)
				continue
			}
			mm := colsField.FindStringSubmatchIndex(body)
			if mm == nil {
				t.Errorf("%s: row %q: no source field", label, body)
				continue
			}
			same("sources", &labelAt, lipgloss.Width(body[:mm[4]]), body)
			assertFieldCut(t, body[:mm[3]], name)
			assertFieldCut(t, body[mm[4]:], e.Detail)
			continue
		}
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
		same("details", &detailAt, at, body)
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
			var f libFrame
			f.setEntries(entries)
			restore := ui.WithPanelWidth(panel)
			var lines []string
			for i, e := range entries {
				cols := f.cols
				if libIsTrackRow(e) {
					cols = f.trackCols
				}
				lines = append(lines, stripAnsi(cursorLine(libEntryLabel(e, f.numbers[i], cols), false)))
			}
			restore()
			checkColsRows(t, fmt.Sprintf("%s at the %dx%d list column (%d)", kind, size[0], size[1], panel), lines, entries, panel)
		}
	}
}

// The measure and the render are one rule: at its own table width a list's
// fields hold its widest title and detail, so a row as wide as the table
// cuts nothing (#133; under #63 the table was the 9-in-10 columns).
func TestLibColsTableIsItsColumns(t *testing.T) {
	for kind, entries := range colsKinds() {
		var f libFrame
		f.setEntries(entries)
		for _, c := range []libCols{f.cols, f.trackCols} {
			if c.titleMax == 0 {
				continue // no rows of this kind
			}
			title, detail := c.fields(c.table() - libCursorPrefix)
			if title != max(c.titleMax, 4) || detail != c.detailMax {
				t.Errorf("%s: fields at the table = %d, %d; widest %d, %d", kind, title, detail, c.titleMax, c.detailMax)
			}
		}
	}
}
