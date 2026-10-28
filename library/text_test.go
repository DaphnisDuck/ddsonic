package library

import "testing"

func TestCleanText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Mahler 5", "Mahler 5"},
		{"Évora — Café", "Évora — Café"},
		{"clip\x1b]52;c;ZXZpbA==\x07board", "clip]52;c;ZXZpbA==board"},
		{"red\x1b[31mtitle", "red[31mtitle"},
		{"c1\u009bcsi", "c1csi"},
		{"two\nlines\tand\rtab", "two lines and tab"},
		{"nul\x00byte", "nulbyte"},
	}
	for _, tt := range tests {
		if got := CleanText(tt.in); got != tt.want {
			t.Errorf("CleanText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
