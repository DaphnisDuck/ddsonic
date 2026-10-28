package catalog

import "testing"

func TestSortKey(t *testing.T) {
	tests := []struct{ in, want string }{
		// Articles are part of the name: "The Cure" files under T.
		{"The Cure", "the cure"},
		{"A Copland Celebration", "a copland celebration"},
		{"An Ending", "an ending"},
		{"Dvořák: Symphony No. 9", "dvorak: symphony no. 9"},
		{`"Academy of Ancient Music" - Baroque`, `academy of ancient music" - baroque`},
		{"  Beethoven  ", "beethoven"},
		{"Theodora", "theodora"},
		{"", ""},
		{"Øresund", "oresund"},
		{"Łódź Philharmonic", "lodz philharmonic"},
		{"Straße", "strasse"},
		{"!!!", "!!!"}, // all punctuation falls back rather than emptying
	}
	for _, tt := range tests {
		if got := SortKey(tt.in); got != tt.want {
			t.Errorf("SortKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
