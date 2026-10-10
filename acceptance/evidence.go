package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// EvidenceRoot is where evidence bundles are kept by default: a private
// folder in ddsonic's data directory (~/.local/share/ddsonic/acceptance).
// It is durable and outside git; a run's short-lived files live elsewhere.
func EvidenceRoot() (string, error) {
	dir, err := appdir.DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "acceptance"), nil
}

// CheckEvidenceRoot refuses a place evidence must not be kept: a temporary
// directory, which does not last, or the repository, which is public. It
// judges where the directory really is: symbolic links, in the directory or
// in any directory above it, are followed first, so a link cannot lead
// evidence into either.
func CheckEvidenceRoot(dir, repo string) error {
	real, err := resolvePath(dir)
	if err != nil {
		return fmt.Errorf("evidence directory %s: %w", dir, err)
	}
	for _, tmp := range []string{os.TempDir(), "/tmp", "/var/tmp"} {
		realTmp, err := resolvePath(tmp)
		if err != nil {
			continue // no such temporary directory on this system
		}
		if within(real, realTmp) {
			return fmt.Errorf("evidence directory %s is temporary storage (%s): evidence must be durable", real, realTmp)
		}
	}
	if repo != "" {
		realRepo, err := resolvePath(repo)
		if err != nil {
			return fmt.Errorf("repository %s: %w", repo, err)
		}
		if within(real, realRepo) {
			return fmt.Errorf("evidence directory %s is inside the repository: raw evidence stays out of git", real)
		}
	}
	return nil
}

// resolvePath returns path as an absolute path with every symbolic link
// followed. The part of it that does not exist yet has no links to follow
// and is appended as written.
func resolvePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rest := ""
	for existing := abs; ; existing = filepath.Dir(existing) {
		real, err := filepath.EvalSymlinks(existing)
		if err == nil {
			return filepath.Join(real, rest), nil
		}
		if !errors.Is(err, fs.ErrNotExist) || existing == filepath.Dir(existing) {
			return "", err
		}
		rest = filepath.Join(filepath.Base(existing), rest)
	}
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Bundle is one run's evidence while it is being collected: a private
// directory that Seal turns into one redacted, checksummed archive.
type Bundle struct {
	Dir string
}

// NewBundle creates the bundle directory for a run under root.
func NewBundle(root, runID string) (*Bundle, error) {
	dir := filepath.Join(root, runID)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("evidence bundle: %w", err)
	}
	return &Bundle{Dir: dir}, nil
}

// Sealed is a sealed bundle: its archive and the archive's checksum.
type Sealed struct {
	Archive string
	SHA256  string
}

// Seal redacts every file in the bundle, packs the directory into
// <dir>.tar.gz, writes the archive's checksum beside it and removes the
// directory, so that what remains is the redacted archive alone.
func (b *Bundle) Seal(r *Redactor) (Sealed, error) {
	var files []string
	err := filepath.WalkDir(b.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, path)
		} else if !d.IsDir() {
			return fmt.Errorf("evidence holds %s, which is neither a file nor a directory", path)
		}
		return nil
	})
	if err != nil {
		return Sealed{}, err
	}
	slices.Sort(files)
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return Sealed{}, err
		}
		if err := os.WriteFile(path, r.Redact(data), 0o600); err != nil {
			return Sealed{}, err
		}
	}

	archive := b.Dir + ".tar.gz"
	if err := pack(archive, filepath.Dir(b.Dir), files); err != nil {
		return Sealed{}, err
	}
	sum, err := FileSHA256(archive)
	if err != nil {
		return Sealed{}, err
	}
	line := fmt.Sprintf("%s  %s\n", sum, filepath.Base(archive))
	if err := os.WriteFile(archive+".sha256", []byte(line), 0o600); err != nil {
		return Sealed{}, err
	}
	if err := os.RemoveAll(b.Dir); err != nil {
		return Sealed{}, err
	}
	return Sealed{Archive: archive, SHA256: sum}, nil
}

