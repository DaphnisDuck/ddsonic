package model

// ddsonic: rendering for the library navigation stack.

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// brandTitle heads every screen (renderTitle in view.go): the duck and the
// name in the title style, then the version as the build reports it, dimmed.
// The version is secondary: in fewer than its width of room cells it is left
// out, and the name stays whole. The duck is the TUI's alone; MPRIS shows
// the plain name (appmeta.DisplayName).
func brandTitle(room int) string {
	name := titleStyle.Render("🦆 " + appdir.Name)
	full := name + " " + dimStyle.Render(appmeta.Version())
	if lipgloss.Width(full) > room {
		return name
	}
	return full
}

// libRow is one rendered line: an entry, or a section heading (index < 0).
type libRow struct {
	index   int
	section string
}

func libraryRows(entries []library.Entry) []libRow {
	rows := make([]libRow, 0, len(entries))
	last := ""
	for i, e := range entries {
		if e.Section != "" && e.Section != last {
			rows = append(rows, libRow{index: -1, section: e.Section})
		}
		last = e.Section
		rows = append(rows, libRow{index: i})
	}
	return rows
}

// libRowOf maps each of n entries to its line in rows.
func libRowOf(rows []libRow, n int) []int {
	rowOf := make([]int, n)
	for i, row := range rows {
		if row.index >= 0 {
			rowOf[row.index] = i
		}
	}
	return rowOf
}

// libLayout returns f's rendered lines, track numbers and entry lines:
// cached by setEntries, or worked out for a frame given its entries
// another way.
func (f *libFrame) libLayout() (rows []libRow, numbers, rowOf []int) {
	if len(f.rowOf) == len(f.entries) && len(f.numbers) == len(f.entries) {
		return f.rows, f.numbers, f.rowOf
	}
	rows = libraryRows(f.entries)
	return rows, libTrackNumbers(f.entries), libRowOf(rows, len(f.entries))
}

// libScroll returns the first visible row so the cursor's row r (and the
// heading directly above it) fits in budget rows.
func libScroll(rows []libRow, r, scroll, budget int) int {
	if budget <= 0 || len(rows) <= budget {
		return 0
	}
	top := r
	if r > 0 && rows[r-1].index < 0 {
		top = r - 1
	}
	if top < scroll {
		scroll = top
	}
	if r >= scroll+budget {
		scroll = r - budget + 1
	}
	return max(0, min(scroll, len(rows)-budget))
}

func (m *Model) libAdjustScroll() {
	if !m.libraryEnabled() {
		return
	}
	f := m.libTop()
	rows, _, rowOf := f.libLayout()
	f.scroll = libScroll(rows, libCursorRow(rowOf, f.cursor), f.scroll, m.libListBudget())
}

// libListBudget is how many rows the list gets: the body, less the search
// input line on the search screen and the level's note.
func (m *Model) libListBudget() int {
	_, budget := m.bodySize()
	if _, ok := m.libSearchLevel(); ok {
		budget--
	}
	budget -= len(m.libNote())
	return max(budget, 1)
}

// libNoteMinRows is the fewest list rows a note leaves; a body too short
// for that shows the list alone.
const libNoteMinRows = 3

// libNoteIndent is the note's left margin, the rows' own.
const libNoteIndent = "  "

// libNote is the top level's note (library.NotedLevel) as the lines shown
// above its rows, or nil when it has none or the body is too short for
// both. The note is wrapped to the panel, never cut short: what it
// qualifies is the reason it is there.
func (m *Model) libNote() []string {
	nl, ok := m.libTop().level.(library.NotedLevel)
	if !ok || m.libSigningInHere(m.libTop()) {
		return nil
	}
	width, rows := m.bodySize()
	var lines []string
	for _, sentence := range nl.Note() {
		lines = append(lines, wrapText(sentence, width-len(libNoteIndent))...)
	}
	if len(lines) == 0 || rows-len(lines) < libNoteMinRows {
		return nil
	}
	return lines
}

func (m Model) libBreadcrumb() string {
	parts := make([]string, len(m.lib.stack))
	for i, f := range m.lib.stack {
		parts[i] = library.CleanText(f.level.Title())
	}
	crumb := strings.Join(parts, " / ")
	// Keep the tail: the current level matters more than the root.
	if limit := ui.PanelWidth - 14; limit > 4 && lipgloss.Width(crumb) > limit {
		for len(parts) > 1 && lipgloss.Width("… / "+strings.Join(parts, " / ")) > limit {
			parts = parts[1:]
		}
		crumb = "… / " + strings.Join(parts, " / ") // sepHeader clips the rest
	}
	return crumb
}

