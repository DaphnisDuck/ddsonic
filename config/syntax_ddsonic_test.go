package config

// ddsonic: a config.toml that is not valid TOML is reported with the
// parser's error and its place; what ddsonic documents and writes passes.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestCheckSyntaxAcceptsValidTOML(t *testing.T) {
	tests := []struct{ name, toml string }{
		{"empty", ""},
		{"blank lines and comments", "\n# a comment\n\n   # an indented one\n"},
		{"top-level keys", "volume = -3\nshuffle = false\nrepeat = \"off\"\neq = [0, 0, 1.5, -2]\nspeed = 1.25\n"},
		{"sections", "[spotify]\nclient_id = \"abc\"\nbitrate = 320\n\n[ytmusic]\ncookies_from = \"firefox\"\n"},
		{"no newline at the end", "[spotify]\nbitrate = 320"},
		{"single quotes", "initial_directory = '~/Music'\n"},
		{"escapes in a string", "path = \"C:\\\\Music\\\\x\"\nq = \"say \\\"hi\\\"\"\n"},
		{"a hash inside a string", "theme = \"C# Minor\"\nname = 'a # b'\n"},
		{"environment variables, as the docs write them", "password = \"${NAVIDROME_PASSWORD}\"\ntoken = \"$PLEX_TOKEN\"\n"},
		// Unknown keys and sections are no business of a syntax check.
		{"keys and sections nobody knows", "flux_capacitor = true\n\n[somebody.elses]\nx = 1\n\n[plugins.lastfm]\nuser = \"me\"\n\n[navidrome]\nurl = \"https://example.invalid\"\n"},
		{"dotted and quoted keys", "a.b = 1\n\"quoted key\" = 2\n'another' = 3\nsite.\"google.com\" = true\n"},
		{"nested arrays and an inline table", "x = [[1, 2], [\"a\", \"b\"]]\npoint = { x = 1, y = \"two\" }\n"},
		{"an array of tables", "[[stations]]\nname = \"one\"\n\n[[stations]]\nname = \"two\"\n"},
		{"multi-line strings", "text = \"\"\"\nline one\nline \"two\"\n\"\"\"\nraw = '''\nit's raw\n'''\nvolume = 0\n"},
		{"numbers, dates and times", "a = +1_000\nb = 0x1F\nc = 1e-3\nd = 2026-10-09T12:00:00Z\ne = inf\n"},
		{"Windows line endings", "volume = 0\r\n[spotify]\r\nbitrate = 320\r\n"},
		{"a byte-order mark", "\xef\xbb\xbfvolume = 0\n"},
		{"spaces everywhere", "  [ spotify ]  \n\t bitrate\t=\t320 \n"},
		// Valid TOML that Load reads differently from what TOML says. They
		// are Load's limits, known and left as they are, and no syntax error:
		// a comment after a section (Load misses the section), a comment
		// after a value (Load takes it into the value), an array over
		// several lines (Load reads the first line only).
		{"a comment after a section", "[spotify] # mine\nbitrate = 320\n"},
		{"a comment after a value", "cookies_from = \"firefox\"   # or chrome\nartwork = false # off\n"},
		{"an array over several lines", "youtube_playlists = [\n  \"PLaaa\",  # mine\n  \"PLbbb\",\n]\nvolume = 0\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkSyntax("config.toml", []byte(tt.toml)); err != nil {
				t.Fatalf("checkSyntax = %v, want nil for:\n%s", err, tt.toml)
			}
		})
	}
}

