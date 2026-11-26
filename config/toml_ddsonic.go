package config

// ddsonic: config.toml is read with the TOML parser the startup check uses
// (#75), so that what the check accepts is read as TOML says: comments after
// a value or a [section], arrays over several lines, escapes in strings,
// dotted and quoted keys. Load's per-key switch is upstream's and stays as
// it is: configEntries hands it each decoded value as the text it already
// parses. The writers (Save, saveSectionValue, setup) use Statements to find
// whole statements in the file's text.

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// decodeConfig parses text, config.toml's contents at path, as TOML, with
// the legacy audio_device line as its one exception (quoteLegacyAudioDevice).
// A file that is not TOML is a *SyntaxError.
func decodeConfig(path, text string) (map[string]any, toml.MetaData, error) {
	data, md, err := decodeTOML(text)
	if err == nil {
		return data, md, nil
	}
	var parse toml.ParseError
	if !errors.As(err, &parse) {
		// Not a place in the file (the parser gives none): still a refusal.
		return nil, md, &SyntaxError{Path: path, Line: 1, Column: 1, Message: err.Error(), cause: err}
	}
	// The one exception: the parser stopped at the device line older
	// versions wrote. With that line quoted, the file is parsed again, and
	// whatever else is wrong with it is reported as it stands.
	if quoted, ok := quoteLegacyAudioDevice(text, parse.Position.Line); ok {
		if data, md, err = decodeTOML(quoted); err == nil {
			return data, md, nil
		}
		if !errors.As(err, &parse) {
			return nil, md, &SyntaxError{Path: path, Line: 1, Column: 1, Message: err.Error(), cause: err}
		}
	}
	return nil, md, &SyntaxError{Path: path, Line: parse.Position.Line, Column: parse.Position.Col, Message: parse.Message, cause: err}
}

func decodeTOML(text string) (map[string]any, toml.MetaData, error) {
	var data map[string]any
	md, err := toml.Decode(text, &data)
	return data, md, err
}

// configEntry is one thing Load's loop reads: a [section] header, or a key
// of section with its value.
type configEntry struct {
	header  bool
	section string // lower case; "" is the top level
	key     string
	val     string // the value as text (renderValue)
}

// configEntries is the decoded file as the sequence of headers and keys a
// line reader would have met, in file order: a key whose table is not the
// current one is preceded by that table's header (a dotted key, a key after
// an inline table). Arrays of tables ([[x]]) are passed over: no setting is
// one.
func configEntries(data map[string]any, md toml.MetaData) []configEntry {
	var out []configEntry
	current := ""
	for _, key := range md.Keys() {
		if md.Type(key...) == "ArrayHash" {
			continue
		}
		v, ok := lookup(data, key)
		if !ok {
			continue // under an array of tables
		}
		if _, table := v.(map[string]any); table {
			current = sectionName(key)
			out = append(out, configEntry{header: true, section: current})
			continue
		}
		section := sectionName(key[:len(key)-1])
		if section != current {
			current = section
			out = append(out, configEntry{header: true, section: current})
		}
		out = append(out, configEntry{section: section, key: key[len(key)-1], val: renderValue(v)})
	}
	return out
}

// sectionName is a table's name as Load's switch matches it: its parts in
// lower case joined with dots, a part that is empty or holds a dot keeping
// its quotes, so that [plugins.""] or ["plugins.x"] is no other table's
// name (the top-level [plugins] allowlist included).
func sectionName(key toml.Key) string {
	parts := make([]string, len(key))
	for i, k := range key {
		if k == "" || strings.Contains(k, ".") {
			k = strconv.Quote(k)
		}
		parts[i] = strings.ToLower(k)
	}
	return strings.Join(parts, ".")
}

func lookup(data map[string]any, key toml.Key) (any, bool) {
	var v any = data
	for _, k := range key {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[k]; !ok {
			return nil, false
		}
	}
	return v, true
}