func pack(archive, base string, files []string) (err error) {
	out, err := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, out.Close()) }()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0o600, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// Redactor removes what must not leave the machine from evidence: the
// user's name and home directory, the host's name, and credentials in the
// forms they ordinarily take. One Redactor is the policy for everything a
// run persists or prints: the files of the bundle and the summary alike.
type Redactor struct {
	literals [][2]string
}

// secretKey is a setting, field or parameter name that holds a credential.
const secretKey = `[A-Za-z0-9_.-]*(?:passw(?:or)?d|secret|token|api[_-]?key|authorization|cookies?|sp_dc|sp_key|sapisid|session[_-]?id)[A-Za-z0-9_.-]*`

var (
	secretName = regexp.MustCompile(`(?i)^` + secretKey + `$`)
	// A credential header: everything after the name, to the end of the line.
	secretHeader = regexp.MustCompile(`(?im)^(\s*(?:(?:proxy-)?authorization|cookie|set-cookie|x-[a-z0-9-]*(?:key|token|auth|secret)[a-z0-9-]*)\s*:)[^\n]*$`)
	// The start of a secret pair: a key that names a credential, bare or in
	// single or double quotes (themselves possibly backslash-escaped, as
	// inside a JSON string), then ":" or "=". The value is read by
	// redactPairs, which knows the forms a value takes.
	secretPairStart = regexp.MustCompile(`(?i)(?:\\?["'])?` + secretKey + `(?:\\?["'])?[ \t]*[:=][ \t]*`)
	// Authorization schemes wherever they appear.
	secretScheme = regexp.MustCompile(`(?i)\b(bearer|basic)(\s+)[A-Za-z0-9._~+/=-]{8,}`)
	// user:password@ in a URL.
	secretUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s:@"']+:[^@\s/"']*@`)
	// A credential among a URL's query parameters.
	secretQuery = regexp.MustCompile(`(?i)([?&](?:access_token|refresh_token|id_token|token|key|api_?key|sig|signature|password|auth|code|session|sid)=)[^&\s"'#\\]+`)
	// A session cookie by name, wherever a name=value pair appears.
	secretCookie = regexp.MustCompile(`\b((?:__Secure-|__Host-)?[A-Za-z0-9]*(?:SID|SAPISID|APISID|LOGIN_INFO|sp_dc|sp_key|sp_t))=[^;\s"'\\]+`)
	cookieName   = regexp.MustCompile(`(?i)(?:SID|SAPISID|APISID|LOGIN_INFO|sp_dc|sp_key|sp_t|session|token|auth)`)
)

// NewRedactor returns a Redactor for this machine and user. extra are other
// strings to remove, each replaced by "<redacted>".
func NewRedactor(extra ...string) *Redactor {
	r := &Redactor{}
	add := func(secret, replacement string) {
		if len(secret) > 2 {
			r.literals = append(r.literals, [2]string{secret, replacement})
		}
	}
	for _, s := range extra {
		add(s, "<redacted>")
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home, "<home>")
	}
	if u, err := user.Current(); err == nil {
		add(u.Username, "<user>")
	}
	if host, err := os.Hostname(); err == nil {
		add(host, "<host>")
	}
	// Longest first, so a home directory goes before the user name inside it.
	slices.SortStableFunc(r.literals, func(a, b [2]string) int { return len(b[0]) - len(a[0]) })
	return r
}

// Redact returns data with everything the Redactor knows of removed.
func (r *Redactor) Redact(data []byte) []byte { return []byte(r.RedactString(string(data))) }

