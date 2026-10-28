package library

import (
	"strings"
	"unicode"
)

// CleanText makes text from a provider, a tag or the catalog safe to draw in
// a terminal: control characters, which could start escape sequences (a
// clipboard write, a moved cursor), are dropped, and line breaks and tabs
// become spaces.
func CleanText(s string) string {
	if !strings.ContainsFunc(s, unicode.IsControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}
