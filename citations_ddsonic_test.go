package main

// ddsonic: docs/ddsonic/POST-1.0-CATALOG.md cites other files by line
// ("plan.md:604", "CHANGELOG.md:44-48"). A change must not move a cited
// line: it may rewrite one in place, but not insert or delete lines above
// it. CHANGELOG.md is the exception (shiftedFiles): a release adds its
// section at the top, so its cited lines may move, but their text must not
// change. Its line numbers are read at a fixed origin commit, not the base. This test compares the working tree with a base: DDSONIC_CITATION_BASE
// when set (make validate sets it), else the merge base with origin/main.
// Without either it is skipped and says "citation check: SKIPPED", unless
// DDSONIC_CITATION_STRICT=1, as make validate and CI's docs profile set: then
// a missing base is a failure.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	citation = regexp.MustCompile("`?(?:docs/ddsonic/(?:design/)?)?(IDEAS|RP|plan\\.md|CHANGELOG(?:\\.md)?|README(?:\\.md)?|releasing\\.md|navigation\\.md|SECURITY(?:\\.md)?|acceptance\\.md|CLAUDE\\.md|Makefile|spotify-token-coordinator\\.md)`?:((?:\\d+(?:-\\d+)?(?:, )?)+)")
	hunk     = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	cited    = map[string]string{
		"IDEAS": "docs/ddsonic/POST-1.0-IDEAS.md", "RP": "docs/ddsonic/release-plan.md", "plan.md": "plan.md",
		"CHANGELOG": "CHANGELOG.md", "CHANGELOG.md": "CHANGELOG.md", "README": "README.md", "README.md": "README.md",
		"releasing.md": "docs/ddsonic/releasing.md", "navigation.md": "docs/ddsonic/navigation.md",
		"SECURITY": "SECURITY.md", "SECURITY.md": "SECURITY.md", "acceptance.md": "docs/ddsonic/acceptance.md",
		"CLAUDE.md": "CLAUDE.md", "Makefile": "Makefile", "spotify-token-coordinator.md": "docs/ddsonic/design/spotify-token-coordinator.md",
	}
)

func gitOut(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	return string(out), err
}

// changelogCitationOrigin is the commit whose CHANGELOG.md the catalog's
// CHANGELOG line numbers describe. The archived catalog is never renumbered,
// so its numbers stay in this commit's coordinates however far the file
// grows: they must be translated from here, not from the validation base,
// which moves with every merge (from a base after the 1.1.0 section, the
// catalog's "44" would name some other line). Never change it.
const changelogCitationOrigin = "4ac92beb87313c74027dcfd4262b76732d1f5fe4"

// shiftedFiles are cited files that grow at the top, each with the commit
// its citations' line numbers belong to. A cited line may move with lines
// inserted or deleted above it, but must keep its text exactly, at the
// line the diff from that commit moves it to. Editing or deleting it fails.
var shiftedFiles = map[string]string{"CHANGELOG.md": changelogCitationOrigin}

// shift is one hunk of git diff -U0: old lines [start, start+count) became
// newCount lines.
type shift struct{ start, count, newCount int }

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, _ := strconv.Atoi(s)
	return n
}

// moved reports whether line n of the base is no longer at line n: a hunk
// before it changed the number of lines, or it lies in a hunk that did.
func moved(n int, hunks []shift) bool {
	offset := 0
	for _, h := range hunks {
		end := h.start + h.count - 1 // the hunk's last old line; for an insertion (count 0), the line it follows
		if h.count == 0 {
			end = h.start
		}
		switch {
		case h.count > 0 && n >= h.start && n <= end:
			if h.count != h.newCount {
				return true
			}
		case end < n:
			offset += h.newCount - h.count
		}
	}
	return offset != 0
}

// parseHunks reads the hunks of a git diff -U0.
func parseHunks(diff string) []shift {
	var hunks []shift
	for _, l := range strings.Split(diff, "\n") {
		if m := hunk.FindStringSubmatch(l); m != nil {
			hunks = append(hunks, shift{atoiOr(m[1], 0), atoiOr(m[2], 1), atoiOr(m[4], 1)})
		}
	}
	return hunks
}