// RedactString is Redact for text. JSON is redacted as JSON: a document, or
// each line that is one (as in "go test -json" output), is decoded, its
// strings redacted as text in their own right (so a credential inside a
// JSON string, itself JSON or TOML, is found) and the values of secret keys
// removed whole, and it is encoded again, so it stays valid JSON. Everything
// else is redacted as text.
//
// "go test -json" gives every printed line an output event of its own, so a
// value written over several lines arrives in pieces, none of which shows
// what it is part of. The output of each test is therefore put back
// together, redacted as the one text it was, and handed back to its events
// (redactOutputStreams).
func (r *Redactor) RedactString(s string) string {
	for _, lit := range r.literals {
		s = strings.ReplaceAll(s, lit[0], lit[1])
	}
	if doc, ok := r.redactJSON(s); ok {
		return doc
	}
	lines := strings.SplitAfter(s, "\n")
	outputs := r.redactOutputStreams(lines)
	var out strings.Builder
	var text strings.Builder // consecutive lines that are not JSON
	flush := func() {
		out.WriteString(redactText(text.String()))
		text.Reset()
	}
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		doc, ok := "", false
		if output, isEvent := outputs[i]; isEvent {
			doc, ok = r.redactEvent(body, output)
		} else {
			doc, ok = r.redactJSON(body)
		}
		if ok {
			flush()
			out.WriteString(doc + line[len(body):])
			continue
		}
		text.WriteString(line)
	}
	flush()
	return out.String()
}

// outputEvent reports whether a line is a "go test -json" output event, and
// gives the stream it belongs to (its package and test) and its text.
func outputEvent(line string) (stream, output string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return "", "", false
	}
	var ev struct {
		Action  string
		Package string
		Test    string
		Output  *string
	}
	if json.Unmarshal([]byte(trimmed), &ev) != nil || ev.Action != "output" || ev.Output == nil {
		return "", "", false
	}
	return ev.Package + "\x00" + ev.Test, *ev.Output, true
}

// redactOutputStreams finds the output events among lines and returns, for
// each such line, the text its Output is to hold after redaction.
//
// The events of one test (or of the package itself) are one stream. Its
// pieces are joined in order and redacted together, so a credential that
// spans events is seen whole. Text redaction keeps the number of lines, so
// the result can be dealt back out, each event getting as many lines as it
// had. Where that cannot be done safely (an event that is not whole lines,
// or a result with another number of lines), the stream's first event gets
// all of the redacted text and the others are emptied: nothing is left in a
// later event that was judged without what came before it.
func (r *Redactor) redactOutputStreams(lines []string) map[int]string {
	type piece struct {
		line   int
		output string
	}
	streams := map[string][]piece{}
	var order []string
	for i, line := range lines {
		stream, output, ok := outputEvent(line)
		if !ok {
			continue
		}
		if _, seen := streams[stream]; !seen {
			order = append(order, stream)
		}
		streams[stream] = append(streams[stream], piece{i, output})
	}
	outputs := map[int]string{}
	for _, stream := range order {
		pieces := streams[stream]
		var joined strings.Builder
		wholeLines := true
		for n, p := range pieces {
			joined.WriteString(p.output)
			if n < len(pieces)-1 && !strings.HasSuffix(p.output, "\n") {
				wholeLines = false
			}
		}
		redacted := r.RedactString(joined.String())
		if !wholeLines || strings.Count(redacted, "\n") != strings.Count(joined.String(), "\n") {
			for n, p := range pieces {
				outputs[p.line] = ""
				if n == 0 {
					outputs[p.line] = redacted
				}
			}
			continue
		}
		rest := redacted
		for n, p := range pieces {
			take := len(rest)
			if n < len(pieces)-1 {
				take = 0
				for range strings.Count(p.output, "\n") {
					take += strings.IndexByte(rest[take:], '\n') + 1
				}
			}
			outputs[p.line], rest = rest[:take], rest[take:]
		}
	}
	return outputs
}

// redactEvent re-encodes an output event with its Output replaced by the
// text redactOutputStreams gave it, and its other fields redacted as JSON.
func (r *Redactor) redactEvent(line, output string) (string, bool) {
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(line)))
	dec.UseNumber()
	var ev map[string]any
	if err := dec.Decode(&ev); err != nil {
		return "", false
	}
	delete(ev, "Output")
	ev, _ = r.redactValue(ev).(map[string]any)
	ev["Output"] = output
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(ev); err != nil {
		return "", false
	}
	return strings.TrimRight(buf.String(), "\n"), true
}

