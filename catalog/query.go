package catalog

import (
	"slices"
	"strings"
	"unicode"
)

// SearchKind is a kind of search result.
type SearchKind string

// Search result kinds. A station is a track of the radio provider.
const (
	SearchArtist   SearchKind = "artist"
	SearchAlbum    SearchKind = "album"
	SearchTrack    SearchKind = "track"
	SearchPlaylist SearchKind = "playlist"
	SearchStation  SearchKind = "station"
)

// SearchKinds lists every kind, in the order results are shown.
var SearchKinds = []SearchKind{SearchArtist, SearchAlbum, SearchTrack, SearchPlaylist, SearchStation}

// Radio is the provider name of catalogued radio stations.
const Radio = "radio"

// Search fields a term can be limited to. FieldAny matches any field.
const (
	FieldAny    = ""
	FieldTitle  = "title"
	FieldArtist = "artist"
	FieldAlbum  = "album"
	FieldGenre  = "genre"
)

// Term is one word or quoted phrase of a query. A word matches as a prefix
// ("bee" finds "Beethoven"); a phrase matches its words in order, the last
// as a prefix.
type Term struct {
	Field  string // FieldAny or one field
	Text   string
	Phrase bool
}

// Query is a parsed search: every term must match, and results are limited
// to Providers and Kinds when they are set.
type Query struct {
	Terms     []Term
	Providers []string
	Kinds     []SearchKind
}

// Empty reports whether the query has nothing to match.
func (q Query) Empty() bool { return len(q.Terms) == 0 }

// Wants reports whether results of kind are wanted.
func (q Query) Wants(kind SearchKind) bool {
	return len(q.Kinds) == 0 || slices.Contains(q.Kinds, kind)
}

// Text is every term's text, joined by spaces: the query without its
// operators, for searches elsewhere that do not read them.
func (q Query) Text() string {
	words := make([]string, len(q.Terms))
	for i, t := range q.Terms {
		words[i] = t.Text
	}
	return strings.Join(words, " ")
}

// ParseQuery parses what the user typed:
//
//	bee sym               words, each matching as a prefix
//	"new world"           a phrase
//	artist:ozawa          a word or phrase limited to a field
//	                      (artist, album, title, genre)
//	source:local          only one provider (spotify, youtube, local, radio); also provider:
//	type:album            only one kind (artist, album, track, playlist, station)
//
// An unknown operator, or a known one with a value it does not know, is
// plain text. An unbalanced quote runs to the end.
func ParseQuery(s string) Query {
	var q Query
	// Control characters (a pasted NUL) would make SQLite reject the match.
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	for _, tok := range tokenize(s) {
		switch {
		case tok.phrase:
			q.addText(tok.text, true)
		case tok.field != "":
			// field:"phrase"; an unknown field keeps just the phrase.
			if f, ok := termField(tok.field); ok {
				q.addTerm(f, tok.text, true)
			} else {
				q.addText(tok.text, true)
			}
		default:
			name, value, ok := strings.Cut(tok.text, ":")
			if !ok || !q.operator(strings.ToLower(name), value) {
				q.addText(tok.text, false)
			}
		}
	}
	return q
}

// operator applies name:value and reports whether it was one. A known
// operator with nothing after the colon yet (mid-typing) is dropped.
func (q *Query) operator(name, value string) bool {
	switch name {
	case "source", "provider":
		if value == "" {
			return true
		}
		if p, ok := provider(value); ok {
			q.Providers = append(q.Providers, p)
			return true
		}
	case "type":
		if value == "" {
			return true
		}
		if k, ok := kind(value); ok {
			q.Kinds = append(q.Kinds, k)
			return true
		}
	default:
		if f, ok := termField(name); ok {
			q.addTerm(f, value, false)
			return true
		}
	}
	return false
}

func (q *Query) addTerm(field, text string, phrase bool) {
	if searchable(text) {
		q.Terms = append(q.Terms, Term{Field: field, Text: text, Phrase: phrase})
	}
}

// addText adds a plain term if it holds anything searchable.
func (q *Query) addText(text string, phrase bool) {
	if searchable(text) {
		q.Terms = append(q.Terms, Term{Text: text, Phrase: phrase})
	}
}

// searchable reports whether text has a letter or digit: punctuation alone
// matches nothing.
func searchable(text string) bool {
	return strings.IndexFunc(text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0
}

func termField(name string) (string, bool) {
	switch strings.ToLower(name) {
	case FieldTitle, FieldArtist, FieldAlbum, FieldGenre:
		return strings.ToLower(name), true
	}
	return "", false
}

func provider(v string) (string, bool) {
	switch p := strings.ToLower(v); p {
	case Spotify, Local, Radio, YouTube:
		return p, true
	}
	return "", false
}

func kind(v string) (SearchKind, bool) {
	v = strings.TrimSuffix(strings.ToLower(v), "s")
	for _, k := range SearchKinds {
		if string(k) == v {
			return k, true
		}
	}
	return "", false
}

// token is one space-separated piece of a query: a word, a "phrase", or a
// field:"phrase" (field set, text the phrase).
type token struct {
	text   string
	phrase bool
	field  string
}

func tokenize(s string) []token {
	var out []token
	rs := []rune(s)
	// quoted reads a phrase opening at rs[i] and returns it and the index
	// after its closing quote (or the end).
	quoted := func(i int) (string, int) {
		j := i + 1
		for j < len(rs) && rs[j] != '"' {
			j++
		}
		return strings.TrimSpace(string(rs[i+1 : j])), j + 1
	}
	for i := 0; i < len(rs); {
		switch {
		case unicode.IsSpace(rs[i]):
			i++
		case rs[i] == '"':
			var text string
			text, i = quoted(i)
			out = append(out, token{text: text, phrase: true})
		default:
			j := i
			for j < len(rs) && !unicode.IsSpace(rs[j]) && rs[j] != '"' {
				j++
			}
			word := string(rs[i:j])
			if j < len(rs) && rs[j] == '"' && strings.HasSuffix(word, ":") {
				var text string
				text, i = quoted(j)
				out = append(out, token{text: text, field: strings.TrimSuffix(word, ":")})
				continue
			}
			out = append(out, token{text: word})
			i = j
		}
	}
	return out
}

// SearchResult is one search hit. Exactly one of Artist, Album, Track and
// Playlist is set (a station is a Track); callers build their entries from
// it, so a result opens like the same item found by browsing.
type SearchResult struct {
	Kind     SearchKind
	Provider string
	// Score orders results within a kind: lower is better. It is bm25
	// relevance scaled up for library members and exact title matches;
	// scores of different kinds are not comparable.
	Score    float64
	Artist   *Artist
	Album    *Album
	Track    *Track
	Playlist *Playlist
}

// SearchResults holds each kind's results, best first.
type SearchResults map[SearchKind][]SearchResult