// Malformed TOML of every kind is refused: not only what is left unclosed,
// but invalid values, repeated keys and tables, bad escapes and bad numbers.
func TestCheckSyntaxRefusesMalformedTOML(t *testing.T) {
	tests := []struct {
		name string
		toml string
		line int    // where the parser places it
		want string // a fragment of the parser's message
	}{
		// Structure.
		{"a line that is no key = value", "volume = 0\njust some words\n", 2, "expected '.' or '='"},
		{"a section that is not closed", "[spotify\nbitrate = 320\n", 2, "to end table name"},
		{"brackets and nothing else", "volume = 0\n\n[[[ broken\n", 3, "to end table name"},
		{"an empty section name", "[]\n", 1, "table names cannot be empty"},
		{"an array-of-tables header closed once", "[[stations]\n", 2, "table array name delimiter"},
		{"text after a section header", "[spotify] bitrate = 320\n", 1, "expected a top-level item to end"},
		{"nothing before the equals sign", "= 3\n", 1, "key name appears blank"},
		{"a key with a space in it", "my key = 3\n", 1, "expected '.' or '='"},
		{"nothing after the equals sign", "[spotify]\nbitrate =\n", 2, "expected value"},
		{"only a comment after the equals sign", "bitrate = # later\n", 1, "expected value"},
		{"two equals signs", "volume = = 3\n", 1, "expected value"},
		// Strings.
		{"a string that is not closed", "[spotify]\nclient_id = \"oops\nbitrate = 320\n", 2, "strings cannot contain newlines"},
		{"a single-quoted string that is not closed", "dir = '~/Music\n", 1, "strings cannot contain newlines"},
		{"text after a string", "theme = \"Tokyo\" Night\n", 1, "expected a top-level item to end"},
		{"an invalid escape", "s = \"bad \\q escape\"\n", 1, "invalid escape"},
		{"a multi-line string that is not closed", "volume = 0\ntext = \"\"\"\nnever ends\n", 3, "unexpected EOF"},
		// Arrays and inline tables.
		{"an array that is not closed", "eq = [0, 0, 1\nvolume = 0\n", 2, "array terminator"},
		{"an array with an empty element", "eq = [0,, 1]\n", 1, "unexpected comma"},
		{"an array missing a comma", "eq = [0 1]\n", 1, "expected a comma"},
		{"an inline table that is not closed", "point = { x = 1\n", 1, "inline table terminator"},
		// Values that are not TOML values.
		{"a bare word for a string", "theme = Tokyo Night\n", 1, "expected value"},
		{"a boolean in capitals", "shuffle = True\n", 1, "expected value"},
		{"an integer with a leading zero", "volume = 01\n", 1, "Invalid integer"},
		{"a float with nothing after the point", "speed = 1.\n", 1, "Invalid float"},
		{"an unfinished hexadecimal number", "x = 0x\n", 1, "hexadecimal"},
		{"an impossible date", "d = 2026-13-40\n", 1, "invalid datetime"},
		{"two values on a line", "volume = 1 2\n", 1, "expected a top-level item to end"},
		// Repeats.
		{"a key given twice", "volume = 1\nvolume = 2\n", 2, "already been defined"},
		{"a section given twice", "[spotify]\nbitrate = 320\n[spotify]\nclient_id = \"x\"\n", 3, "already been defined"},
		{"a key given twice in a section", "[spotify]\nbitrate = 320\nbitrate = 160\n", 3, "already been defined"},
		// The first error is the one reported.
		{"the first of several errors", "ok = 1\nbad line\n[also bad\n", 2, "expected '.' or '='"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSyntax("/cfg/config.toml", []byte(tt.toml))
			var syntax *SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("checkSyntax = %v, want a *SyntaxError for:\n%s", err, tt.toml)
			}
			if syntax.Line != tt.line || syntax.Column < 1 || syntax.Message == "" || !strings.Contains(syntax.Message, tt.want) {
				t.Errorf("error %d:%d %q, want line %d, a column and a message with %q", syntax.Line, syntax.Column, syntax.Message, tt.line, tt.want)
			}
			if syntax.Path != "/cfg/config.toml" || !strings.HasPrefix(err.Error(), "/cfg/config.toml:") {
				t.Errorf("the error does not name the file: %v", err)
			}
			// It is the TOML parser's own error, kept as the cause.
			var parse toml.ParseError
			if !errors.As(err, &parse) {
				t.Errorf("the error does not carry the parser's: %v", err)
			}
		})
	}
}

