package model

// ddsonic: the key table of each library view (Library, Search Results,
// Library Search, Queue). A view's table lists the library's own keys and
// the cliamp keys it passes through. The gate (handleLibraryKey) passes
// exactly the table's cliamp keys, and the key bar at the bottom and the
// keymap overlay's entries render the table, so a cliamp key works in a view
// if and only if it is listed. The library's own keys are handled in
// handleLibraryKey's switch, except q, which handleKey handles; a new one
// needs its row here too. One row follows the selection: f, on a directory
// station (library_favorite.go).

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/library"
)

// libKeyHelp is one row of a view's keys.
type libKeyHelp struct {
	keys     []string // KeyPressMsg.String values handled for this row
	keyLabel string
	label    string
}

// libKeyView is one view's key table.
type libKeyView struct {
	title string
	own   []libKeyHelp // handled by the library
	pass  []libKeyHelp // reach cliamp's own handlers through the gate
}

var (
	libMoveKeys = []libKeyHelp{
		{[]string{"up", "k", "down", "j"}, "j k", "Move"},
		{[]string{"g", "home", "G", "end"}, "g G", "Top, bottom"},
		{[]string{"pgup", "ctrl+u", "pgdown", "ctrl+d"}, "PgUp PgDn", "Page"},
	}
	libOpenKey    = libKeyHelp{[]string{"enter", "l", "right"}, "Enter l", "Open"}
	libBackKey    = libKeyHelp{[]string{"esc", "h", "left", "backspace"}, "Esc h", "Back"}
	libSearchKey  = libKeyHelp{[]string{"/"}, "/", "Search"}
	libPauseKey   = libKeyHelp{[]string{"space"}, "Space", "Pause"}
	libQuitKey    = libKeyHelp{[]string{"q"}, "q", "Quit"}
	libQueueKey   = libKeyHelp{[]string{"tab"}, "Tab", "Queue"}
	libOrderKey   = libKeyHelp{[]string{"o"}, "o", "Order"}
	libRefreshKey = libKeyHelp{[]string{"r"}, "r", "Sync"}
	// libSkipKey is the library's previous/next track, in every view but
	// the search input.
	libSkipKey = libKeyHelp{[]string{"p", "n"}, "p n", "Prev, next"}

	// In search results, the back keys return to the query instead.
	libResultBackKeys = []libKeyHelp{
		{[]string{"/", "esc", "h", "left", "backspace"}, "/ Esc h", "Query"},
		libPauseKey, libSkipKey, libQueueKey, libQuitKey,
	}
	// The search input takes q as text.
	libSearchInputKeys = []libKeyHelp{
		{[]string{"enter", "down", "tab"}, "Enter Tab", "Results"},
		{[]string{"esc"}, "Esc", "Close"},
		{[]string{"ctrl+u"}, "Ctrl+U", "Clear"},
	}
	libQueueKeys = []libKeyHelp{
		{[]string{"tab", "esc", "b"}, "Tab Esc", "Library"},
		libSkipKey,
		{[]string{"X"}, "X", "Clear queue"}, // library_queue_clear.go
	}

	// libPlayerKeys are cliamp's transport keys, live in every view but the
	// search input.
	libPlayerKeys = []libKeyHelp{
		{[]string{"s"}, "s", "Stop"},
		{[]string{"+", "=", "-"}, "+ -", "Volume"},
		{[]string{"Z"}, "Z", "Sleep"}, // the sleep timer (sleep_ddsonic.go)
		{[]string{"ctrl+g"}, "Ctrl+G", "Hide keys"},
	}

	// libQueuePassKeys are cliamp's queue keys, live while the queue has the
	// screen. Ctrl+I (Metadata, which terminals send as Tab) and m (Mono)
	// stay swallowed; n and p are the library's.
	libQueuePassKeys = slices.Concat(libMoveKeys, []libKeyHelp{
		{[]string{"enter"}, "Enter", "Play"},
		libPauseKey,
		{[]string{"left", "right"}, "← →", "Seek"},
		{[]string{"shift+left", "shift+right"}, "Shift ← →", "Seek far"},
		{[]string{"/"}, "/", "Filter"},
		{[]string{"z"}, "z", "Shuffle"},
		{[]string{"r"}, "r", "Repeat"},
		{[]string{"a"}, "a", "Play next"},
		{[]string{"A"}, "A", "Up next"},
		{[]string{"x"}, "x", "Remove"},
		{[]string{"shift+up", "shift+down"}, "Shift ↑ ↓", "Reorder"},
		{[]string{"ctrl+z"}, "Ctrl+Z", "Undo"},
		{[]string{"e"}, "e", "EQ"},
		{[]string{"v"}, "v", "Visualizer"},
		{[]string{"[", "]"}, "[ ]", "Speed"},
		{[]string{"i"}, "i", "Info"},
		{[]string{"y"}, "y", "Lyrics"},
		{[]string{"ctrl+j"}, "Ctrl+J", "Jump"},
		libQuitKey, // for the bar: handleKey quits on q before the gate
	})

	// The gate's sets, read from the tables.
	libraryPassthroughKeys = libKeySet(libPlayerKeys)
	queuePassthroughKeys   = libKeySet(libQueuePassKeys)
)

func libKeySet(rows []libKeyHelp) map[string]bool {
	set := make(map[string]bool)
	for _, r := range rows {
		for _, k := range r.keys {
			set[k] = true
		}
	}
	return set
}

