package model

// ddsonic: a library list's text uses the room the terminal gives it, up to
// libTableMax, before it is cut (#133), and keeps #63's fixed columns.

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// widthShapes are lists shaped like the four views of #133, with their real
// details, and the other library lists (colsKinds).
func widthShapes() map[string][]library.Entry {
	open := library.Menu("x")
	shapes := colsKinds()
	artists := []string{"Ólafur Arnalds", "Godspeed You! Black Emperor", "The Orchestra of the Age of Enlightenment",
		"Wolfgang Amadeus Mozart, Vienna Philharmonic and Karl Böhm", "坂本龍一", "Beyoncé", "Nick Cave & The Bad Seeds",
		"Florence + The Machine", "Berliner Philharmoniker, Herbert von Karajan", "👩‍🎤 Singer and the Band", "Sigur Rós", "ヨアソビ"}
	sources := []string{"Spotify", "Local", "YouTube · from tracks"}
	for i, a := range artists {
		shapes["All Music artists"] = append(shapes["All Music artists"], library.Entry{Title: a, Detail: sources[i%3], Open: open})
	}
	albums := []string{"The Rise and Fall of Ziggy Stardust and the Spiders from Mars (2012 Remaster)", "Hunky Dory", "Low",
		"Station to Station (Deluxe Edition)", "Blackstar", "Aladdin Sane (50th Anniversary Half-Speed Master)",
		"Symphony No. 9 in D minor, Op. 125 \"Choral\": Live at the Royal Albert Hall with the Philharmonia Chorus (2023 Remaster)", "夜に駆ける 夜に駆ける"}
	for i, a := range albums {
		shapes["Spotify artist albums"] = append(shapes["Spotify artist albums"], library.Entry{Title: a, Detail: fmt.Sprint(1969 + i), Open: open})
	}
	shapes["Spotify artist albums"] = append(shapes["Spotify artist albums"], library.Entry{Title: "Full discography…", Open: open})
	names := []string{"Bohemian Rhapsody - Remastered 2011", "Shine On You Crazy Diamond (Pts. 1-5) [2011 Remastered Version]", "Hey Jude",
		"夜に駆ける", "Concerto for Two Violins in D minor, BWV 1043: I. Vivace - Academy of St Martin in the Fields, Sir Neville Marriner, 1972 Recording",
		"Café del Mar", "One", "👩‍🎤 Anthem", "Unfinished Sympathy - 2012 Mix/Master", "Hurt", "Roygbiv"}
	for i, n := range names {
		tr := playlist.Track{Path: fmt.Sprint("liked/", i), Title: n, DurationSecs: 125 + i*331}
		shapes["Liked Songs"] = append(shapes["Liked Songs"], library.Entry{Title: n, Track: &tr})
	}
	ytAlbums := []string{"Random Access Memories", "In Rainbows", "To Pimp a Butterfly", "Kid A Mnesia",
		"The Dark Side of the Moon (50th Anniversary) [2023 Remaster]", "Ágætis byrjun", "Music for 18 Musicians", "Blonde",
		"Songs of Innocence & Experience", "A Moon Shaped Pool", "Currents", "夜に駆ける"}
	ytArtists := []string{"Daft Punk", "Radiohead", "Kendrick Lamar", "Radiohead", "Pink Floyd", "Sigur Rós", "Steve Reich and Musicians",
		"Frank Ocean", "Johann Sebastian Bach, Academy of St Martin in the Fields & Sir Neville Marriner", "Radiohead", "Tame Impala", "ヨアソビ"}
	for i, a := range ytAlbums {
		shapes["YouTube Music albums from tracks"] = append(shapes["YouTube Music albums from tracks"], library.Entry{Title: a, Detail: ytArtists[i], Open: open})
	}
	return shapes
}

// widthSizes are the terminal sizes #133 names, and narrow ones.
var widthSizes = [][2]int{{40, 24}, {56, 24}, {80, 24}, {120, 30}, {160, 45}, {200, 50}, {300, 50}}

// listRowsEnd is the cell where the widest of rendered rows ends, headings
// and blank lines aside.
func listRowsEnd(lines []string) (end int) {
	for _, l := range lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimLeft(l, " "), "─") {
			continue
		}
		end = max(end, lipgloss.Width(strings.TrimRight(l, " ")))
	}
	return end
}