func (m *Model) libHeaderLine() string {
	f := m.libTop()
	label := m.libBreadcrumb()
	if ol, ok := f.level.(library.OrderedLevel); ok {
		label += " · " + ol.OrderName()
	}
	if badge := m.libSyncBadge(); badge != "" {
		label += "  " + badge
	}
	if _, pending := m.libPending(f); (f.loading() && len(f.entries) == 0) || f.err != nil || pending {
		return sepHeader(label)
	}
	return sepHeaderN(label, f.cursor+1, len(f.entries))
}

// libSyncBadge is the small catalog sync indicator: ↻ while syncing, the
// age of the last success, or a note that the cached library is showing.
// With several providers, each badge names its provider.
func (m *Model) libSyncBadge() string {
	var badges []string
	for _, provider := range slices.Sorted(maps.Keys(m.lib.sync)) {
		st := m.lib.sync[provider]
		var badge string
		switch {
		case st.needsAuth:
			// Always named: it is the one badge that asks for something.
			badge = library.SourceLabel(provider) + " sign-in required" + m.libCachedSuffix(provider)
			badges = append(badges, badge)
			continue
		case st.running:
			badge = "↻ syncing"
		case st.lastErr != "":
			badge = "sync failed" + m.libCachedSuffix(provider)
		case !st.lastSuccess.IsZero():
			badge = "✓ synced " + syncAge(time.Since(st.lastSuccess))
		default:
			continue
		}
		if len(m.lib.sync) > 1 {
			badge = library.SourceLabel(provider) + " " + badge
		}
		badges = append(badges, badge)
	}
	return strings.Join(badges, " · ")
}

func syncAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

// libHelpLine is the library overlay's help piece. renderTierHelp draws the
// key bar (library_keymap.go) before reaching it; this keeps the overlay whole.
func (m *Model) libHelpLine() string {
	bar, _ := m.libKeyBar(ui.PanelWidth)
	return bar
}

func (m *Model) renderLibraryBody() string {
	if sl, ok := m.libSearchLevel(); ok && !m.libSigningInHere(m.libTop()) {
		budget := m.libListBudget()
		body := m.renderLibraryList(budget)
		if sl.Query() == "" {
			lines := make([]string, len(libSearchHint))
			for i, h := range libSearchHint {
				lines[i] = dimStyle.Render(truncate(h, ui.PanelWidth))
			}
			body = bodyLines(lines, budget)
		}
		return m.libSearchPrompt(sl) + "\n" + body
	}
	var b strings.Builder
	for _, line := range m.libNote() {
		b.WriteString(dimStyle.Render(libNoteIndent+line) + "\n")
	}
	return b.String() + m.renderLibraryList(m.libListBudget())
}

// renderLibraryList renders the top frame's rows, or its loading, sign-in
// or error state, in budget lines.
func (m *Model) renderLibraryList(budget int) string {
	f := m.libTop()
	switch {
	case m.libSigningInHere(f):
		return bodyLines(m.libSigningInLines(budget), budget)
	case f.loading() && len(f.entries) == 0:
		return bodyLines([]string{loadingLine("Loading " + f.level.Title() + "…")}, budget)
	case errors.Is(f.err, playlist.ErrNeedsAuth) && m.libCanSignIn(f):
		return bodyMessage(libSignInPrompt, budget)
	case f.err != nil:
		return bodyLines([]string{
			dimStyle.Render("  " + truncate(f.err.Error(), ui.PanelWidth-4)),
			dimStyle.Render("  Press Enter to retry."),
		}, budget)
	case len(f.entries) == 0:
		return bodyMessage("Nothing here.", budget)
	}
	if provider, ok := m.libPending(f); ok {
		return bodyMessage(m.libPendingText(provider), budget)
	}

	panel := ui.PanelWidth
	defer ui.WithPanelWidth(listRowWidth(panel, f.column, 0))() // section headings
	rows, numbers, rowOf := f.libLayout()
	scroll := libScroll(rows, libCursorRow(rowOf, f.cursor), f.scroll, budget)
	// While the search input has focus, Enter does not act on a row, so no
	// row shows the cursor.
	_, searching := m.libSearchLevel()
	showCursor := !searching || !m.lib.searchInput
	lines := make([]string, 0, budget)
	for _, row := range rows[scroll:] {
		if len(lines) >= budget {
			break
		}
		if row.index < 0 {
			lines = append(lines, dimStyle.Render(labeledSeparator("", library.CleanText(row.section))))
			continue
		}
		need := 0
		if row.index < len(f.needs) {
			need = f.needs[row.index]
		}
		cols := f.cols
		if libIsTrackRow(f.entries[row.index]) {
			cols = f.trackCols
		}
		// Every row of a kind is its kind's table (setEntries).
		restore := ui.WithPanelWidth(listRowWidth(panel, need, need))
		lines = append(lines, cursorLine(libEntryLabel(f.entries[row.index], numbers[row.index], cols),
			showCursor && row.index == f.cursor))
		restore()
	}
	return bodyLines(lines, budget)
}

