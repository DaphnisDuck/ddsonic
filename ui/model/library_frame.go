package model

// ddsonic: the frame's border and the rules between the screen's parts.
//
// The border takes the place of the frame's outer padding (one row above and
// below, one column each side), so the layout loses no row or column to it:
// recomputeLayout's arithmetic is the same with it or without. It is drawn
// from the compact tier up, where the frame has that padding to give; the
// minimal tier keeps its rows for the list.

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/ui"
)

// SetFrameBorder turns the border and rules on or off ([ddsonic] border).
func (m *Model) SetFrameBorder(on bool) {
	m.lib.border = on
	m.recomputeLayout()
}

// libBorderOn reports whether a frame laid out as l draws the border: it is
// on, the library is enabled, and l's tier is compact or full, whose sizes
// leave the frame a cell of padding on each side for it to take.
func (m Model) libBorderOn(l frameLayout) bool {
	return m.lib.border && m.libraryEnabled() && (l.tier == layoutCompact || l.tier == layoutFull)
}

// libFrameStyle is the frame style for m.layout: its padding, with the
// border in the padding's outer cells when there is one. A bordered frame
// is the terminal's height: cliamp's layout reserves a row for a status
// message and lets the frame shrink without one, which would leave a blank
// row under the border.
func (m Model) libFrameStyle() lipgloss.Style {
	l := m.layout
	// ui.FrameStyle is global: clear what an earlier layout set on it.
	s := ui.FrameStyle.UnsetBorderStyle().UnsetBorderTop().UnsetBorderRight().UnsetBorderBottom().UnsetBorderLeft().
		UnsetBorderForeground().UnsetHeight()
	if !m.libraryEnabled() {
		return s.Padding(l.paddingV, l.paddingH) // cliamp's frame, as before
	}
	s = s.Border(lipgloss.RoundedBorder(), l.border).BorderForeground(ui.ColorDim)
	if l.border {
		return s.Padding(l.paddingV-1, l.paddingH-1).Height(m.height)
	}
	return s.Padding(l.paddingV, l.paddingH)
}

// libSpacerRule is the row between the body and the key bar: a rule with
// the border, else blank. The column divider above it stops short of it,
// as it does of the headers at its top.
func (m Model) libSpacerRule() string {
	if !m.layout.border {
		return ""
	}
	return dimStyle.Render(strings.Repeat("─", ui.PanelWidth))
}

// libColumnGutter is the channel between the queue and the settings column:
// a divider down its middle with the border, else blank.
func (m Model) libColumnGutter() string {
	if !m.layout.border {
		return columnGutter
	}
	half := strings.Repeat(" ", columnGutterWidth/2)
	return half + dimStyle.Render("│") + half
}
