package catalog

import (
	"reflect"
	"testing"
)

func TestParseQuery(t *testing.T) {
	word := func(s string) Term { return Term{Text: s} }
	tests := []struct {
		in   string
		want Query
	}{
		{"", Query{}},
		{"   ", Query{}},
		{"bee sym", Query{Terms: []Term{word("bee"), word("sym")}}},
		{`"new world" dvořák`, Query{Terms: []Term{{Text: "new world", Phrase: true}, word("dvořák")}}},
		{"artist:ozawa mahler", Query{Terms: []Term{{Field: FieldArtist, Text: "ozawa"}, word("mahler")}}},
		{`Album:"the planets"`, Query{Terms: []Term{{Field: FieldAlbum, Text: "the planets", Phrase: true}}}},
		{"title:x genre:jazz", Query{Terms: []Term{{Field: FieldTitle, Text: "x"}, {Field: FieldGenre, Text: "jazz"}}}},
		{"source:local provider:Spotify holst", Query{Terms: []Term{word("holst")}, Providers: []string{Local, Spotify}}},
		{"source:youtube x", Query{Terms: []Term{word("x")}, Providers: []string{YouTube}}},
		{"type:albums type:station x", Query{Terms: []Term{word("x")}, Kinds: []SearchKind{SearchAlbum, SearchStation}}},
		// A pasted NUL or other control character separates words.
		{"mah\x00ler\x1b", Query{Terms: []Term{word("mah"), word("ler")}}},
		// Mid-typing operators and unknown values.
		{"artist:", Query{}},
		{"type: x", Query{Terms: []Term{word("x")}}},
		{"type:foo", Query{Terms: []Term{word("type:foo")}}},
		{"source:tidal", Query{Terms: []Term{word("source:tidal")}}},
		{"foo:bar", Query{Terms: []Term{word("foo:bar")}}},
		{`foo:"a b"`, Query{Terms: []Term{{Text: "a b", Phrase: true}}}},
		{"ac/dc 12:34", Query{Terms: []Term{word("ac/dc"), word("12:34")}}},
		// Odd input.
		{`"unbalanced phrase`, Query{Terms: []Term{{Text: "unbalanced phrase", Phrase: true}}}},
		{`"" * - : NEAR`, Query{Terms: []Term{word("NEAR")}}},
		{`artist:"" x`, Query{Terms: []Term{word("x")}}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := ParseQuery(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseQuery(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestQueryHelpers(t *testing.T) {
	q := ParseQuery("new world")
	if q.Text() != "new world" || q.Empty() || !q.Wants(SearchTrack) {
		t.Errorf("plain query: Text %q, Empty %v, Wants %v", q.Text(), q.Empty(), q.Wants(SearchTrack))
	}
	q = ParseQuery("artist:holst type:album mars")
	if q.Text() != "holst mars" || q.Wants(SearchTrack) || !q.Wants(SearchAlbum) {
		t.Errorf("operator query: Text %q, Wants track %v, album %v", q.Text(), q.Wants(SearchTrack), q.Wants(SearchAlbum))
	}
}
