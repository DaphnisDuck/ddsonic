package model

// ddsonic: library rows shorten a title or detail at a word boundary (#64).
// The general truncate (view_helpers.go) still cuts at the cell limit for
// everything else: inputs with a cursor, errors, the queue.

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// truncateWords shortens s to maxW cells for a library row's field: as many
// whole words as fit with "…" after the last, with no space before it. The
// word that does not fit is left out whole, even if that leaves room, and so
// is a separator ("·", "-") it would leave last. Text that fits is
// unchanged. When not even the first word fits, or the text has no space to
// break at (CJK, say), it is cut as truncate cuts it, at a grapheme cluster
// within maxW cells, but not after a space. ANSI styling is kept and reset as
// truncate keeps it.
func truncateWords(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= maxW {
		return s
	}
	plain := ansi.Strip(s)
	keep := 0 // the width of the longest whole-word run that fits with "…"
	for i := range len(plain) {
		if plain[i] != ' ' {
			continue
		}
		w := ansi.StringWidth(wordRun(plain[:i]))
		if w+1 > maxW {
			break
		}
		keep = max(keep, w)
	}
	if keep == 0 {
		// No whole word fits: cut at a cluster as truncate does, less any
		// space the cut leaves last.
		keep = ansi.StringWidth(strings.TrimRight(ansi.Truncate(plain, maxW-1, ""), " "))
	}
	// keep ends at a cluster boundary, so this cuts exactly there.
	return ansi.Truncate(s, keep+1, "…")
}

// wordRun is prefix without trailing spaces, or trailing words made only of
// punctuation: "Artist ·" is "Artist".
func wordRun(prefix string) string {
	for {
		prefix = strings.TrimRight(prefix, " ")
		cut := strings.LastIndexByte(prefix, ' ')
		last := prefix[cut+1:]
		if last == "" || strings.IndexFunc(last, func(r rune) bool { return !unicode.IsPunct(r) }) >= 0 {
			return prefix
		}
		prefix = prefix[:max(cut, 0)]
	}
}
