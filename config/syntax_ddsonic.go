package config

// ddsonic: a check of config.toml's syntax, for the player's startup.
//
// Load reads the file line by line and passes over what it cannot make sense
// of, so a malformed file used to start the player on defaults without a
// word. CheckSyntax parses the file as TOML and reports the parser's error
// with its place.
//
// It checks syntax and nothing else. The parsed values are thrown away: Load
// still reads the file, in its own way, and which keys and sections exist is
// no concern of this check, so an unknown one is as welcome as ever. That
// also means a file can be valid TOML and still be read by Load differently
// from what TOML says (a comment after a [section] or after a value, an
// array over several lines); those are Load's limits, and not errors here.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// SyntaxError is where config.toml is not valid TOML, as the parser reports it.
type SyntaxError struct {
	Path    string
	Line    int    // from 1
	Column  int    // from 1
	Message string // the parser's own message
	cause   error
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", e.Path, e.Line, e.Column, e.Message)
}

// Unwrap returns the TOML parser's error.
func (e *SyntaxError) Unwrap() error { return e.cause }

// CheckSyntax parses config.toml and returns a *SyntaxError when it is not
// valid TOML. No file is no error: the defaults apply. The file is only
// read, never created, repaired or replaced.
func CheckSyntax() error {
	path, err := configPath()
	if err != nil {
		return nil // no config directory to look in: Load starts on defaults too
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	return checkSyntax(path, data)
}

func checkSyntax(path string, data []byte) error {
	text := string(data)
	err := parseTOML(text)
	if err == nil {
		return nil // valid TOML is never looked at again, let alone rewritten
	}
	var parse toml.ParseError
	if !errors.As(err, &parse) {
		// Not a place in the file (the parser gives none): still a refusal.
		return &SyntaxError{Path: path, Line: 1, Column: 1, Message: err.Error(), cause: err}
	}
	// The one exception: the parser stopped at the device line older
	// versions wrote. With that line quoted, the file is parsed again, and
	// whatever else is wrong with it is reported as it stands.
	if quoted, ok := quoteLegacyAudioDevice(text, parse.Position.Line); ok {
		if err = parseTOML(quoted); err == nil {
			return nil
		}
		if !errors.As(err, &parse) {
			return &SyntaxError{Path: path, Line: 1, Column: 1, Message: err.Error(), cause: err}
		}
	}
	return &SyntaxError{Path: path, Line: parse.Position.Line, Column: parse.Position.Col, Message: parse.Message, cause: err}
}

func parseTOML(text string) error {
	var discard map[string]any
	_, err := toml.Decode(text, &discard)
	return err
}

// audioDeviceKey is the one setting ddsonic used to write without quotes:
// choosing an output device saved "audio_device = <name>", which is not TOML.
const audioDeviceKey = "audio_device"

// legacyDeviceName reports whether name is something the old Save could
// have written as a device's name and the old Load read back: a name on one
// line, with a letter in it, no space at either end, and none of the
// characters that would make the line something else in TOML or to the old
// reader: a quote, a comment's "#", a second "=", a bracket or a brace, a
// control character. The old writer put no limit on a name's alphabet, so
// neither does this. What it excludes is a line that was never a device
// name at all (a malformed number, a stray "="), which stays the syntax
// error it is.
func legacyDeviceName(name string) bool {
	if name == "" || name != strings.TrimSpace(name) || !strings.ContainsFunc(name, unicode.IsLetter) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return unicode.IsControl(r) || strings.ContainsRune("\"'#=[]{}", r)
	})
}

