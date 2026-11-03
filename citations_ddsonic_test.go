package main

// ddsonic: docs/ddsonic/POST-1.0-CATALOG.md cites other files by line
// ("plan.md:604", "CHANGELOG.md:44-48"). A change must not move a cited
// line: it may rewrite one in place, but not insert or delete lines above
// it. This test compares the working tree with a base: DDSONIC_CITATION_BASE
// when set (make validate sets it), else the merge base with origin/main.

import (
	"os"
	"os/exec"
	"regexp"
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

func TestCatalogCitationsDoNotMove(t *testing.T) {
	base := os.Getenv("DDSONIC_CITATION_BASE")
	if base == "" {
		out, err := gitOut(t, "merge-base", "origin/main", "HEAD")
		if err != nil {
			t.Skip("no base to compare with: set DDSONIC_CITATION_BASE, or fetch origin/main (make validate does)")
		}
		base = strings.TrimSpace(out)
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
		diff, err := gitOut(t, "diff", "-U0", "--no-ext-diff", base, "--", file)
		if err != nil {
			t.Fatalf("git diff %s: %v", file, err)
		}
		var hunks []shift
		for _, l := range strings.Split(diff, "\n") {
			if m := hunk.FindStringSubmatch(l); m != nil {
				hunks = append(hunks, shift{atoiOr(m[1], 0), atoiOr(m[2], 1), atoiOr(m[4], 1)})
			}
		}
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