// redactJSON redacts s when it is one JSON object or array, and reports
// whether it was.
func (r *Redactor) redactJSON(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) < 2 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil || dec.More() {
		return "", false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if strings.Contains(trimmed, "\n") {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(r.redactValue(v)); err != nil {
		return "", false
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " \t\r\n"))]
	trail := s[len(strings.TrimRight(s, " \t\r\n")):]
	return lead + strings.TrimRight(buf.String(), "\n") + trail, true
}

// redactValue redacts a decoded JSON value.
func (r *Redactor) redactValue(v any) any {
	switch v := v.(type) {
	case string:
		return r.RedactString(v)
	case []any:
		for i := range v {
			v[i] = r.redactValue(v[i])
		}
	case map[string]any:
		// A cookie as browsers and yt-dlp list them: {"name": …, "value": …}.
		if name, ok := v["name"].(string); ok && cookieName.MatchString(name) {
			if _, has := v["value"]; has {
				v["value"] = "<redacted>"
			}
		}
		for key, value := range v {
			if secretName.MatchString(key) {
				v[key] = "<redacted>" // whatever it holds: a string, a list of cookies, an object
			} else {
				v[key] = r.redactValue(value)
			}
		}
	}
	return v
}

// redactText redacts text that is not JSON.
func redactText(s string) string {
	// Text that is a YAML mapping is first redacted as YAML, by the parser's
	// own account of where each value lies. The rules below then apply to
	// it as to any text: they find what is left in forms that are not YAML,
	// and they are all there is for text the parser does not accept, such
	// as a log with a YAML fragment in it.
	if doc, ok := redactYAMLMapping(s); ok {
		s = doc
	}
	s = secretHeader.ReplaceAllString(s, "${1} <redacted>")
	s = redactPairs(s)
	s = secretScheme.ReplaceAllString(s, "${1}${2}<redacted>")
	s = secretUserinfo.ReplaceAllString(s, "${1}<redacted>@")
	s = secretQuery.ReplaceAllString(s, "${1}<redacted>")
	s = secretCookie.ReplaceAllString(s, "${1}=<redacted>")
	return s
}

// redactYAMLMapping redacts s when it is one YAML document that is a
// mapping, and reports whether it was. The YAML parser says where every key
// is. The value of a secret-named key is everything from that key to the
// next key the parser found after it (or the end of the text), whatever
// form the value takes and however it is indented, commented or anchored;
// all of it is replaced, line for line.
func redactYAMLMapping(s string) (out string, ok bool) {
	if !secretPairStart.MatchString(s) {
		return "", false
	}
	defer func() {
		if recover() != nil { // a parser that gives up is no reason to stop
			out, ok = "", false
		}
	}()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(s), &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", false
	}
	lines := strings.SplitAfter(s, "\n")
	type region struct{ line, column, until int } // the key's place, and the line the next key is on
	var regions []region
	var walk func(n *yaml.Node, until int)
	walk = func(n *yaml.Node, until int) {
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				key, value, next := n.Content[i], n.Content[i+1], until
				if i+2 < len(n.Content) {
					next = n.Content[i+2].Line
				}
				if key.Kind == yaml.ScalarNode && secretName.MatchString(key.Value) {
					regions = append(regions, region{key.Line, key.Column, next})
					continue
				}
				walk(value, next)
			}
		case yaml.SequenceNode:
			for i, item := range n.Content {
				next := until
				if i+1 < len(n.Content) {
					next = n.Content[i+1].Line
				}
				walk(item, next)
			}
		}
	}
	walk(doc.Content[0], len(lines)+1)
	for _, r := range regions {
		if r.line < 1 || r.line > len(lines) || r.until <= r.line {
			continue // the next key is on the same line (a flow mapping): left to the rules for text
		}
		line := lines[r.line-1]
		body := strings.TrimRight(line, "\r\n")
		runes := []rune(body)
		if r.column < 1 || r.column > len(runes) {
			continue
		}
		start := len(string(runes[:r.column-1]))
		loc := secretPairStart.FindStringIndex(body[start:])
		if loc == nil || loc[0] != 0 {
			continue
		}
		indent := len(body) - len(strings.TrimLeft(body, " \t"))
		lines[r.line-1] = body[:start+loc[1]] + "<redacted>" + line[len(body):]
		for n := r.line; n < r.until-1 && n < len(lines); n++ {
			rest := strings.TrimRight(lines[n], "\r\n")
			if strings.TrimSpace(rest) != "" {
				lines[n] = strings.Repeat(" ", indent+2) + "<redacted>" + lines[n][len(rest):]
			}
		}
	}
	return strings.Join(lines, ""), true
}

