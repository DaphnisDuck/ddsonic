package catalog

import (
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// unfoldedLetters are Latin letters with no Unicode decomposition, so
// stripping combining marks leaves them in place and they would sort after z.
var unfoldedLetters = strings.NewReplacer(
	"ø", "o", "Ø", "o", "ł", "l", "Ł", "l", "đ", "d", "Đ", "d",
	"ß", "ss", "æ", "ae", "Æ", "ae", "œ", "oe", "Œ", "oe", "þ", "th", "Þ", "th",
)

// SortKeyVersion changes whenever SortKey does, so a catalog written with
// older keys recomputes them.
const SortKeyVersion = 2

// SortKey is the one normalization every catalog writer uses for sort_title,
// sort_name and sort_artist, so lists from different providers interleave
// consistently. Names sort as written, except that case and diacritics are
// folded ("Dvořák" → "dvorak") and leading punctuation is dropped; "The
// Planets" files under T. A title that is all punctuation keeps it rather
// than sorting as empty.
func SortKey(s string) string {
	folded, _, err := transform.String(transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC), s)
	if err != nil {
		folded = s
	}
	lower := strings.ToLower(unfoldedLetters.Replace(strings.TrimSpace(folded)))
	key := strings.TrimLeftFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if key == "" {
		return lower
	}
	return key
}