// libCursorRow is the cursor's line, or 0 when the cursor is off the list.
func libCursorRow(rowOf []int, cursor int) int {
	if cursor < 0 || cursor >= len(rowOf) {
		return 0
	}
	return rowOf[cursor]
}

// libTrackNumbers numbers each section's tracks from 1 (0 for other rows),
// so an album counts its tracks and a search result section its own.
func libTrackNumbers(entries []library.Entry) []int {
	numbers := make([]int, len(entries))
	n := 0
	for i, e := range entries {
		if i > 0 && e.Section != entries[i-1].Section {
			n = 0
		}
		if e.Track != nil && !e.Track.Realtime {
			n++
			numbers[i] = n
		}
	}
	return numbers
}

// libTrackRow renders a library track row in its list's fields (#63):
// "  3. Name          Label     4:05". The number is right-aligned in the
// lead, the name and the label (a search result's source) each have a field,
// cut at a word (#64), and the duration starts at the same cell on every
// row. The label keeps its column before the name (libCols.detailFirst).
func libTrackRow(number int, name, label string, secs int, cols libCols) string {
	num := fmt.Sprintf("%d. ", number)
	nameW, labelW := cols.fields(ui.PanelWidth - libCursorPrefix)
	row := strings.Repeat(" ", max(cols.lead-lipgloss.Width(num), 0)) + num + padCells(truncateWords(name, nameW), nameW)
	if cols.detail > 0 {
		row += strings.Repeat(" ", libDetailGap) + padCells(truncateWords(label, labelW), labelW)
	}
	if cols.tail > 0 {
		row += " " + formatTrackTime(secs)
	}
	return strings.TrimRight(row, " ")
}

// libEntryLabel renders "Title      Detail ›"; tracks use the numbered track
// row (libTrackRow) in cols, the list's track fields, with their detail,
// which only a search result has (its source), in a field of its own;
// unmeasured (cols zero), a track row has its own. A station is a live stream with neither, so it
// is a plain row. Browsable rows end in "›". The title and the detail each
// get a field of the list's (cols.fields): the detail starts at the same
// cell on every row, and a longer title or detail is cut at a word (#63).
func libEntryLabel(e library.Entry, number int, cols libCols) string {
	if libIsTrackRow(e) {
		if cols.lead == 0 { // not measured with a list: the row's own
			cols = measureTrackCols([]library.Entry{e}, []int{number})
		}
		return libTrackRow(number, library.CleanText(trackViewName(*e.Track)), library.CleanText(e.Detail), e.Track.DurationSecs, cols)
	}
	suffix := ""
	if e.Open != nil {
		suffix = " ›"
	}
	title := library.CleanText(e.Title)
	if e.Favorite {
		title = "★ " + title
	}
	titleW, detailW := cols.fields(ui.PanelWidth - libCursorPrefix)
	title = truncateWords(title, titleW)
	if cols.detail == 0 {
		return title + suffix
	}
	row := padCells(title, titleW) + strings.Repeat(" ", libDetailGap) + padCells(truncateWords(library.CleanText(e.Detail), detailW), detailW)
	if suffix == "" {
		return strings.TrimRight(row, " ")
	}
	return row + suffix
}

// padCells pads s with spaces to w cells.
func padCells(s string, w int) string {
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
}