// redactPairs removes the value after every key that names a credential,
// whole, in whichever form it is written, by the rules that form has (see
// redactedValue). The text keeps its number of lines: a value over several
// lines leaves a "<redacted>" on each, so what follows keeps its place.
func redactPairs(s string) string {
	var out strings.Builder
	for {
		loc := secretPairStart.FindStringIndex(s)
		if loc == nil {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:loc[1]])
		lineStart := strings.LastIndexByte(s[:loc[0]], '\n') + 1
		indent := len(s[lineStart:loc[0]]) - len(strings.TrimLeft(s[lineStart:loc[0]], " \t"))
		colon := strings.HasSuffix(strings.TrimRight(s[loc[0]:loc[1]], " \t"), ":")
		replacement, rest := redactedValue(s[loc[1]:], indent, colon)
		out.WriteString(replacement)
		s = rest
	}
}

// yamlBlockHeader is what follows "key:" when the value is a YAML block
// scalar: "|" or ">", optional chomping and indentation indicators in
// either order, and optionally a comment.
var yamlBlockHeader = regexp.MustCompile(`^[|>](?:[+-][1-9]?|[1-9][+-]?)?[ \t]*(?:#.*)?$`)

// yamlProperties are what YAML allows before a value: an anchor (&name) and
// a tag (!tag, !!tag), in either order.
var yamlProperties = regexp.MustCompile(`^(?:[&!]\S*[ \t]*)+`)

