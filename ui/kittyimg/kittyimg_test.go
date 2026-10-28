package kittyimg

import (
	"crypto/rand"
	"image"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSupported(t *testing.T) {
	for _, tt := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"TERM": "xterm-kitty"}, true},
		{map[string]string{"KITTY_WINDOW_ID": "1", "TERM": "xterm-256color"}, true},
		{map[string]string{"TERM": "xterm-ghostty"}, true},
		{map[string]string{"TERM_PROGRAM": "ghostty", "TERM": "xterm-256color"}, true},
		{map[string]string{"TERM": "alacritty"}, false},
		{map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"}, false},
		{map[string]string{"TERM": "xterm-kitty", "TMUX": "/tmp/tmux-1000/default,1,0"}, false},
		{map[string]string{}, false},
	} {
		if got := Supported(func(k string) string { return tt.env[k] }); got != tt.want {
			t.Errorf("Supported(%v) = %v, want %v", tt.env, got, tt.want)
		}
	}
}

// Placeholder rows are cols cells wide to the renderer, carry the id in a
// 256-color foreground, and name every cell's row and column.
func TestPlaceholders(t *testing.T) {
	lines := Placeholders(42, 12, 5)
	if len(lines) != 5 {
		t.Fatalf("%d rows, want 5", len(lines))
	}
	for i, l := range lines {
		if w := lipgloss.Width(l); w != 12 {
			t.Errorf("row %d is %d cells wide, want 12", i, w)
		}
		if w := ansi.StringWidth(l); w != 12 {
			t.Errorf("row %d: ansi width %d, want 12", i, w)
		}
		if !strings.HasPrefix(l, "\x1b[38;5;42m") || strings.Count(l, "\U0010EEEE") != 12 {
			t.Errorf("row %d = %q", i, l)
		}
	}
	if got := Placeholders(1, 400, 400); len(got) != MaxCells {
		t.Errorf("rows = %d, want capped at %d", len(got), MaxCells)
	}
}

// Placing anew removes the old placements first, keeping the image data
// (a lowercase d=i), so no stale placement answers the placeholders.
func TestPlaceClearsFirst(t *testing.T) {
	seq := Place(7, 30, 15)
	if del, put := strings.Index(seq, "a=d"), strings.Index(seq, "a=p"); del < 0 || put < del || strings.Contains(seq, "d=I") {
		t.Errorf("Place = %q, want a placement delete, then the put", seq)
	}
}

func TestSequences(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	rand.Read(img.Pix) // noise, so the data needs several chunks
	seq, err := Transmit(img, 7)
	if err != nil {
		t.Fatal(err)
	}
	first := seq[:strings.Index(seq, ";")]
	if strings.Contains(first, "a=") && !strings.Contains(first, "a=t") {
		t.Errorf("transmit's first chunk %q is not a plain transmit (a=t, the default)", first)
	}
	for _, want := range []string{"i=7", "f=100", "q=2", "m=1"} {
		if !strings.Contains(first, want) {
			t.Errorf("transmit's first chunk %q lacks %s", first, want)
		}
	}
	if strings.Contains(first, "U=1") || !strings.HasSuffix(seq, "\x1b\\") || strings.Count(seq, "\x1b_G") < 2 {
		t.Errorf("transmit is not chunked APC sequences")
	}
	for seq, wants := range map[string][]string{
		Place(7, 30, 15): {"d=i", "a=d", "a=p", "i=7", "U=1", "c=30", "r=15", "q=2"},
		Delete(7):        {"a=d", "d=I", "i=7", "q=2"},
	} {
		for _, want := range wants {
			if !strings.Contains(seq, want) {
				t.Errorf("%q lacks %s", seq, want)
			}
		}
	}
}
