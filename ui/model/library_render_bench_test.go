package model

// ddsonic: rendering a long library list costs the rows in view, not the list
// (review P2). Run with go test ./ui/model -run '^$' -bench LibraryList -benchmem.

import (
	"context"
	"fmt"
	"testing"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

func BenchmarkLibraryList(b *testing.B) {
	for _, n := range []int{100, 10_000, 100_000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			entries := make([]library.Entry, n)
			for i := range entries {
				tr := playlist.Track{Path: fmt.Sprint(i), Title: fmt.Sprint("Track ", i), DurationSecs: 200}
				entries[i] = library.Entry{ID: fmt.Sprint(i), Title: tr.Title, Track: &tr}
			}
			root := library.NewLevel("Big", func(context.Context) ([]library.Entry, error) { return entries, nil })
			m := newLibraryModel(root)
			m = libRun(b, m, m.libraryLoad())
			m.libTop().cursor = n / 2
			m.libAdjustScroll()
			b.ResetTimer()
			for b.Loop() {
				m.renderLibraryList(25)
			}
		})
	}
}

// The layout cached with the entries is the one a render would work out:
// headings between sections, each entry's line, and per-section numbers.
func TestLibraryLayoutCached(t *testing.T) {
	tr := playlist.Track{Path: "t", Title: "t"}
	entries := []library.Entry{
		{ID: "a", Title: "a", Section: "Albums"},
		{ID: "b", Title: "b", Section: "Albums"},
		{ID: "1", Title: "1", Section: "Tracks", Track: &tr},
		{ID: "2", Title: "2", Section: "Tracks", Track: &tr},
	}
	var f libFrame
	f.setEntries(entries)
	rows, numbers, rowOf := f.libLayout()
	fresh := libraryRows(entries)
	if fmt.Sprint(rows) != fmt.Sprint(fresh) || fmt.Sprint(numbers) != fmt.Sprint(libTrackNumbers(entries)) {
		t.Errorf("cached layout %v %v, want %v %v", rows, numbers, fresh, libTrackNumbers(entries))
	}
	// Albums heading, a, b, Tracks heading, 1, 2.
	if fmt.Sprint(rowOf) != "[1 2 4 5]" {
		t.Errorf("rowOf = %v, want each entry's line past the headings", rowOf)
	}
	// A frame given entries another way still lays out.
	g := libFrame{entries: entries}
	if r, _, ro := g.libLayout(); len(r) != 6 || fmt.Sprint(ro) != "[1 2 4 5]" {
		t.Errorf("uncached layout = %v %v", r, ro)
	}
}