// redactedValue reads the value that starts value and returns what to write
// in its place and what follows it. indent is the indentation of the line
// the key is on, and colon says the key was followed by ":" (YAML, JSON)
// and not "=" (TOML, an environment, a query string). The forms, each read
// to its real end:
//
//   - a TOML multi-line string, """…""" or ”'…”': over any number of
//     lines; in the basic form a backslash escapes the next character (so
//     \""" does not close it); and it may close on a run of four or five
//     quotes, the first of which are its own last characters;
//   - a quoted string: "…" with backslash escapes, '…' with YAML's doubled
//     apostrophe (”) for an apostrophe; the same two in backslash-escaped
//     quotes, as inside a JSON string. After "=" it ends on its line. After
//     ":" it may run on, as a YAML scalar does, over the following lines
//     that are indented deeper than the key;
//   - a bracketed list or object, to its matching bracket;
//   - after ":", any other YAML value, with the lines that belong to it,
//     which are the following lines indented deeper than the key (YAML's
//     own rule for what continues a value): a block scalar (yamlBlockHeader)
//     and its body; a plain scalar, all of it, spaces included, and its
//     continuation lines; and a block collection, which may also be written
//     as "- …" items at the key's own indentation. An anchor or a tag
//     before the value (yamlProperties) is part of it;
//   - after "=", a bare word.
//
// Where a value's end cannot be told for certain (a quote that never
// closes), everything that could still belong to it is removed. The text
// keeps its number of lines throughout.
func redactedValue(value string, indent int, colon bool) (replacement, rest string) {
	// An anchor or a tag comes before a YAML value of any form, so it is
	// taken off first; what follows it decides how the value is read.
	if colon {
		value = value[len(yamlProperties.FindString(value)):]
	}
	// lines is the replacement for the rest of a value that ran over
	// several lines: one mark per line it went on to.
	lines := func(consumed string) string {
		return strings.Repeat("\n<redacted>", strings.Count(consumed, "\n"))
	}
	depthOf := func(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }
	// aside reports a line that is no part of any value and ends none: a
	// blank line, or a comment at the key's indentation or less.
	aside := func(line string) bool {
		text := strings.TrimSpace(line)
		return text == "" || (strings.HasPrefix(text, "#") && depthOf(line) <= indent)
	}
	// continuation splits after, the text that follows the value's first
	// line, into the lines that still belong to the value and the rest.
	// Lines indented deeper than the key belong; blank lines and comment
	// lines neither belong nor end it. items says that "- …" at the key's
	// own indentation belongs too.
	continuation := func(after string, items bool) (belongs, rest string) {
		end := 0
		for end < len(after) {
			next, _, _ := strings.Cut(after[end:], "\n")
			depth := depthOf(next)
			item := items && depth == indent && (strings.HasPrefix(next[depth:], "- ") || strings.TrimRight(next[depth:], " \t\r") == "-")
			if !aside(next) && depth <= indent && !item {
				break
			}
			end += len(next)
			if end < len(after) {
				end++ // its line end
			}
		}
		return after[:end], after[end:]
	}
	// mask replaces every line of a continuation that is part of the value.
	mask := func(belongs string) string {
		var b strings.Builder
		for line := range strings.SplitAfterSeq(belongs, "\n") {
			body := strings.TrimRight(line, "\r\n")
			if aside(body) {
				b.WriteString(line)
				continue
			}
			b.WriteString(strings.Repeat(" ", indent+2) + "<redacted>" + line[len(body):])
		}
		return b.String()
	}
	// quoted reads a quoted string that starts value, looking for its
	// closing quote no further than within, and reports whether it found it.
	quoted := func(open, closer string, within int) (string, string, bool) {
		body := value[len(open):within]
		for i := 0; i < len(body); i++ {
			switch {
			case body[i] == '\\' && (open == `"` || open == `"""`):
				i++ // an escaped character, which may be a quote
			case open == `'` && strings.HasPrefix(body[i:], `''`):
				i++ // YAML's doubled apostrophe
			case strings.HasPrefix(body[i:], closer):
				end := i + len(closer)
				// A multi-line string may close on four or five quotes.
				for extra := 0; len(closer) == 3 && extra < 2 && end < len(body) && body[end] == closer[0]; extra++ {
					end++
				}
				return open + "<redacted>" + lines(body[:end]) + closer, value[len(open)+end:], true
			}
		}
		// Never closed within its reach: all of it, to be safe. A line end
		// that finishes it stays a line end.
		kept := ""
		if strings.HasSuffix(body, "\n") {
			body, kept = body[:len(body)-1], "\n"
		}
		return open + "<redacted>" + lines(body) + closer + kept, value[within:], false
	}
	line, after, more := strings.Cut(value, "\n")
	// yamlQuoted reads a quoted YAML scalar. It ends at its closing quote,
	// on whatever line and at whatever indentation that is: YAML does not
	// ask a quoted scalar's lines to be indented at the top level, and a
	// reader that did would stop short of the value's end. Only when no
	// closing quote exists at all is the value bounded by indentation: then
	// the lines that could still belong to it are removed, and no more.
	yamlQuoted := func(quote string) (string, string) {
		if replacement, rest, found := quoted(quote, quote, len(value)); found {
			return replacement, rest
		}
		reach := len(value)
		if more {
			belongs, _ := continuation(after, false)
			reach = len(line) + 1 + len(belongs)
		}
		replacement, rest, _ := quoted(quote, quote, reach)
		return replacement, rest
	}
	onLine := func(open string) (string, string) {
		replacement, rest, _ := quoted(open, open, len(line))
		return replacement, rest
	}
	switch {
	case strings.HasPrefix(value, `"""`):
		replacement, rest, _ := quoted(`"""`, `"""`, len(value))
		return replacement, rest
	case strings.HasPrefix(value, `'''`):
		replacement, rest, _ := quoted(`'''`, `'''`, len(value))
		return replacement, rest
	case strings.HasPrefix(value, `\"`):
		return onLine(`\"`)
	case strings.HasPrefix(value, `\'`):
		return onLine(`\'`)
	case strings.HasPrefix(value, `"`) && colon:
		return yamlQuoted(`"`)
	case strings.HasPrefix(value, `'`) && colon:
		return yamlQuoted(`'`)
	case strings.HasPrefix(value, `"`):
		return onLine(`"`)
	case strings.HasPrefix(value, `'`):
		return onLine(`'`)
	case strings.HasPrefix(value, "[") || strings.HasPrefix(value, "{"):
		// A flow collection ends at its matching bracket, wherever that is.
		end := containerEnd(value, !colon)
		return `"<redacted>"` + lines(value[:end]), value[end:]
	}
	if !colon {
		// After "=": a bare word.
		end := strings.IndexAny(value, " \t\r\n\"',;&}]\\")
		if end < 0 {
			end = len(value)
		}
		if end == 0 {
			return "", value // nothing on this line to remove
		}
		return "<redacted>", value[end:]
	}
	// After ":": a YAML value, with the lines that belong to it.
	first := strings.TrimRight(line, " \t\r")
	nothingHere := first == "" || strings.HasPrefix(first, "#")
	belongs := ""
	if more {
		belongs, after = continuation(after, nothingHere)
	}
	lineEnd := ""
	if more {
		lineEnd = "\n"
	}
	switch {
	case yamlBlockHeader.MatchString(first):
		return first[:1] + lineEnd + mask(belongs), after
	case nothingHere && mask(belongs) == belongs:
		// "key:" and nothing under it: there is no value to remove.
		return "", value
	case nothingHere:
		return lineEnd + mask(belongs), after
	}
	return "<redacted>" + lineEnd + mask(belongs), after
}

