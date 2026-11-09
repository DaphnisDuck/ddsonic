package model

// ddsonic: how wide list rows run. Up to listReadWidth they fill the width, as
// cliamp draws them; past it they form a table (docs/ddsonic/layout.md). A
// library level measures its rows once, when its entries arrive; the queue
// once per playlist change. Rendering narrows ui.PanelWidth per row with
// ui.WithPanelWidth, so cliamp's row formatters need no change.

import (
	"fmt"
	"slices"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// listReadWidth is how wide list rows grow with the terminal before they
// stop at their content: up to it, rows fill the width as they always have.
// Past it, a list's rows end where most of them fit (listColumn), so a title
// and its detail stay within reach of the eye instead of a screen apart. A
// library level's browsable rows all end at its table (libCols); a longer
// track row runs past the column as far as it needs.
const listReadWidth = 100

// listColumnShare is the share of a list's rows that fit in its column; the
// rest run past it.
const listColumnShare = 0.9

// listRowWidth returns the width one row fills in panel columns: the
// list's column, or the row's own need when longer, within the panel.
func listRowWidth(panel, column, need int) int {
	if panel <= listReadWidth {
		return panel
	}
	return min(panel, max(listReadWidth, column, need))
}

// listColumn returns the width that listColumnShare of needs fit in.
func listColumn(needs []int) int {
	if len(needs) == 0 {
		return 0
	}
	sorted := slices.Sorted(slices.Values(needs))
	return sorted[int(float64(len(sorted)-1)*listColumnShare)]
}

// libCursorPrefix is the "> " cursor and its gap before each library row.
const libCursorPrefix = 4

// libEntryTitleWidth is the width of entry's title as its row shows it.
func libEntryTitleWidth(e library.Entry) int {
	w := lipgloss.Width(library.CleanText(e.Title))
	if e.Favorite {
		w += 2 // "★ "
	}
	return w
}

// libTrackLabelGap is the space between a track row's name and its label
// (libTrackRow).
const libTrackLabelGap = 2

// libIsTrackRow reports whether entry renders as a numbered track row.
func libIsTrackRow(e library.Entry) bool { return e.Track != nil && !e.Track.Realtime }

// libDetailGap is the space between a browsable row's title and detail
// fields.
const libDetailGap = 2

// libCols is what a library level measures of its browsable rows to lay out
// their fields (#63): the width of the widest title and detail, and the
// columns listColumnShare of them fit (listColumn); detail is 0 when no row
// has one. opens is whether a row ends in " ›".
type libCols struct {
	title, titleMax, detail, detailMax int
	opens                              bool
}

// measureLibCols measures entries' browsable rows.
func measureLibCols(entries []library.Entry) libCols {
	var titles, details []int
	var c libCols
	for _, e := range entries {
		if libIsTrackRow(e) {
			continue
		}
		titles = append(titles, libEntryTitleWidth(e))
		if d := library.CleanText(e.Detail); d != "" {
			details = append(details, lipgloss.Width(d))
		}
		c.opens = c.opens || e.Open != nil
	}
	c.title, c.detail = listColumn(titles), listColumn(details)
	if len(titles) > 0 {
		c.titleMax = slices.Max(titles)
	}
	if len(details) > 0 {
		c.detailMax = slices.Max(details)
	}
	return c
}

func (c libCols) suffixWidth() int {
	if c.opens {
		return 2 // " ›"
	}
	return 0
}

// fields returns the title and detail fields' widths in a row with avail
// cells between the cursor prefix and the " ›". Every browsable row of a list
// has the same fields, so its detail starts at the same cell whatever the
// title's length. The title field is as wide as the row's room after the
// widest detail allows, but no wider than the widest title, and no narrower
// than the title column, so one long title or detail does not move the
// column for the rest. It leaves the detail field the detail column, up to
// half the row, and keeps at least 4 cells (cliamp's minimum title). The
// detail field is the rest of the row. A list without details has only the
// title field.
func (c libCols) fields(avail int) (title, detail int) {
	avail = max(avail, 0)
	if c.detail == 0 {
		return avail, 0
	}
	reserve := min(c.detail, avail/2)
	title = min(max(c.title, avail-libDetailGap-c.detailMax), c.titleMax, avail-libDetailGap-reserve)
	title = min(max(title, 4), avail)
	return title, max(avail-libDetailGap-title, 0)
}

// table is the width the list's browsable rows need: its title and detail
// columns (fields gives them exactly at this width), the cursor prefix and
// the " ›".
func (c libCols) table() int {
	w := libCursorPrefix + c.title + c.suffixWidth()
	if c.detail > 0 {
		w += libDetailGap + c.detail
	}
	return w
}

// libEntryNeed is the width entry's row needs on a wide terminal: a
// browsable row the list's table, whatever its own title; number is a
// track's number.
func libEntryNeed(e library.Entry, number int, cols libCols) int {
	if libIsTrackRow(e) {
		need := libCursorPrefix + lipgloss.Width(fmt.Sprintf("%d. ", number)) + trackRowNeed(library.CleanText(trackViewName(*e.Track)), *e.Track, false)
		if d := library.CleanText(e.Detail); d != "" {
			need += libTrackLabelGap + lipgloss.Width(d) // a search result's source, after the name
		}
		return need
	}
	return cols.table()
}

// setEntries sets the frame's rows and measures them for listRowWidth: the
// fields of its browsable rows, each row's need, and the list's column.
func (f *libFrame) setEntries(entries []library.Entry) {
	f.entries = entries
	f.cols = measureLibCols(entries)
	numbers := libTrackNumbers(entries)
	f.rows, f.numbers = libraryRows(entries), numbers
	f.rowOf = libRowOf(f.rows, len(entries))
	f.needs = make([]int, len(entries))
	for i, e := range entries {
		f.needs[i] = libEntryNeed(e, numbers[i], f.cols)
	}
	f.column = listColumn(f.needs)
}

// trackRowNeed is the width of track t's row after its prefix: its name as
// the row shows it, the album suffix when there are no album headers, and
// the duration.
func trackRowNeed(name string, t playlist.Track, albumSuffix bool) int {
	w := lipgloss.Width(name)
	if albumSuffix && t.Album != "" {
		w += 3 + lipgloss.Width(t.Album) // " · album"
	}
	if t.Unplayable {
		w += len(" (unavailable)")
	}
	if d := formatTrackTime(t.DurationSecs); d != "" {
		w += 1 + lipgloss.Width(d)
	}
	return w
}

// trackColumn is listColumn over tracks' rows.
func trackColumn(tracks []playlist.Track, albumSuffix bool) int {
	needs := make([]int, len(tracks))
	for i, t := range tracks {
		needs[i] = trackRowNeed(trackViewName(t), t, albumSuffix)
	}
	return listColumn(needs)
}

// libTrackRowWidth is the width track t's row fills after prefix cells in
// a track list whose column is column.
func (m Model) libTrackRowWidth(t playlist.Track, prefix, column int, albumSuffix bool) int {
	if !m.libraryEnabled() {
		return ui.PanelWidth
	}
	return listRowWidth(ui.PanelWidth, prefix+column, prefix+trackRowNeed(trackViewName(t), t, albumSuffix))
}

// trackFit caches a track list's column, which needs a pass over all its
// tracks, until the playlist changes.
type trackFit struct {
	valid    bool
	revision uint64
	headers  bool
	length   int
	column   int
}

// get returns the column for tracks at revision, measuring them with fetch
// when the cached one is stale.
func (c *trackFit) get(revision uint64, headers bool, length int, fetch func() []playlist.Track) int {
	if !c.valid || c.revision != revision || c.headers != headers || c.length != length {
		*c = trackFit{valid: true, revision: revision, headers: headers, length: length, column: trackColumn(fetch(), !headers)}
	}
	return c.column
}

// libQueueColumn returns the queue's column and the prefix of its rows
// (renderPlaylist) after prefix cells of markers and track number, once per
// render; column is 0 where rows fill the panel.
func (m Model) libQueueColumn(prefix int) (column, rowPrefix int) {
	if n := m.playlist.QueueLen(); n > 0 {
		prefix += len(fmt.Sprintf(" [Q%d]", n))
	}
	if !m.libraryEnabled() || m.lib.fits == nil || ui.PanelWidth <= listReadWidth {
		return 0, prefix
	}
	return m.lib.fits.queue.get(m.playlist.Revision(), m.showAlbumHeaders, m.playlist.Len(), m.playlist.Tracks), prefix
}

// libUpNextColumn returns Up next's column (renderQueueBody): over every
// queued track, so it holds still while the list scrolls.
func (m Model) libUpNextColumn() int {
	if !m.libraryEnabled() || m.lib.fits == nil || ui.PanelWidth <= listReadWidth {
		return 0
	}
	n := m.playlist.QueueLen()
	return m.lib.fits.upNext.get(m.playlist.Revision(), true, n, func() []playlist.Track { return m.playlist.QueueWindow(0, n) })
}

// libFits holds the queue's and Up next's cached columns.
type libFits struct{ queue, upNext trackFit }

// width is the cells renderPlaylist's markers take: cursor, state, each
// reserved column, and a space.
func (c markerColumns) width() int {
	w := 3
	for _, on := range []bool{c.queue, c.bookmark, c.favorite, c.played} {
		if on {
			w++
		}
	}
	return w
}
