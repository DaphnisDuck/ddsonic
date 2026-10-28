// Package kittyimg draws images in terminals that support the Kitty graphics
// protocol's Unicode placeholders (Kitty, Ghostty). An image is sent once
// with a virtual placement of a size in cells; the view then draws it as
// ordinary text, one placeholder character per cell, so it moves, clips and
// disappears with the text around it and leaves nothing behind on screen.
//
// See https://sw.kovidgoyal.net/kitty/graphics-protocol/#unicode-placeholders
package kittyimg

import (
	"fmt"
	"image"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
)

// MaxCells is the most rows or columns a placement can span: the number of
// row and column diacritics the protocol defines.
const MaxCells = 297

// CellSizeQuery asks the terminal for its cell size in pixels (CSI 16 t);
// Bubble Tea hands the answer to Update as an ultraviolet CellSizeEvent.
const CellSizeQuery = "\x1b[16t"

// Supported reports whether the terminal, as getenv describes it, draws
// Unicode placeholders. Inside tmux the protocol needs passthrough, which is
// off by default, so tmux is left out.
func Supported(getenv func(string) string) bool {
	if getenv("TMUX") != "" {
		return false
	}
	term := getenv("TERM")
	return getenv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty") ||
		term == "xterm-ghostty" || strings.EqualFold(getenv("TERM_PROGRAM"), "ghostty")
}

// Transmit returns the sequence that sends img as image id, replacing any
// image with that id, without placing it (Place does). It is PNG, which
// keeps a transparent image's colors, and costs tens of milliseconds for a
// cover: build it off the UI goroutine.
func Transmit(img image.Image, id int) (string, error) {
	var sb strings.Builder
	err := kitty.EncodeGraphics(&sb, img, &kitty.Options{
		Action:       kitty.Transmit,
		Quiet:        2,
		ID:           id,
		Format:       kitty.PNG,
		Transmission: kitty.Direct,
		Chunk:        true,
	})
	if err != nil {
		return "", fmt.Errorf("kittyimg: %w", err)
	}
	return sb.String(), nil
}

// Place returns the sequence that gives image id, already sent, a virtual
// placement of cols×rows cells, removing the placements it had. The
// terminal fits the image into the cells without distorting it.
func Place(id, cols, rows int) string {
	clear := kitty.Options{Action: kitty.Delete, Quiet: 2, ID: id, Delete: kitty.DeleteID}
	put := kitty.Options{Action: kitty.Put, Quiet: 2, ID: id, VirtualPlacement: true, Columns: cols, Rows: rows}
	return ansi.KittyGraphics(nil, clear.Options()...) + ansi.KittyGraphics(nil, put.Options()...)
}

// Delete returns the sequence that removes image id and frees its data.
func Delete(id int) string {
	o := kitty.Options{Action: kitty.Delete, Quiet: 2, ID: id, Delete: kitty.DeleteID, DeleteResources: true}
	return ansi.KittyGraphics(nil, o.Options()...)
}

// Placeholders returns the rows of text that draw cols×rows cells of image
// id's placement. id must be 1–255: it is the cells' 256-color foreground,
// which survives any color profile the renderer converts to. Every cell
// names its row and column, so a row redrawn from its middle still shows
// the right part of the image.
func Placeholders(id, cols, rows int) []string {
	cols, rows = min(cols, MaxCells), min(rows, MaxCells)
	lines := make([]string, rows)
	for r := range rows {
		var sb strings.Builder
		fmt.Fprintf(&sb, "\x1b[38;5;%dm", id)
		for c := range cols {
			sb.WriteRune(kitty.Placeholder)
			sb.WriteRune(kitty.Diacritic(r))
			sb.WriteRune(kitty.Diacritic(c))
		}
		sb.WriteString("\x1b[39m")
		lines[r] = sb.String()
	}
	return lines
}