// The example config ddsonic ships is valid, as shipped and with each of its
// commented settings switched on by itself.
func TestCheckSyntaxAcceptsTheExampleConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config.toml.example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSyntax("config.toml.example", data); err != nil {
		t.Fatalf("the example as shipped: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	switched := 0
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "# ")
		// "# key = value" and "# [section]" are settings; prose stays prose.
		if !ok || !(strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") || strings.Contains(rest, " = ") && !strings.Contains(strings.SplitN(rest, " = ", 2)[0], " ")) {
			continue
		}
		// Alone at the end of the top level or as its own section, so that
		// two alternatives for one key do not meet.
		doc := rest + "\n"
		if !strings.HasPrefix(rest, "[") {
			doc = "[example]\n" + doc
		}
		if err := checkSyntax("config.toml.example", []byte(doc)); err != nil {
			t.Errorf("line %d of the example, switched on (%q): %v", i+1, rest, err)
		}
		switched++
	}
	if switched < 10 {
		t.Fatalf("only %d commented settings were found in the example: the test reads it wrongly", switched)
	}
}

// What ddsonic itself writes is valid TOML, the output device's name included.
func TestCheckSyntaxAcceptsWhatSaveWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	for key, value := range map[string]string{
		"theme":        `"Tokyo Night"`,
		"eq":           "[1, 2, 3, 0, 0, 0, 0, 0, 0, -1.5]",
		"speed":        "1.25",
		"shuffle":      "true",
		"audio_device": "alsa_output.pci-0000_00_1f.3.analog-stereo", // passed bare by its callers
	} {
		if err := Save(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := SaveRadioCountry("NO"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err := CheckSyntax(); err != nil {
		t.Fatalf("CheckSyntax = %v for a file Save wrote:\n%s", err, data)
	}
	if !strings.Contains(string(data), `audio_device = "alsa_output.pci-0000_00_1f.3.analog-stereo"`) {
		t.Errorf("the output device was not written as a string:\n%s", data)
	}
	// And Load reads the device back as it was given.
	cfg, err := Load()
	if err != nil || cfg.AudioDevice != "alsa_output.pci-0000_00_1f.3.analog-stereo" || cfg.Theme != "Tokyo Night" {
		t.Errorf("Load = device %q, theme %q, err %v", cfg.AudioDevice, cfg.Theme, err)
	}
	// Saving it again replaces the line; it is not quoted twice.
	if err := Save("audio_device", "other device"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := Load(); cfg.AudioDevice != "other device" {
		t.Errorf("after a second save the device reads %q", cfg.AudioDevice)
	}
	if err := CheckSyntax(); err != nil {
		t.Errorf("after a second save: %v", err)
	}
}

// Until v1.0.0-rc.4, choosing an output device saved its name without
// quotes. That line, in the exact shape Save wrote it, is still accepted:
// ddsonic wrote it. Nothing else about the file is forgiven for it, and the
// same line in any other shape is malformed TOML like any other.
func TestCheckSyntaxAcceptsTheDeviceLineOlderVersionsWrote(t *testing.T) {
	accepted := map[string]string{
		"among other settings":         "volume = 0\naudio_device = alsa_output.pci-0000_00_1f.3.analog-stereo\ntheme = \"Nord\"\n\n[spotify]\nbitrate = 320\n",
		"alone, with no line end":      "audio_device = hw:0,0",
		"a name with spaces":           "audio_device = Built-in Audio Analog Stereo\n",
		"with Windows line endings":    "volume = 0\r\naudio_device = hw0\r\n",
		"quoted, as it is written now": "audio_device = \"hw0\"\n",
	}
	for name, doc := range accepted {
		if err := checkSyntax("config.toml", []byte(doc)); err != nil {
			t.Errorf("%s: %v, want it accepted:\n%s", name, err, doc)
		}
	}
	refused := map[string]struct {
		toml string
		line int
	}{
		// The exception is for one key.
		"another key with a bare value":   {"audio_device = hw0\ntheme = Nord\n", 2},
		"a key that only starts the same": {"audio_device_2 = hw0\n", 1},
		// It forgives nothing else in the file.
		"an error after the device line": {"audio_device = hw0\n\n[spotify\n", 4},
		"a repeated key beside it":       {"audio_device = hw0\nvolume = 1\nvolume = 2\n", 3},
		"the device line given twice":    {"audio_device = hw0\naudio_device = hw1\n", 2},
		// And only the shape Save wrote.
		"in a section":                          {"[spotify]\naudio_device = hw0\n", 2},
		"indented":                              {"  audio_device = hw0\n", 1},
		"without the spaces around =":           {"audio_device=hw0\n", 1},
		"with a comment after it":               {"audio_device = hw0 # mine\n", 1},
		"with trailing spaces":                  {"audio_device = hw0  \n", 1},
		"with a string left open":               {"audio_device = \"hw0\n", 1},
		"with a single-quoted string left open": {"audio_device = 'hw0\n", 1},
		"with no value":                         {"audio_device = \n", 1},
		"with an unclosed array":                {"audio_device = [hw0\n", 1},
	}
	for name, tt := range refused {
		err := checkSyntax("config.toml", []byte(tt.toml))
		var syntax *SyntaxError
		if !errors.As(err, &syntax) || syntax.Line != tt.line {
			t.Errorf("%s: checkSyntax = %v, want a syntax error at line %d", name, err, tt.line)
		}
	}
	// Only the parsed copy is quoted, and only the line the parser refused.
	legacy := "volume = 0\naudio_device = hw0\n"
	if got, ok := quoteLegacyAudioDevice(legacy, 2); !ok || got != "volume = 0\naudio_device = \"hw0\"\n" || legacy != "volume = 0\naudio_device = hw0\n" {
		t.Errorf("quoteLegacyAudioDevice = %q, %v", got, ok)
	}
	for _, line := range []int{0, 1, 3, 9} {
		if got, ok := quoteLegacyAudioDevice(legacy, line); ok {
			t.Errorf("quoteLegacyAudioDevice rewrote for line %d, which is not the device line: %q", line, got)
		}
	}
}

// QuoteString gives a TOML string that Load's reader gives back unchanged.
func TestQuoteStringRoundTrips(t *testing.T) {
	for _, s := range []string{"plain", "/home/me/Music", "two words", `C:\Music\x`, `My "Best" Of`, "it's mine", "ünïcödé", "a # b"} {
		quoted := QuoteString(s)
		var doc struct{ V string }
		if _, err := toml.Decode("v = "+quoted+"\n", &doc); err != nil || doc.V != s {
			t.Errorf("QuoteString(%q) = %s: TOML reads %q, err %v", s, quoted, doc.V, err)
		}
		if got := parseString(quoted); got != s {
			t.Errorf("QuoteString(%q) = %s: Load's reader gives %q", s, quoted, got)
		}
	}
	// Both kinds of quote: valid TOML, though Load's reader cannot undo the escapes.
	both := QuoteString(`it's "x"`)
	var doc struct{ V string }
	if _, err := toml.Decode("v = "+both+"\n", &doc); err != nil || doc.V != `it's "x"` {
		t.Errorf("QuoteString with both quotes = %s: TOML reads %q, err %v", both, doc.V, err)
	}
}

func TestCheckSyntaxOfTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	path := filepath.Join(dir, "config.toml")

	// No file is not an error: ddsonic starts on its defaults.
	if err := CheckSyntax(); err != nil {
		t.Fatalf("with no config file: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("checking created the config file (stat: %v)", err)
	}

	broken := []byte("volume = 0\n[spotify]\nbitrate = \n")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(path)
	err := CheckSyntax()
	var syntax *SyntaxError
	if !errors.As(err, &syntax) || syntax.Path != path || syntax.Line != 3 {
		t.Fatalf("CheckSyntax = %v, want a *SyntaxError at line 3 of %s", err, path)
	}
	// The file is read, never written.
	after, _ := os.Stat(path)
	if got, _ := os.ReadFile(path); string(got) != string(broken) || !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("the malformed file was touched: %q, modified %s (was %s)", got, after.ModTime(), before.ModTime())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the config directory now holds %d entries, want the one file", len(entries))
	}

	// A file that cannot be read is an error too, and not a syntax error.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckSyntax(); err == nil || errors.As(err, &syntax) {
		t.Errorf("a config.toml that is a directory: %v, want a read error", err)
	}
}

// M7: the legacy exception looks at nothing the parser accepts. Text shaped
// like the old device line inside a valid multi-line string is part of that
// string and stays so; and a device line that is not a device name (a
// malformed number, a second equals sign) is malformed, not excused.
func TestLegacyDeviceLineNeitherRewritesValidTOMLNorMasksErrors(t *testing.T) {
	for name, doc := range map[string]string{
		"inside a basic multi-line string":    "text = \"\"\"\naudio_device = x\"\"\"\n",
		"inside a literal multi-line string":  "text = '''\naudio_device = x'\n'''\n",
		"a whole line of a multi-line string": "text = \"\"\"\naudio_device = hw0\n\"\"\"\nvolume = 0\n",
	} {
		if err := checkSyntax("config.toml", []byte(doc)); err != nil {
			t.Errorf("%s: valid TOML was refused: %v\n%s", name, err, doc)
		}
	}
	for name, tt := range map[string]struct {
		toml string
		line int
	}{
		"a malformed number as the device":   {"audio_device = 01\n", 1},
		"a second equals sign":               {"audio_device = = default\n", 1},
		"a device that is punctuation":       {"audio_device = --\n", 1},
		"the device line after a real error": {"volume = 01\naudio_device = hw0\n", 1},
		// What follows the device line is still checked, at its own line.
		"an error two lines after it":            {"audio_device = hw0\nvolume = 0\ntheme = Nord\n", 3},
		"a multi-line string left open after it": {"audio_device = hw0\ntext = \"\"\"\nnever closed\n", 3},
	} {
		err := checkSyntax("config.toml", []byte(tt.toml))
		var syntax *SyntaxError
		if !errors.As(err, &syntax) || syntax.Line != tt.line {
			t.Errorf("%s: checkSyntax = %v, want a syntax error at line %d", name, err, tt.line)
		}
	}
}

// M8: whatever the device is called, what Save writes is valid TOML that
// holds exactly that name.
func TestSaveWritesAnyDeviceNameAsValidTOML(t *testing.T) {
	for _, name := range []string{
		"alsa_output.pci-0000_00_1f.3.analog-stereo", "hw:0,0", "Built-in Audio", "",
		`"Studio Device`, `'Studio Device`, `"quoted"`, `'quoted'`, `it's "both"`, `back\slash`, `ends with \`,
		"control\x01device", "bell\adevice", "tab\tname", "new\nline", "cr\rname", "del\x7fname", "esc\x1bname",
		"ünïcödé ♫", "a # b", "a = b", "[bracket]", "{brace}", "'''", `"""`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := Save("volume", "0"); err != nil {
				t.Fatal(err)
			}
			if err := Save("audio_device", name); err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
			var doc struct {
				Device *string `toml:"audio_device"`
			}
			if _, err := toml.Decode(string(data), &doc); err != nil {
				t.Fatalf("Save wrote malformed TOML for the device %q: %v\n%s", name, err, data)
			}
			if doc.Device == nil || *doc.Device != name {
				t.Fatalf("Save wrote %q, which TOML reads as %v, want %q", data, doc.Device, name)
			}
			if err := CheckSyntax(); err != nil {
				t.Errorf("the start would refuse it: %v", err)
			}
		})
	}
}

// Round 3, M7: whether the old device line is at the top level is a
// question about TOML, not about what a line of text starts with. The first
// file here is what the old Save really produced after a multi-line string
// holding a line that starts with "[": the key went to the end of the file,
// because that line is not a section header.
func TestLegacyDeviceLineTopLevelIsLexical(t *testing.T) {
	accepted := map[string]string{
		"after a multi-line string holding a line that starts with [": "text = \"\"\"\n[not a section\n\"\"\"\nvolume = -3\n\naudio_device = default\n",
		"after a literal multi-line string holding a section header":  "text = '''\n[spotify]\n'''\naudio_device = default\n",
		"after a multi-line array whose lines start with [":           "pairs = [\n  [1, 2],\n  [3, 4],\n]\naudio_device = default\n",
		"after a comment that looks like a section":                   "# [spotify]\naudio_device = default\n",
		"after a string holding a bracket and a hash":                 "name = \"[x] # y\"\naudio_device = default\n",
		"before the first section":                                    "audio_device = default\n\n[spotify]\nbitrate = 320\n",
		// The old writer wrote whatever the device was called.
		"a name that is not ASCII":      "audio_device = Müller Lautsprecher\n",
		"a name with several spaces":    "audio_device = Built-in  Audio\n",
		"a name with other punctuation": "audio_device = USB Audio; Port 2 & 3!\n",
	}
	for name, doc := range accepted {
		if err := checkSyntax("config.toml", []byte(doc)); err != nil {
			t.Errorf("%s: %v, want it accepted:\n%s", name, err, doc)
		}
	}
	refused := map[string]struct {
		toml string
		line int
	}{
		"after a real section":                         {"[spotify]\nbitrate = 320\naudio_device = default\n", 3},
		"after a real section and a multi-line string": {"[spotify]\ntext = \"\"\"\nx\n\"\"\"\naudio_device = default\n", 5},
		"after an array of tables":                     {"[[stations]]\nname = \"x\"\naudio_device = default\n", 3},
		"after an indented section":                    {"  [spotify]\naudio_device = default\n", 2},
		"an unrelated error after a multi-line string": {"text = \"\"\"\n[not a section\n\"\"\"\ntheme = Tokyo Night\n", 4},
		"a number, not a name":                         {"audio_device = 01\n", 1},
		"a second equals sign":                         {"audio_device = = default\n", 1},
		"a name with an equals sign":                   {"audio_device = a=b\n", 1},
		"a name with a bracket":                        {"audio_device = [hw0\n", 1},
		"a name with a control character":              {"audio_device = hw\x010\n", 1},
	}
	for name, tt := range refused {
		err := checkSyntax("config.toml", []byte(tt.toml))
		var syntax *SyntaxError
		if !errors.As(err, &syntax) || syntax.Line != tt.line {
			t.Errorf("%s: checkSyntax = %v, want a syntax error at line %d", name, err, tt.line)
		}
	}
}

// Round 4, M7: a multi-line string may end in a run of four or five quotes
// (one or two of them belong to the value). The old Save wrote the device
// line after such a string like after any other, and Load read it back.
func TestLegacyDeviceLineAfterAQuoteRunThatEndsAMultilineString(t *testing.T) {
	accepted := map[string]string{
		"a basic multi-line string ending in four quotes":   "text = \"\"\"ends with quote\"\"\"\"\nvolume = -3\n\naudio_device = default\n",
		"a literal multi-line string ending in four quotes": "text = '''ends with quote''''\nvolume = -3\n\naudio_device = default\n",
		"a basic multi-line string ending in five quotes":   "text = \"\"\"ends with two\"\"\"\"\"\naudio_device = default\n",
		"a literal multi-line string ending in five quotes": "text = '''ends with two'''''\naudio_device = default\n",
		"four quotes over several lines":                    "text = \"\"\"\nline\nlast\"\"\"\"\naudio_device = default\n",
		"an escaped quote before the closing three":         "text = \"\"\"a \\\"\"\"\"\naudio_device = default\n",
		"quotes inside, then a plain closing":               "text = \"\"\"a \"\" b\"\"\"\naudio_device = default\n",
		"two such strings":                                  "a = \"\"\"x\"\"\"\"\nb = '''y''''\naudio_device = default\n",
	}
	for name, doc := range accepted {
		// Each prefix is valid TOML by itself: the fixture is what it claims.
		prefix := doc[:strings.Index(doc, "audio_device")]
		if err := parseTOML(prefix); err != nil {
			t.Fatalf("%s: the fixture's prefix is not valid TOML: %v", name, err)
		}
		if err := checkSyntax("config.toml", []byte(doc)); err != nil {
			t.Errorf("%s: %v, want it accepted:\n%s", name, err, doc)
		}
	}
	refused := map[string]struct {
		toml string
		line int
	}{
		"a real section after a four-quote string":         {"text = \"\"\"x\"\"\"\"\n[spotify]\naudio_device = default\n", 3},
		"an unrelated error after a four-quote string":     {"text = '''x''''\ntheme = Tokyo Night\n", 2},
		"a section opened inside nothing: still a section": {"text = \"\"\"x\"\"\"\"\n\n[a]\nb = 1\naudio_device = default\n", 5},
	}
	for name, tt := range refused {
		err := checkSyntax("config.toml", []byte(tt.toml))
		var syntax *SyntaxError
		if !errors.As(err, &syntax) || syntax.Line != tt.line {
			t.Errorf("%s: checkSyntax = %v, want a syntax error at line %d", name, err, tt.line)
		}
	}
}
