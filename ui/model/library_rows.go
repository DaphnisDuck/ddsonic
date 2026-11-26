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
// library level's rows of each kind all end at its table (libCols); a
// longer queue row runs past the column as far as it needs.
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

// libIsTrackRow reports whether entry renders as a numbered track row.
func libIsTrackRow(e library.Entry) bool { return e.Track != nil && !e.Track.Realtime }

// libDetailGap is the space between a row's title and detail fields.
const libDetailGap = 2

// libCols is what a library level measures of one kind of its rows, the
// browsable rows or the track rows, to lay out their fields (#63): the
// widest title and detail, and the columns listColumnShare of them fit
// (listColumn); detail is 0 when no row has one. A track's title is its name
// and its detail its source (a search result's). lead is the field before
// the title (a track's number) and tail the one after the detail (" ›", or
// a space and a track's duration). detailFirst keeps the detail its column
// whole before the title, as a track row keeps its source.
type libCols struct {
	title, titleMax, detail, detailMax int
	lead, tail                         int
	detailFirst                        bool
}

// measureCols sets c's title and detail columns from their widths.
func (c *libCols) measureCols(titles, details []int) {
	c.title, c.detail = listColumn(titles), listColumn(details)
	if len(titles) > 0 {
		c.titleMax = slices.Max(titles)
	}
	if len(details) > 0 {
		c.detailMax = slices.Max(details)
	}
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
		if e.Open != nil {
			c.tail = 2 // " ›"
		}
	}
	c.measureCols(titles, details)
	return c
}

// measureTrackCols measures entries' track rows, numbered by numbers.
func measureTrackCols(entries []library.Entry, numbers []int) libCols {
	var titles, details []int
	c := libCols{detailFirst: true}
	for i, e := range entries {
		if !libIsTrackRow(e) {
			continue
		}
		titles = append(titles, lipgloss.Width(library.CleanText(trackViewName(*e.Track))))
		if d := library.CleanText(e.Detail); d != "" {
			details = append(details, lipgloss.Width(d))
		}
		c.lead = max(c.lead, lipgloss.Width(fmt.Sprintf("%d. ", numbers[i])))
		if d := formatTrackTime(e.Track.DurationSecs); d != "" {
			c.tail = max(c.tail, 1+lipgloss.Width(d))
		}
	}
	c.measureCols(titles, details)
	return c
}

// fields returns the title and detail fields' widths in a row of width
// cells after the cursor prefix. Every row of a kind in a list has the same
// fields, so its detail and tail start at the same cell whatever the
// title's length (#63). The detail keeps the detail column, up to half the
// row (detailFirst: all but 4 cells), and more where the titles leave room,
// up to the widest detail; the title takes the rest, up to the widest title
// and at least 4 cells (cliamp's minimum title); the detail field is what
// is left. So text is cut only where the row has no room left for it, and
// one long title or detail does not take the other's room while there is
// some (#133). A list without details has only the title field.
func (c libCols) fields(width int) (title, detail int) {
	avail := max(width-c.lead-c.tail, 0)
	if c.detail == 0 {
		return avail, 0
	}
	hi := avail / 2
	if c.detailFirst {
		hi = avail - libDetailGap - 4
	}
	detail = max(min(c.detailMax, hi, max(c.detail, avail-libDetailGap-c.titleMax)), 0)
	title = min(max(min(c.titleMax, avail-libDetailGap-detail), 4), avail)
	return title, max(avail-libDetailGap-title, 0)
}

// table is the width the list's rows of this kind need to show all their
// text: the cursor prefix, the lead, the widest title and detail (fields
// gives them exactly at this width) and the tail (#133).
func (c libCols) table() int {
	w := libCursorPrefix + c.lead + max(c.titleMax, 4) + c.tail
	if c.detail > 0 {
		w += libDetailGap + c.detailMax
	}
	return w
}

// libTableMax is the widest a library list's rows grow (#133): on a wider
// terminal the rest stays empty, so a title and its detail stay within
// reach of the eye.
const libTableMax = 160

// libRowWidth returns the width a library row of a list whose rows need need
// cells fills: its content, within the panel and libTableMax. Every row of a
// kind needs the same, so the columns hold still from row to row.
func libRowWidth(panel, need int) int {
	return min(panel, libTableMax, need)
}

// setEntries sets the frame's rows and measures them for libRowWidth: the
// fields of its browsable rows and of its track rows, each row's need (its
// kind's table), and the list's column, the widest need.
func (f *libFrame) setEntries(entries []library.Entry) {
	f.entries = entries
	numbers := libTrackNumbers(entries)
	f.cols, f.trackCols = measureLibCols(entries), measureTrackCols(entries, numbers)
	f.rows, f.numbers = libraryRows(entries), numbers
	f.rowOf = libRowOf(f.rows, len(entries))
	f.needs = make([]int, len(entries))
	for i, e := range entries {
		f.needs[i] = f.cols.table()
		if libIsTrackRow(e) {
			f.needs[i] = f.trackCols.table()
		}
	}
	f.column = 0
	if len(f.needs) > 0 {
		f.column = slices.Max(f.needs) // the section headings span the widest kind's rows
	}
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