// containerEnd is where the bracketed value that starts s ends: after the
// bracket that closes the first one, or at the end of s when it is never
// closed. It reads the collection as YAML and TOML write one, so that only
// brackets that are syntax are counted:
//
//   - a comment runs from "#" to the end of its line, and whatever it holds
//     (a bracket, a brace, a quote) is not syntax. Where a comment may start
//     differs, and tomlSyntax says which rules apply. The key's own
//     separator tells: "=" is TOML, ":" is YAML. In TOML a "#" outside a
//     string always starts a comment, straight after a value too (1# c):
//     no bare TOML value holds one. In YAML a "#" starts a comment at the
//     start of a line, after a space, or after a delimiter or a string;
//     inside a word (a#b) it is part of the word;
//   - a string is skipped whole: double-quoted with backslash escapes,
//     single-quoted with no escapes but YAML's doubled apostrophe, and
//     TOML's triple-quoted strings of either kind, over as many lines as
//     they run and to the end of their closing run of three, four or five
//     quotes.
func containerEnd(s string, tomlSyntax bool) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '#':
			if !tomlSyntax && i > 0 && !strings.ContainsRune(" \t\r\n,[]{}\"'", rune(s[i-1])) {
				continue // YAML: inside a word
			}
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				return len(s) // a comment to the end: never closed
			}
			i += end // the line end; the loop moves past it
		case '"', '\'':
			quote := string(c)
			if triple := strings.Repeat(quote, 3); strings.HasPrefix(s[i:], triple) {
				quote = triple
			}
			i += len(quote)
			for ; i < len(s) && !strings.HasPrefix(s[i:], quote); i++ {
				switch {
				case c == '"' && s[i] == '\\':
					i++ // an escaped character
				case quote == "'" && strings.HasPrefix(s[i:], "''"):
					i++ // a doubled apostrophe
				}
			}
			i += len(quote) - 1
			// A multi-line string may close on four or five quotes: the
			// first one or two are its own last characters. The run is
			// taken whole, as redactedValue takes it, so that what follows
			// is read as the next item and not as a string that never was.
			for extra := 0; len(quote) == 3 && extra < 2 && i+1 < len(s) && s[i+1] == c; extra++ {
				i++
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}
