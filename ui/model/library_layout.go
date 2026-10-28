package model

// ddsonic: the body's size for the library's views, and the rows a short
// terminal keeps for the list.
//
// recomputeLayout (layout.go) counts the chrome for the terminal size and
// the screen, and leaves the rest to the body. With the library enabled the
// lists take all of it: more rows in the terminal mean more rows listed, and
// the frame fills the terminal instead of centering in it. Views size
// themselves from bodySize rather than from ui.PanelWidth and row counts of
// their own; its width is there for a detail view (track info) to lay
// itself out for the room it has without another layout change. How wide
// list rows run is library_rows.go.

import "github.com/bjarneo/cliamp/ui"

// bodySize returns the width and rows of the body region below the chrome:
// the list column beside the settings pane in the two-column playback
// screen, else the frame's inner width. Rows are zero while the terminal is
// too small.
func (m Model) bodySize() (width, rows int) {
	width = ui.PanelWidth
	if m.layout.twoColumn {
		width = m.layout.playlistWidth
	}
	return width, m.effectivePlaylistVisible()
}

// libPlaybackMinRows is the list a playback screen (the queue, or track info,
// lyrics or Up next over it) keeps on a short terminal: the visualizer
// gives up rows for it first (libVisualizerYield), then the queue's key bar
// (libFitKeyBar).
const libPlaybackMinRows = 8

// libVisualizerYield returns how many of visRows visualizer rows a playback
// screen gives up so its list keeps libPlaybackMinRows rows (below the queue's
// whole key bar), in a body of body rows before the bar. The visualizer
// keeps one row.
func (m *Model) libVisualizerYield(width, body, visRows int) int {
	if visRows <= 1 || !m.libraryEnabled() {
		return 0
	}
	bar := 0
	if !m.hideHelpBar && m.libraryOnScreen() {
		bar = max(0, len(m.libKeyBarLines(width))-1) // the rows beyond the one counted
	}
	return min(max(0, libPlaybackMinRows+bar-body), visRows-1)
}