// renderValue writes a decoded value as the text Load's switch parses:
//   - a string between double quotes, its characters as decoded (no
//     escapes: parseString takes the quotes off, and they keep a string a
//     string, so "true" is still not a bool);
//   - an integer in decimal; a float with a "." (or Inf, NaN), so it is
//     still no integer; a bool as true or false;
//   - an array on one line, "[a, b]", and an inline table as "{k = v}";
//   - a date or time in RFC 3339.
func renderValue(v any) string {
	switch v := v.(type) {
	case string:
		return `"` + v + `"`
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.ContainsAny(s, ".IN") {
			s += ".0"
		}
		return s
	case bool:
		return strconv.FormatBool(v)
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = renderValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case []map[string]any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = renderValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + " = " + renderValue(v[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return `"` + fmt.Sprint(v) + `"`
	}
}

// Statement is one statement of a config file, lines[Start:End] of the
// file's text split at "\n": a [section] header, a key and its value (over
// several lines when the value is), or a line that is neither (blank, a
// comment, or one the parser refuses).
type Statement struct {
	Start, End int
	// Header: the statement is a [section] or [[array]] header. Name is
	// the section's name as decoded ("plugins.lastfm"), "" for an array of
	// tables, which no writer writes into.
	Header bool
	Name   string
	// Key is the key a key/value statement sets ("a.b" for a dotted key).
	Key string
	// parsed: the statement is valid TOML by itself, as opposed to a line
	// of a broken file read the old way.
	parsed bool
}

// Statements splits a config file's lines into statements, in order, every
// line in exactly one. A statement is the fewest lines from its first that
// the TOML parser accepts on their own; a line it refuses, however many
// lines follow (a broken file), is a statement by itself and is read as
// the writers always read lines: a header when it is "[" ... "]", a key
// when it has an "=".
func Statements(lines []string) []Statement {
	var out []Statement
	for i := 0; i < len(lines); {
		s := statementAt(lines, i)
		out = append(out, s)
		i = s.End
	}
	return out
}

func statementAt(lines []string, i int) Statement {
	for j := i + 1; j <= len(lines); j++ {
		_, md, err := decodeTOML(strings.Join(lines[i:j], "\n"))
		if err == nil {
			return parsedStatement(lines, i, j, md)
		}
		// Only a statement cut short (the parser ran to the end of the
		// lines given) can be completed by the next line.
		var parse toml.ParseError
		if !errors.As(err, &parse) || parse.Position.Line < lastNonBlank(lines[i:j]) {
			break
		}
	}
	return lineStatement(lines[i], i)
}

// lastNonBlank is the number, from 1, of the last line that is not blank
// (1 when all are).
func lastNonBlank(lines []string) int {
	for n := len(lines); n > 1; n-- {
		if strings.TrimSpace(lines[n-1]) != "" {
			return n
		}
	}
	return 1
}

func parsedStatement(lines []string, i, j int, md toml.MetaData) Statement {
	s := Statement{Start: i, End: j, parsed: true}
	keys := md.Keys()
	if len(keys) == 0 {
		return s // blank lines and comments
	}
	first := keys[0]
	if typ := md.Type(first...); strings.HasPrefix(strings.TrimSpace(lines[i]), "[") && (typ == "Hash" || typ == "ArrayHash") {
		s.Header = true
		if typ == "Hash" {
			s.Name = strings.Join(first, ".")
		}
		return s
	}
	s.Key = strings.Join(first, ".")
	return s
}

// lineStatement reads a line the parser refuses as the writers did before
// #75, so that a broken file can still be edited.
func lineStatement(line string, i int) Statement {
	s := Statement{Start: i, End: i + 1}
	t := strings.TrimSpace(line)
	switch {
	case t == "" || strings.HasPrefix(t, "#"):
	case strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]"):
		s.Header = true
		s.Name = t[1 : len(t)-1]
	case !strings.HasPrefix(t, "["):
		if k, _, ok := strings.Cut(t, "="); ok {
			s.Key = strings.TrimSpace(k)
		}
	}
	return s
}

// ReplaceStatement replaces statement s of lines with the single line
// line, and returns the lines. A comment after a one-line statement's value
// is kept after the new value; a comment inside a value over several lines
// goes with the value.
func ReplaceStatement(lines []string, s Statement, line string) []string {
	if s.parsed && s.End == s.Start+1 {
		if comment, ok := trailingComment(lines[s.Start]); ok {
			line += comment
		}
	}
	return slices.Replace(lines, s.Start, s.End, line)
}

// trailingComment is the comment after the statement on line, with the
// spaces before it, when there is one. A "#" starts it when the text
// before it is a whole statement by itself; a "#" inside a string leaves
// the string open, and the text before it is no statement.
func trailingComment(line string) (string, bool) {
	for p := strings.IndexByte(line, '#'); p >= 0; {
		if _, _, err := decodeTOML(line[:p]); err == nil {
			start := len(strings.TrimRight(line[:p], " \t"))
			return strings.TrimRight(line[start:], "\r"), true
		}
		next := strings.IndexByte(line[p+1:], '#')
		if next < 0 {
			break
		}
		p += next + 1
	}
	return "", false
}

// unquote takes one pair of quotes off a string's ends, when it has them:
// a string whose own last character is a quote keeps it.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