// translate is the line that base line n is at after the hunks, or false
// when a hunk rewrote or deleted it.
func translate(n int, hunks []shift) (int, bool) {
	offset := 0
	for _, h := range hunks {
		if h.count > 0 && n >= h.start && n < h.start+h.count {
			return 0, false
		}
		end := h.start + h.count - 1 // as in moved: an insertion follows line start
		if h.count == 0 {
			end = h.start
		}
		if end < n {
			offset += h.newCount - h.count
		}
	}
	return n + offset, true
}

// shiftedCitation checks base line n of a shifted file: "" when its text is
// unchanged at the line the hunks move it to, else what happened to it.
func shiftedCitation(n int, hunks []shift, base, cur string) string {
	b, c := strings.Split(base, "\n"), strings.Split(cur, "\n")
	if n < 1 || n > len(b) {
		return "is past the end of the base file"
	}
	m, ok := translate(n, hunks)
	if !ok {
		return "has been edited or deleted"
	}
	if m < 1 || m > len(c) || c[m-1] != b[n-1] {
		return fmt.Sprintf("does not have its text at line %d", m)
	}
	return ""
}

// numberedAt is the commit a file's citations are numbered at: a shifted
// file's fixed origin, else the validation base.
func numberedAt(file, base string) (string, bool) {
	if origin, ok := shiftedFiles[file]; ok {
		return origin, true
	}
	return base, false
}

// shiftedFailures checks a shifted file's cited lines ns, numbered at
// origin, against cur, the file's text now: each is translated through git
// diff -U0 origin. git runs git in the repository. An origin the clone does
// not have is an error, never a pass.
func shiftedFailures(git func(...string) (string, error), file, origin, cur string, ns []int) ([]string, error) {
	if _, err := git("cat-file", "-e", origin+"^{commit}"); err != nil {
		return nil, fmt.Errorf("%s: its citation origin %.12s is not in this clone (fetch it, or the whole history): %v", file, origin, err)
	}
	diff, err := git("diff", "-U0", "--no-ext-diff", origin, "--", file)
	if err != nil {
		return nil, fmt.Errorf("git diff %.12s -- %s: %v", origin, file, err)
	}
	originText, err := git("show", origin+":"+file)
	if err != nil {
		return nil, fmt.Errorf("git show %.12s:%s: %v", origin, file, err)
	}
	hunks := parseHunks(diff)
	var bad []string
	for _, n := range ns {
		if why := shiftedCitation(n, hunks, originText, cur); why != "" {
			bad = append(bad, fmt.Sprintf("%s:%d is cited by POST-1.0-CATALOG.md and %s since %.12s, the commit its citations are numbered at: keep cited lines as they are", file, n, why, origin))
		}
	}
	return bad, nil
}

// citationBase chooses the base: the variable, else the merge base. It
// returns a reason to skip when there is none, or an error in strict mode.
func citationBase(getenv func(string) string, mergeBase func() (string, error)) (base, skip string, err error) {
	if base = getenv("DDSONIC_CITATION_BASE"); base != "" {
		return base, "", nil
	}
	out, mbErr := mergeBase()
	if mbErr == nil && strings.TrimSpace(out) != "" {
		return strings.TrimSpace(out), "", nil
	}
	if getenv("DDSONIC_CITATION_STRICT") == "1" {
		return "", "", errors.New("citation check: no base (DDSONIC_CITATION_BASE unset and no origin/main), and DDSONIC_CITATION_STRICT=1")
	}
	return "", "citation check: SKIPPED: no base to compare with: set DDSONIC_CITATION_BASE, or fetch origin/main (make validate does)", nil
}

