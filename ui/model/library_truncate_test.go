package model

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// #64: whole words with "…", the next word left out whole; a cluster-safe
// cell cut when no word fits; never wider than the field.
func TestTruncateWords(t *testing.T) {
	const (
		family = "👨‍👩‍👧" // one cluster, 2 cells
		bold   = "\x1b[1m"
		reset  = "\x1b[0m"
	)
	for _, tt := range []struct {
		name, s string
		w       int
		want    string
	}{
		{"fits unchanged", "Rumours", 12, "Rumours"},
		{"fits exactly", "Abbey Road", 10, "Abbey Road"},
		{"whole words", "Dark Side of the Moon", 12, "Dark Side…"},
		{"next word left out though room stays", "A Supercalifragilistic", 12, "A…"},
		{"no space before the ellipsis", "Abbey Road Remastered", 11, "Abbey Road…"},
		{"spaces inside the run kept", "Abbey  Road  Remastered", 12, "Abbey  Road…"},
		{"a separator is not left last", "Pink Floyd · 1973", 13, "Pink Floyd…"},
		{"oversized word", "Supercalifragilistic", 12, "Supercalifr…"},
		{"oversized first word", "Supercalifragilistic Expialidocious", 12, "Supercalifr…"},
		{"no spaces: CJK", "日本語のタイトル", 9, "日本語の…"},
		{"CJK, odd width", "日本語のタイトル", 10, "日本語の…"},
		{"CJK words", "東京事変 大人 (アダルト) 完全版", 12, "東京事変…"},
		{"combining mark in a word", "Beyoncé Lemonade Deluxe", 12, "Beyoncé…"},
		{"combining marks not split", "éééé", 3, "éé…"},
		{"emoji sequence word", family + " Family Album Remaster", 12, family + " Family…"},
		{"emoji sequence not split", family + family, 3, family + "…"},
		{"emoji sequence wider than the room", family + family, 2, "…"},
		{"wide character wider than the room", "日本", 2, "…"},
		{"ANSI kept and reset", bold + "Bold Title Here" + reset + " Tail", 12, bold + "Bold Title…" + reset},
		{"width 0", "Abbey Road", 0, ""},
		{"width 1", "Abbey Road", 1, "…"},
		{"width 2", "Abbey Road", 2, "A…"},
		{"width 2, fits", "Ab", 2, "Ab"},
		{"cut after a separator, less the space", "- Live", 3, "-…"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateWords(tt.s, tt.w)
			if got != tt.want {
				t.Errorf("truncateWords(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.want)
			}
			if w := lipgloss.Width(got); w > max(tt.w, 0) {
				t.Errorf("truncateWords(%q, %d) is %d cells wide", tt.s, tt.w, w)
			}
		})
	}
}

// truncate, which every other view uses, still cuts at the cell limit
// (#64: only library rows change). These are its results before #64,
// mid-word cuts and the space before "…" included.
func TestTruncateUnchanged(t *testing.T) {
	for _, tt := range []struct {
		s    string
		w    int
		want string
	}{
		{"Rumours", 12, "Rumours"},
		{"Dark Side of the Moon", 12, "Dark Side o…"},
		{"Beyoncé Lemonade Deluxe", 9, "Beyoncé …"},
		{"Beyoncé Lemonade Deluxe", 12, "Beyoncé Lem…"},
		{"Supercalifragilistic", 12, "Supercalifr…"},
		{"東京事変 大人 (アダルト) 完全版", 2, "…"},
		{"東京事変 大人 (アダルト) 完全版", 12, "東京事変 大…"},
		{"👨‍👩‍👧 Family Album Remaster", 9, "👨‍👩‍👧 Famil…"},
		{"\x1b[1mBold Title Here\x1b[0m Tail", 12, "\x1b[1mBold Title …\x1b[0m"},
		{"Abbey Road", 0, ""},
		{"Abbey Road", 1, "…"},
		{"Abbey Road", 2, "A…"},
	} {
		if got := truncate(tt.s, tt.w); got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.w, got, tt.want)
		}
	}
}

// assertWordCut fails unless shown is full, or a run of full's whole words
// with "…" and no space before it.
func assertWordCut(t *testing.T, shown, full string) {
	t.Helper()
	if shown == full {
		return
	}
	run, ok := strings.CutSuffix(shown, "…")
	if !ok || run == "" || strings.HasSuffix(run, " ") || !strings.HasPrefix(full, run) || full[len(run)] != ' ' {
		t.Errorf("%q is not a whole-word cut of %q", shown, full)
	}
}

// #64: a library row's title and detail, and a search track row's name,
// are cut at a word, at every width.
func TestLibraryRowsCutAtWords(t *testing.T) {
	const title = "The Dark Side of the Moon Experience Immersion Box Set Remastered"
	const detail = "Pink Floyd and the Orchestra of the Age of Enlightenment · 1973"
	track := playlist.Track{Path: "/m/1.mp3", Title: "Shine On You Crazy Diamond Parts One Through Nine Live", DurationSecs: 300}
	for _, panel := range []int{40, 56, 80, 100} {
		defer ui.WithPanelWidth(panel)()
		row := libEntryLabel(library.Entry{Title: title, Detail: detail, Open: library.Menu("x")}, 0, 0)
		row = strings.TrimSuffix(row, " ›")
		fields := regexp.MustCompile(`  +`).Split(row, -1)
		if len(fields) != 2 {
			t.Fatalf("panel %d: row %q: want a title and a detail", panel, row)
		}
		assertWordCut(t, fields[0], title)
		assertWordCut(t, fields[1], detail)
		if w := lipgloss.Width(row); w > panel-libCursorPrefix {
			t.Errorf("panel %d: row is %d cells", panel, w)
		}
		tr := libEntryLabel(library.Entry{Title: track.Title, Detail: "YouTube", Track: &track}, 1, 0)
		name := strings.Split(strings.TrimPrefix(tr, "1. "), "  ")[0]
		assertWordCut(t, name, track.Title)
	}
}
