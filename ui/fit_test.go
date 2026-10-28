package ui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestFitRect(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		width, height int
		want          string
	}{
		{name: "non-positive", text: "text", width: 0, height: 1, want: ""},
		{name: "rows", text: "one\ntwo\nthree", width: 10, height: 2, want: "one\ntwo"},
		{name: "wide", text: "ab音c", width: 4, height: 1, want: "ab音"},
		{name: "ansi", text: "\x1b[31mabcdef\x1b[0m", width: 3, height: 1, want: "\x1b[31mabc\x1b[0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FitRect(tt.text, tt.width, tt.height)
			if got != tt.want {
				t.Fatalf("FitRect(%q, %d, %d) = %q, want %q", tt.text, tt.width, tt.height, got, tt.want)
			}
			if got != "" && lipgloss.Width(got) > tt.width {
				t.Fatalf("FitRect width = %d, want <= %d", lipgloss.Width(got), tt.width)
			}
		})
	}
}