// libraryKeyView is the key table of the library view in front: the queue
// while Tab has handed it the screen, else the top of the stack.
func (m Model) libraryKeyView() libKeyView {
	if !m.lib.visible {
		return libKeyView{title: "Queue", own: slices.Concat(libQueueKeys, m.libFavoriteKeys()), pass: slices.Concat(libQueuePassKeys, libPlayerKeys)}
	}
	own := slices.Concat(libMoveKeys, []libKeyHelp{libOpenKey}, m.libFavoriteKeys())
	if _, searching := m.libSearchLevel(); searching {
		if m.lib.searchInput { // the input takes every other key as text
			return libKeyView{title: "Library Search", own: libSearchInputKeys}
		}
		return libKeyView{title: "Search Results", own: slices.Concat(own, libResultBackKeys), pass: libPlayerKeys}
	}
	if len(m.lib.stack) > 1 {
		own = append(own, libBackKey)
	}
	own = append(own, libSearchKey)
	if _, ok := m.libTop().level.(library.OrderedLevel); ok {
		own = append(own, libOrderKey)
	}
	own = append(own, libPauseKey, libSkipKey, libQueueKey)
	if m.lib.refresh != nil {
		own = append(own, libRefreshKey)
	}
	own = append(own, libQuitKey)
	return libKeyView{title: "Library", own: own, pass: libPlayerKeys}
}

// has reports whether key is live in the view.
func (v libKeyView) has(key string) bool {
	return slices.ContainsFunc(slices.Concat(v.own, v.pass), func(r libKeyHelp) bool {
		return slices.Contains(r.keys, key)
	})
}

// libraryOnScreen reports whether one of the library's views is in front,
// rather than a cliamp screen or overlay opened over it.
func (m Model) libraryOnScreen() bool {
	if !m.libraryEnabled() {
		return false
	}
	s := m.activeScreen()
	return s == screenLibrary || s == screenMain
}

// libraryQuitsOnQ reports whether q quits at once: the library is enabled
// and no text input, nor a cliamp screen the library does not open, has the
// keyboard. Over the library's own overlays (Up next, track info, lyrics,
// the keymap Ctrl+K opens over them) it quits too. The cliamp screens are
// listed defensively: the library swallows the keys that open them.
func (m Model) libraryQuitsOnQ() bool {
	if !m.libraryEnabled() {
		return false
	}
	switch {
	case m.keymap.searching, m.jumping, m.urlInputting, m.search.active, m.netSearch.active, m.provSearch.active,
		m.devicePicker.visible, m.plPicker.visible, m.fileBrowser.visible, m.spotSearch.visible,
		m.navBrowser.visible, m.themePicker.visible, m.visPicker.visible, m.plManager.visible, m.subs.visible:
		return false
	}
	_, searching := m.libSearchLevel()
	return !(m.lib.visible && searching && m.lib.searchInput)
}

// libraryDropsGlobalKey reports whether key, one cliamp handles before the
// gate on every screen (the keymap, undo), must do nothing because the
// library view in front does not list it.
func (m Model) libraryDropsGlobalKey(key string) bool {
	switch key {
	case "ctrl+k", "ctrl+z":
		return m.libraryOnScreen() && !m.libraryKeyView().has(key)
	}
	return false
}

// libMinBodyRows is the list height the key bar never takes rows from: a
// terminal too short for the whole bar shows its first rows.
const libMinBodyRows = 3

// libKeyBar renders the view's keys, wrapped to width, in at most the rows
// the layout gave it (all of them before any layout). ok is false when no
// library view is in front.
func (m Model) libKeyBar(width int) (bar string, ok bool) {
	if !m.libraryOnScreen() {
		return "", false
	}
	lines := m.libKeyBarLines(width)
	if n := m.lib.barRows; n > 0 && len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n"), true
}

func (m Model) libKeyBarLines(width int) []string {
	v := m.libraryKeyView()
	var lines []string
	line := ""
	for _, r := range slices.Concat(v.own, v.pass) {
		hint := helpKey(r.keyLabel, r.label)
		switch {
		case line == "":
			line = hint
		case lipgloss.Width(line)+1+lipgloss.Width(hint) <= width:
			line += " " + hint
		default:
			lines = append(lines, ansi.Truncate(line, width, ""))
			line = hint
		}
	}
	if line != "" {
		lines = append(lines, ansi.Truncate(line, width, ""))
	}
	return lines
}

// libFitKeyBar gives the key bar its rows for a frame of width and a body of
// body rows before the bar, and returns the rows beyond the one the layout
// already counts for the hint bar.
func (m *Model) libFitKeyBar(width, body int) int {
	m.lib.barRows = 0
	if !m.libraryOnScreen() {
		return 0
	}
	extra := max(0, min(len(m.libKeyBarLines(width))-1, body-libMinBodyRows))
	m.lib.barRows = 1 + extra
	return extra
}

// libraryKeymapEntries lists the view's keys, for a keymap overlay opened
// over the library from a cliamp screen.
func (m Model) libraryKeymapEntries() []keymapEntry {
	v := m.libraryKeyView()
	out := []keymapEntry{{action: "— current: " + v.title + " —", divider: true}}
	for _, r := range slices.Concat(v.own, v.pass) {
		out = append(out, keymapEntry{key: r.keyLabel, action: r.label})
	}
	return out
}