// quoteLegacyAudioDevice is a temporary compatibility exception (owner,
// 2026-10-09), and the only one: it is not a way to recover from malformed
// TOML in general.
//
// Until v1.0.0-rc.4, Save wrote the output device's name bare, so a config
// in which a device was ever chosen holds a line that is not TOML, written
// by ddsonic itself. When the parser stops at exactly that line, the line is
// given quotes in a copy of the text and the copy is parsed, strictly, like
// any file. The file is not touched on read, and the next time a device is
// chosen Save writes the line quoted (saveValue).
//
// It is consulted only for the line the parser refused, so text the parser
// accepts (a multi-line string that happens to hold such a line) is never
// rewritten. And only the shape Save wrote is accepted: at the top level of
// the file, starting the line, "audio_device = " and then a device name
// (legacyDeviceName) to the end of the line. Indented, without the spaces,
// under a section, with a comment, a quote left open, or something that is
// no device name, it is malformed like anything else. The line count does
// not change, so every later error keeps its place.
//
// TODO(ddsonic): remove this function, legacyDeviceName, topLevel, the
// second parse in checkSyntax and the tests of the exception once configs
// written before v1.0.0-rc.4 no longer have to start unedited. When that
// is, is the owner's decision (plan.md, Post-1.0 backlog).
func quoteLegacyAudioDevice(text string, refusedLine int) (string, bool) {
	const prefix = audioDeviceKey + " = "
	lines := strings.Split(text, "\n")
	if refusedLine < 1 || refusedLine > len(lines) {
		return "", false
	}
	before := strings.Join(lines[:refusedLine-1], "\n")
	if refusedLine > 1 {
		before += "\n"
	}
	if !topLevel(before) {
		return "", false
	}
	line, cr := strings.CutSuffix(lines[refusedLine-1], "\r")
	name, ok := strings.CutPrefix(line, prefix)
	if !ok || !legacyDeviceName(name) {
		return "", false
	}
	lines[refusedLine-1] = prefix + QuoteString(name)
	if cr {
		lines[refusedLine-1] += "\r"
	}
	return strings.Join(lines, "\n"), true
}

// topLevel reports whether a statement that follows before, the text of a
// TOML file up to the start of some line, is at the file's top level: no
// table header has been opened, and before ends between statements, not
// inside a string or an array.
//
// It reads before as TOML is written, not as lines of text: a "[" that
// starts a line of a multi-line string, a line of an array over several
// lines, or a comment is not a table header. before is the part of the file
// the parser accepted, so it is well-formed; where this reader is in doubt
// it answers no.
func topLevel(before string) bool {
	depth := 0             // open arrays and inline tables
	statementStart := true // only spaces since the last line end, outside any value
	for i := 0; i < len(before); i++ {
		switch c := before[i]; c {
		case '#':
			end := strings.IndexByte(before[i:], '\n')
			if end < 0 {
				return true // a comment to the end: nothing follows it
			}
			i += end - 1
		case '"', '\'':
			closer := string(c)
			if triple := strings.Repeat(closer, 3); strings.HasPrefix(before[i:], triple) {
				closer = triple
			}
			i += len(closer)
			for ; i < len(before) && !strings.HasPrefix(before[i:], closer); i++ {
				if c == '"' && before[i] == '\\' {
					i++ // an escaped character
				}
			}
			if i >= len(before) {
				return false // before ends inside a string
			}
			i += len(closer) - 1
			// A multi-line string may end in four or five quotes: the
			// first one or two are the value's own last characters, and
			// the string ends with the run.
			for extra := 0; len(closer) == 3 && extra < 2 && i+1 < len(before) && before[i+1] == c; extra++ {
				i++
			}
			statementStart = false
		case '\n':
			if depth == 0 {
				statementStart = true
			}
		case ' ', '\t', '\r':
		case '[', '{':
			if c == '[' && depth == 0 && statementStart {
				return false // a table header: what follows is under it
			}
			depth++
			statementStart = false
		case ']', '}':
			depth--
		default:
			statementStart = false
		}
	}
	return depth == 0
}

// QuoteString writes s as a TOML string, in the form Load reads back as s
// where there is one. Load strips the quotes around a value and decodes no
// escapes, so s goes between plain double quotes when it needs no escape,
// and between single quotes (a TOML literal string, which has none) when it
// holds a backslash or a double quote. Anything else, a control character or
// both kinds of quote, is written as a basic string with TOML's escapes:
// always valid TOML that means exactly s, though Load's reader does not
// undo the escapes.
func QuoteString(s string) string {
	control := strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
	switch {
	case !control && !strings.ContainsAny(s, "\"\\"):
		return `"` + s + `"`
	case !control && !strings.Contains(s, "'"):
		return "'" + s + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// saveValue is value as Save should write it for key. The output device's
// name arrives raw from its callers (ui/model/update.go), whatever it holds,
// and is made a TOML string here, once, so that what ddsonic writes is
// always valid TOML. Every other key's callers format their own value.
func saveValue(key, value string) string {
	if key != audioDeviceKey {
		return value
	}
	return QuoteString(value)
}