// #133: text is cut only where the row has no room left: a list whose rows
// cut text ends at the room (the panel, up to libTableMax), and one that cuts
// nothing ends at its content. The fields hold still from row to row (#63),
// and no row is wider than the room.
func TestLibraryRowsUseTheWidthBeforeCutting(t *testing.T) {
	for kind, entries := range widthShapes() {
		for _, sz := range widthSizes {
			label := fmt.Sprintf("%s at %dx%d", kind, sz[0], sz[1])
			m := resized(t, newLibraryModel(library.Menu("Music", entries...)), sz[0], sz[1])
			panel := ui.PanelWidth
			room := min(panel, libTableMax) - 2 // the cursor's two reserved cells
			lines := strings.Split(stripAnsi(m.renderLibraryList(len(entries)+10)), "\n")
			checkColsRows(t, label, lines, entries, room)
			// Per kind: browsable rows and track rows have fields of their own.
			for _, tracks := range []bool{false, true} {
				var kindLines []string
				cut := false
				row := 0
				for _, l := range lines {
					if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimLeft(l, " "), "─") {
						continue
					}
					e := entries[row]
					row++
					if libIsTrackRow(e) != tracks {
						continue
					}
					kindLines = append(kindLines, l)
					shown := strings.TrimRight(l, " ")
					cut = cut || strings.Contains(strings.ReplaceAll(shown, "Full discography…", ""), "…")
				}
				if len(kindLines) == 0 {
					continue
				}
				end := listRowsEnd(kindLines)
				if end > room {
					t.Errorf("%s: rows end at %d, past the room %d", label, end, room)
				}
				if cut && end != room {
					t.Errorf("%s: text is cut while the rows end at %d of %d", label, end, room)
				}
			}
		}
	}
}

// One long detail among short ones does not take the title's room while
// there is some: the title field keeps what the titles need up to the room
// left after 9 in 10 details (#133; it was the 9-in-10 title column).
func TestLibraryOutlierDetailLeavesTheTitlesRoom(t *testing.T) {
	entries := widthShapes()["YouTube Music albums from tracks"]
	for _, sz := range widthSizes {
		m := resized(t, newLibraryModel(library.Menu("Music", entries...)), sz[0], sz[1])
		f := m.libTop()
		width := libRowWidth(ui.PanelWidth, f.cols.table())
		title, detail := f.cols.fields(width - libCursorPrefix)
		avail := width - libCursorPrefix - f.cols.tail - libDetailGap
		if want := min(f.cols.titleMax, avail-min(f.cols.detail, (avail+libDetailGap)/2)); title < max(want, 4) {
			t.Errorf("%dx%d: title field %d (detail %d), want at least %d: the long detail took the titles' room",
				sz[0], sz[1], title, detail, want)
		}
	}
}

// One long title among short ones does not take the details' room: 9 in 10
// details are shown whole while the row has room for them.
func TestLibraryOutlierTitleLeavesTheDetailsRoom(t *testing.T) {
	open := library.Menu("x")
	entries := []library.Entry{{Title: strings.Repeat("Very Long Title ", 12), Detail: "Artist · 1999", Open: open}}
	for i := range 12 {
		entries = append(entries, library.Entry{Title: fmt.Sprint("Album ", i), Detail: "Some Artist Name · 2001", Open: open})
	}
	for _, sz := range widthSizes {
		m := resized(t, newLibraryModel(library.Menu("Music", entries...)), sz[0], sz[1])
		f := m.libTop()
		width := libRowWidth(ui.PanelWidth, f.cols.table())
		_, detail := f.cols.fields(width - libCursorPrefix)
		avail := width - libCursorPrefix - f.cols.tail
		if want := min(f.cols.detail, avail/2); detail < want {
			t.Errorf("%dx%d: detail field %d, want at least %d", sz[0], sz[1], detail, want)
		}
	}
}

// Past libTableMax the rows stop: a list that needs more is cut to it, with
// the rest of a wide terminal left empty (#133).
func TestLibraryRowsStopAtTheTableMax(t *testing.T) {
	entries := widthShapes()["Liked Songs"]
	long := playlist.Track{Path: "liked/long", Title: strings.Repeat("A Very Long Track Name ", 9), DurationSecs: 600} // 207 cells
	entries = append(entries, library.Entry{Title: long.Title, Track: &long})
	m := resized(t, newLibraryModel(library.Menu("Music", entries...)), 300, 50)
	lines := strings.Split(stripAnsi(m.renderLibraryList(len(entries))), "\n")
	if got := listRowsEnd(lines); got != libTableMax-2 {
		t.Errorf("rows end at %d, want %d (libTableMax less the cursor's reserved cells)", got, libTableMax-2)
	}
}