func TestCatalogCitationsDoNotMove(t *testing.T) {
	base, skip, err := citationBase(os.Getenv, func() (string, error) { return gitOut(t, "merge-base", "origin/main", "HEAD") })
	if err != nil {
		t.Fatal(err)
	}
	if skip != "" {
		t.Skip(skip)
	}
	if _, err := gitOut(t, "cat-file", "-e", base+"^{commit}"); err != nil {
		t.Fatalf("the base %q is not a commit", base)
	}
	catalog, err := os.ReadFile("docs/ddsonic/POST-1.0-CATALOG.md")
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]map[int]bool{} // file -> cited lines
	for _, m := range citation.FindAllStringSubmatch(string(catalog), -1) {
		file := cited[m[1]]
		for _, part := range strings.Split(m[2], ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, b, _ := strings.Cut(part, "-")
			from, to := atoiOr(a, 0), atoiOr(b, atoiOr(a, 0))
			for n := from; n <= to; n++ {
				if lines[file] == nil {
					lines[file] = map[int]bool{}
				}
				lines[file][n] = true
			}
		}
	}
	checked := 0
	for file, ns := range lines {
		if origin, shifted := numberedAt(file, base); shifted {
			cur, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var sorted []int
			for n := range ns {
				sorted = append(sorted, n)
			}
			sort.Ints(sorted)
			checked += len(sorted)
			bad, err := shiftedFailures(func(args ...string) (string, error) { return gitOut(t, args...) }, file, origin, string(cur), sorted)
			if err != nil {
				t.Error(err)
			}
			for _, b := range bad {
				t.Error(b)
			}
			continue
		}
		diff, err := gitOut(t, "diff", "-U0", "--no-ext-diff", base, "--", file)
		if err != nil {
			t.Fatalf("git diff %s: %v", file, err)
		}
		hunks := parseHunks(diff)
		for n := range ns {
			checked++
			if moved(n, hunks) {
				t.Errorf("%s:%d is cited by POST-1.0-CATALOG.md and has moved since %.12s: edit cited lines in place", file, n, base)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no citation found: the pattern no longer matches the catalog")
	}
	t.Logf("%d cited lines in %d files, none moved since %.12s", checked, len(lines), base)
}

// The shift arithmetic, on hand-made hunks.
func TestCitationMoved(t *testing.T) {
	tests := []struct {
		name  string
		n     int
		hunks []shift
		want  bool
	}{
		{"no change", 10, nil, false},
		{"a line rewritten in place", 10, []shift{{10, 1, 1}}, false},
		{"a line inserted above", 10, []shift{{4, 0, 1}}, true},
		{"a line deleted above", 10, []shift{{4, 1, 0}}, true},
		{"a line inserted below", 10, []shift{{12, 0, 1}}, false},
		{"an insertion right after it", 10, []shift{{10, 0, 2}}, false},
		{"a hunk that grows it", 10, []shift{{9, 2, 3}}, true},
		{"one in and one out above", 10, []shift{{3, 0, 1}, {5, 1, 0}}, false},
	}
	for _, tt := range tests {
		if got := moved(tt.n, tt.hunks); got != tt.want {
			t.Errorf("%s: moved = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Strict mode turns a missing base into a failure; otherwise it is a skip
// that says so, never a pass.
func TestCitationBaseStrict(t *testing.T) {
	none := func() (string, error) { return "", errors.New("no origin/main") }
	found := func() (string, error) { return "abc123\n", nil }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	tests := []struct {
		name     string
		vars     map[string]string
		mb       func() (string, error)
		wantBase string
		wantSkip bool
		wantErr  bool
	}{
		{"the variable wins", map[string]string{"DDSONIC_CITATION_BASE": "deadbeef"}, none, "deadbeef", false, false},
		{"the merge base", nil, found, "abc123", false, false},
		{"no base: a skip that says so", nil, none, "", true, false},
		{"no base, strict: a failure", map[string]string{"DDSONIC_CITATION_STRICT": "1"}, none, "", false, true},
		{"strict with a base: runs", map[string]string{"DDSONIC_CITATION_STRICT": "1"}, found, "abc123", false, false},
	}
	for _, tt := range tests {
		base, skip, err := citationBase(env(tt.vars), tt.mb)
		if base != tt.wantBase || (skip != "") != tt.wantSkip || (err != nil) != tt.wantErr {
			t.Errorf("%s: base %q, skip %q, err %v", tt.name, base, skip, err)
		}
		if skip != "" && !strings.HasPrefix(skip, "citation check: SKIPPED") {
			t.Errorf("%s: the skip does not say citation check: SKIPPED: %q", tt.name, skip)
		}
	}
}

// A shifted file's cited line, on hand-made hunks.
func TestCitationTranslate(t *testing.T) {
	tests := []struct {
		name   string
		n      int
		hunks  []shift
		want   int
		wantOK bool
	}{
		{"no change", 10, nil, 10, true},
		{"lines inserted above", 10, []shift{{4, 0, 27}}, 37, true},
		{"inserted at the top", 10, []shift{{0, 0, 3}}, 13, true},
		{"a line deleted above", 10, []shift{{4, 1, 0}}, 9, true},
		{"inserted right after it", 10, []shift{{10, 0, 2}}, 10, true},
		{"inserted below", 10, []shift{{12, 0, 1}}, 10, true},
		{"rewritten in place", 10, []shift{{10, 1, 1}}, 0, false},
		{"deleted", 10, []shift{{10, 1, 0}}, 0, false},
		{"inside a hunk that grows", 10, []shift{{9, 2, 3}}, 0, false},
		{"inserted above and below", 10, []shift{{2, 0, 5}, {20, 0, 1}}, 15, true},
	}
	for _, tt := range tests {
		got, ok := translate(tt.n, tt.hunks)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%s: translate = %d, %v, want %d, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}

// The CHANGELOG rule on real git diffs of a changelog: a cited line may
// move down under a new section, but not be edited or deleted. (The test
// builds its own changelog: a test that read CHANGELOG.md would make it
// code, see TestReadOrEmbeddedFilesAreCodeOrAbove.)
func TestShiftedCitationOnGitDiffs(t *testing.T) {
	lines := []string{"# Changelog", "", "Notable changes.", "", "Older names.", "", "## [1.0.0] - 2026-10-31", "", "The first release.", "",
		"### Added", "- A release build.", "- `setup`.", "", "### Changed", "- Renamed.", "- Wording.", "", "### Known limitations", "- Spotify.", "", "[1.0.0]: https://example.invalid", ""}
	orig := strings.Join(lines, "\n")
	const cite = 16 // "- Renamed."
	with := func(edit func([]string) []string) string {
		return strings.Join(edit(append([]string(nil), lines...)), "\n")
	}
	section := []string{"## [9.9.9] - unreleased", "", "### Added", "- Something.", ""}
	tests := []struct {
		name string
		text string
		pass bool
	}{
		{"unchanged", orig, true},
		{"a section inserted above", with(func(l []string) []string {
			return append(l[:6:6], append(section, l[6:]...)...)
		}), true},
		{"a line deleted above", with(func(l []string) []string { return append(l[:2:2], l[3:]...) }), true},
		{"lines added below", with(func(l []string) []string { return append(l, "- later") }), true},
		{"the cited line edited in place", with(func(l []string) []string { l[cite-1] += " (edited)"; return l }), false},
		{"the cited line deleted", with(func(l []string) []string { return append(l[:cite-1:cite-1], l[cite:]...) }), false},
		{"a section above and the cited line edited", with(func(l []string) []string {
			l[cite-1] += " (edited)"
			return append(l[:6:6], append(section, l[6:]...)...)
		}), false},
	}
	for _, tt := range tests {
		f := &fixture{t: t, dir: t.TempDir()}
		f.git("init", "-q")
		f.write("CHANGELOG.md", orig)
		base := f.commit("base")
		f.write("CHANGELOG.md", tt.text)
		hunks := parseHunks(f.git("diff", "-U0", "--no-ext-diff", base, "--", "CHANGELOG.md"))
		why := shiftedCitation(cite, hunks, orig, tt.text)
		if (why == "") != tt.pass {
			t.Errorf("%s: line %d: %q, want pass %v", tt.name, cite, why, tt.pass)
		}
	}
}

// The text is compared too, not only the hunks: hunks that miss an edit (or
// a translation that lands on other text) still fail.
func TestShiftedCitationComparesText(t *testing.T) {
	base := "a\nb\nc\n"
	tests := []struct {
		name  string
		cur   string
		hunks []shift
		pass  bool
	}{
		{"the same text", base, nil, true},
		{"moved down with its hunk", "x\na\nb\nc\n", []shift{{0, 0, 1}}, true},
		{"edited, no hunk", "a\nB\nc\n", nil, false},
		{"moved down, no hunk", "x\na\nb\nc\n", nil, false},
		{"past the end of the new file", "a\n", []shift{{2, 2, 0}}, false},
	}
	for _, tt := range tests {
		if why := shiftedCitation(2, tt.hunks, base, tt.cur); (why == "") != tt.pass {
			t.Errorf("%s: %q, want pass %v", tt.name, why, tt.pass)
		}
	}
}

// The catalog's CHANGELOG numbers belong to a fixed origin, not to the
// validation base: once a release section is merged, the base moves to the
// merge, and an edit of a historical cited line must still fail.
func TestShiftedCitationFromAFixedOrigin(t *testing.T) {
	lines := []string{"# Changelog", "", "Notable changes.", "", "## [1.0.0] - 2026-10-31", "", "### Added", "- A release build.", "- `setup`.", "",
		"### Known limitations", "- Spotify.", "- Ogg.", "", "[1.0.0]: https://example.invalid", ""}
	const cite = 12 // "- Spotify.", in the origin's numbers
	join := func(l []string) string { return strings.Join(l, "\n") }
	section := []string{"## [1.1.0] - unreleased", "", "### Added", "- A sleep timer.", ""}
	merged := append(append(append([]string(nil), lines[:4]...), section...), lines[4:]...)
	at := cite + len(section) // where the cited line is after the merge

	dir := t.TempDir()
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir, cmd.Env = dir, gitEnv()
		out, err := cmd.Output()
		return string(out), err
	}
	f := &fixture{t: t, dir: dir}
	f.git("init", "-q")
	f.write("CHANGELOG.md", join(lines))
	origin := f.commit("origin: the catalog's numbers")
	f.write("CHANGELOG.md", join(merged))
	base := f.commit("the 1.1.0 section, merged: the validation base moves here")

	edited := append([]string(nil), merged...)
	edited[at-1] += " (edited)"
	deleted := append(append([]string(nil), merged[:at-1]...), merged[at:]...)
	tests := []struct {
		name string
		from string // the commit the citations are read at
		text string
		pass bool
	}{
		{"the merge, nothing else", origin, join(merged), true},
		{"after the merge, the historical line edited", origin, join(edited), false},
		{"after the merge, the historical line deleted", origin, join(deleted), false},
		{"after the merge, a line added above it", origin, join(append([]string{"# Changelog", "x"}, merged[1:]...)), true},
		// What a moving base would do: it reads "12" in the merge's
		// numbers, another line, and lets the edit through.
		{"the bug this prevents: the edit, read from the moved base", base, join(edited), true},
	}
	for _, tt := range tests {
		f.write("CHANGELOG.md", tt.text)
		bad, err := shiftedFailures(git, "CHANGELOG.md", tt.from, tt.text, []int{cite})
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if (len(bad) == 0) != tt.pass {
			t.Errorf("%s: %q, want pass %v", tt.name, bad, tt.pass)
		}
	}

	// An origin the clone does not have fails closed.
	f.write("CHANGELOG.md", join(merged))
	if bad, err := shiftedFailures(git, "CHANGELOG.md", strings.Repeat("0", 40), join(merged), []int{cite}); err == nil {
		t.Errorf("a missing origin: no error, failures %q", bad)
	}
	// The check reads CHANGELOG.md's citations at the origin whatever the
	// base, and every other file's at the base.
	if at, shifted := numberedAt("CHANGELOG.md", base); at != changelogCitationOrigin || !shifted {
		t.Errorf("CHANGELOG.md's citations are read at %q (shifted %v), want the fixed origin %s", at, shifted, changelogCitationOrigin)
	}
	if at, shifted := numberedAt("plan.md", base); at != base || shifted {
		t.Errorf("plan.md's citations are read at %q (shifted %v), want the base", at, shifted)
	}
}
