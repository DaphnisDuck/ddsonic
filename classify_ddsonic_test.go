package main

// ddsonic: tests of tools/classify-change.sh, the classifier of proportional
// validation (docs/ddsonic/acceptance.md). Each case builds a repository in
// a temporary directory: a base commit carrying the real rules file, then a
// change, and classifies it with the base's rules (--rules-from), as CI and
// make validate do. The classifier must fail closed: what it cannot decide
// safely is full.

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// fixture is a throwaway repository with the real classifier rules.
type fixture struct {
	t   *testing.T
	dir string
}

// gitEnv runs git with no outside configuration, as the classifier does.
func gitEnv() []string {
	env := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@example.invalid", "GIT_AUTHOR_DATE=2026-11-03T00:00:00Z", "GIT_COMMITTER_DATE=2026-11-03T00:00:00Z"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return env
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir, cmd.Env = f.dir, gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(path, data string) {
	f.t.Helper()
	p := filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) commit(msg string) string {
	f.t.Helper()
	f.git("add", "-A")
	f.git("commit", "-q", "--allow-empty", "-m", msg)
	return f.git("rev-parse", "HEAD")
}

func readRepo(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// newFixture is a repository whose base commit has the real rules, the
// classifier, and a file of each class.
func newFixture(t *testing.T) (*fixture, string) {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir()}
	f.git("init", "-q")
	f.write("tools/validation-classes.txt", readRepo(t, "tools/validation-classes.txt"))
	f.write("tools/classify-change.sh", readRepo(t, "tools/classify-change.sh"))
	f.write("README.md", "readme\n")
	f.write("docs/a.md", "a\n")
	f.write("library/x.go", "package library\n")
	f.write("library/y.go", "package library\n")
	f.write("library/x_test.go", "package library\n")
	f.write("Makefile", "all:\n")
	return f, f.commit("base")
}

// classify runs the repository's classifier in dir and returns its class
// line and the rest.
func classify(t *testing.T, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	script, err := filepath.Abs("tools/classify-change.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the classifier exited with %v; it must exit 0 once it has printed a class\n%s", err, out)
	}
	first, rest, _ := strings.Cut(string(out), "\n")
	return first, rest
}

func TestClassifyChange(t *testing.T) {
	tests := []struct {
		name   string
		change func(f *fixture)
		want   string
		reason string // in the output, when set
	}{
		{"docs", func(f *fixture) { f.write("docs/a.md", "b\n") }, "docs", "rule docs docs/*.md"},
		{"test", func(f *fixture) { f.write("library/x_test.go", "package library\n// more\n") }, "test", "rule test *_test.go"},
		{"code", func(f *fixture) { f.write("library/x.go", "package library\n// more\n") }, "code", "rule code *.go"},
		{"full: the Makefile", func(f *fixture) { f.write("Makefile", "all:\n\ttrue\n") }, "full", "rule full Makefile"},
		{"mixed: docs and code is code", func(f *fixture) {
			f.write("docs/a.md", "b\n")
			f.write("library/x.go", "package library\n// more\n")
		}, "code", ""},
		{"mixed: test and full is full", func(f *fixture) {
			f.write("library/x_test.go", "package library\n// more\n")
			f.write("go.mod", "module x\n")
		}, "full", "rule full go.mod"},
		{"README is code, not docs", func(f *fixture) { f.write("README.md", "changed\n") }, "code", "rule code README.md"},
		{"policy docs are full", func(f *fixture) { f.write("docs/ddsonic/acceptance.md", "x\n") }, "full", "rule full docs/ddsonic/acceptance.md"},
		{"an unknown path is full", func(f *fixture) { f.write("new/thing.txt", "x\n") }, "full", "no rule matches"},
		{"downgrade: a Go file renamed to .md", func(f *fixture) {
			f.git("mv", "library/y.go", "docs/y.md")
		}, "full", "a rename across classes"},
		{"a rename within a class", func(f *fixture) { f.git("mv", "docs/a.md", "docs/b.md") }, "docs", ""},
		{"a deleted test", func(f *fixture) { f.git("rm", "-q", "library/x_test.go") }, "full", "a deleted test"},
		{"a renamed test", func(f *fixture) { f.git("mv", "library/x_test.go", "library/z_test.go") }, "full", "a renamed test"},
		{"a symlink", func(f *fixture) {
			if err := os.Symlink("../library/x.go", filepath.Join(f.dir, "docs/l.md")); err != nil {
				f.t.Fatal(err)
			}
		}, "full", "a symlink"},
		{"a submodule (gitlink)", func(f *fixture) {
			sub := &fixture{t: f.t, dir: filepath.Join(f.dir, "docs", "sub")}
			if err := os.MkdirAll(sub.dir, 0o755); err != nil {
				f.t.Fatal(err)
			}
			sub.git("init", "-q")
			sub.write("x.md", "x\n")
			sub.commit("sub")
		}, "full", "a submodule"},
		{"a mode change", func(f *fixture) {
			if err := os.Chmod(filepath.Join(f.dir, "docs/a.md"), 0o755); err != nil {
				f.t.Fatal(err)
			}
		}, "full", "mode change"},
		{"a binary file", func(f *fixture) { f.write("docs/img.md", "a\x00b\x00c") }, "full", "a binary file"},
		{"the classifier itself", func(f *fixture) { f.write("tools/classify-change.sh", "echo docs\n") }, "full", "rule full tools/*"},
		{"a path with spaces", func(f *fixture) { f.write("docs/my file.md", "x\n") }, "docs", ""},
		{"a path with unicode", func(f *fixture) { f.write("docs/ñandú.md", "x\n") }, "docs", ""},
		{"a path with a leading dash", func(f *fixture) { f.write("docs/-n.md", "x\n") }, "docs", ""},
		{"a path with a newline", func(f *fixture) { f.write("docs/a\nb.md", "x\n") }, "full", "fail-closed: a path with a control character"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, base := newFixture(t)
			tt.change(f)
			head := f.commit("change")
			got, rest := classify(t, f.dir, nil, "--base", base, "--head", head, "--rules-from", base)
			if got != tt.want {
				t.Fatalf("class = %q, want %q\n%s", got, tt.want, rest)
			}
			if tt.reason != "" && !strings.Contains(rest, tt.reason) {
				t.Errorf("the reasons do not say %q:\n%s", tt.reason, rest)
			}
		})
	}
}

// A change that loosens the rules is classified by the base's rules: by
// default, with --base alone, the rules come from that same commit, and the
// "# " line says so. Asking for the change's own rules is refused; only the
// dev-mode rules file, which make validate and CI never pass, would let the
// change's rules classify it, and the "# " line says that too.
func TestClassifyChangeUsesTheBaseRules(t *testing.T) {
	f, base := newFixture(t)
	f.write("tools/validation-classes.txt", "docs *\n")
	f.write("library/x.go", "package library\n// more\n")
	head := f.commit("loosen the rules")
	got, rest := classify(t, f.dir, nil, "--base", base, "--head", head)
	if got != "full" {
		t.Fatalf("by default: %q, want full by the base's rules\n%s", got, rest)
	}
	meta := "# base=" + base + " head=" + head + " rules=" + base + ":tools/validation-classes.txt"
	if line, _, _ := strings.Cut(rest, "\n"); line != meta {
		t.Errorf("the # line is %q, want %q", line, meta)
	}
	if got, rest := classify(t, f.dir, nil, "--base", base, "--head", head, "--rules-from", head); got != "full" || !strings.Contains(rest, "is not the base") {
		t.Fatalf("with --rules-from the change: %q, want full, fail-closed\n%s", got, rest)
	}
	got, rest = classify(t, f.dir, nil, "--rules", "tools/validation-classes.txt", "--base", base, "--head", head)
	if got != "docs" || !strings.Contains(rest, "(dev mode)") {
		t.Fatalf("dev mode with the change's rules file: %q; the test expects docs, reported as dev mode\n%s", got, rest)
	}
}

// The caller's shell state does not change the classification: inherited
// bash options (nocasematch would let DOCS/new.md match docs/*.md), a
// BASH_ENV that sets them, SHELLOPTS and an odd IFS.
func TestClassifyChangeIgnoresShellState(t *testing.T) {
	f, base := newFixture(t)
	f.write("DOCS/new.md", "x\n")
	head := f.commit("an unknown path that only nocasematch would match")
	g, gbase := newFixture(t)
	g.write("docs/a.md", "b\n")
	ghead := g.commit("a docs change")
	bashenv := filepath.Join(t.TempDir(), "bashenv")
	if err := os.WriteFile(bashenv, []byte("shopt -s nocasematch extglob globstar nullglob\nIFS=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][]string{
		"BASHOPTS=nocasematch":   {"BASHOPTS=nocasematch:extglob:globstar"},
		"SHELLOPTS":              {"SHELLOPTS=allexport:noglob"},
		"BASH_ENV":               {"BASH_ENV=" + bashenv},
		"IFS":                    {"IFS=/"},
		"all, and a fake marker": {"DDSONIC_CLASSIFY_CLEAN=1", "BASHOPTS=nocasematch", "BASH_ENV=" + bashenv, "IFS=/"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, rest := classify(t, f.dir, env, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "no rule matches") {
				t.Errorf("DOCS/new.md: %q, want full (no rule matches)\n%s", got, rest)
			}
			if got, rest := classify(t, g.dir, env, "--base", gbase, "--head", ghead); got != "docs" {
				t.Errorf("a docs change: %q, want docs\n%s", got, rest)
			}
		})
	}
}

// A BASH_ENV runs in the caller's bash before the script does, and could
// define functions the script calls (here an iconv that accepts anything).
// The clean restart under env -i leaves them behind.
func TestClassifyChangeIgnoresBashEnvFunctions(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/\xff.md", "x\n")
	head := f.commit("an invalid name")
	bashenv := filepath.Join(t.TempDir(), "bashenv")
	if err := os.WriteFile(bashenv, []byte("iconv() { cat >/dev/null; return 0; }\nexport -f iconv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, rest := classify(t, f.dir, []string{"BASH_ENV=" + bashenv}, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "not valid UTF-8") {
		t.Fatalf("with a BASH_ENV iconv: %q, want full, not valid UTF-8\n%s", got, rest)
	}
}

// A path that is not valid UTF-8 is full, even where its bytes would match a
// docs rule; valid UTF-8 beyond ASCII classifies normally.
func TestClassifyChangeRefusesInvalidUTF8(t *testing.T) {
	for _, name := range []string{"docs/\xff.md", "docs/\xc3\x28.md", "docs/a\xe2\x82.md"} {
		t.Run(strings.ToValidUTF8(name, "?"), func(t *testing.T) {
			f, base := newFixture(t)
			f.write(name, "x\n")
			head := f.commit("an invalid name")
			if got, rest := classify(t, f.dir, nil, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "not valid UTF-8") {
				t.Fatalf("%q: %q, want full, not valid UTF-8\n%s", name, got, rest)
			}
		})
	}
	f, base := newFixture(t)
	f.write("docs/日本語.md", "x\n")
	head := f.commit("a valid name")
	if got, rest := classify(t, f.dir, nil, "--base", base, "--head", head); got != "docs" {
		t.Fatalf("a valid UTF-8 name: %q, want docs\n%s", got, rest)
	}
}

// --paths uses the rules it is given: a rules file that says docs for a
// probe path changes the answer from the default, full.
func TestClassifyPathsUsesTheGivenRules(t *testing.T) {
	rules := filepath.Join(t.TempDir(), "rules")
	if err := os.WriteFile(rules, []byte("docs probe/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, def := classify(t, ".", nil, "--paths", "probe/x")
	meta, given := classify(t, ".", nil, "--rules", rules, "--paths", "probe/x")
	if !strings.Contains(def, "probe/x\tfull\t") || !strings.Contains(given, "probe/x\tdocs\t") || meta != "# rules=file:"+rules+" (dev mode)" {
		t.Fatalf("default:\n%s\ngiven rules:\n%s\nwant full, then docs from the given file", def, given)
	}
}

// The local default fetches main only from the project's GitHub URL, and
// only when the repository's config cannot redirect the fetch. Each of
// these is refused before any network access.
func TestClassifyChangeTrustsOnlyTheProjectOrigin(t *testing.T) {
	for name, config := range map[string][][]string{
		"origin is a local path":       {{"remote.origin.url", "/tmp/elsewhere"}},
		"origin is another repository": {{"remote.origin.url", "https://github.com/someone/ddsonic.git"}},
		"a URL rewrite":                {{"remote.origin.url", "https://github.com/DaphnisDuck/ddsonic.git"}, {"url./tmp/elsewhere.insteadOf", "https://github.com/"}},
		"an include":                   {{"remote.origin.url", "https://github.com/DaphnisDuck/ddsonic.git"}, {"include.path", "/tmp/other.config"}},
		"an ssh command":               {{"remote.origin.url", "git@github.com:DaphnisDuck/ddsonic.git"}, {"core.sshCommand", "/tmp/evil"}},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newFixture(t)
			for _, kv := range config {
				f.git("config", "--local", kv[0], kv[1])
			}
			got, rest := classify(t, f.dir, nil)
			if got != "full" || !strings.Contains(rest, "fail-closed:") || strings.Contains(rest, "cannot fetch") {
				t.Fatalf("%q, want full, refused before fetching\n%s", got, rest)
			}
		})
	}
}

// Every failure is full, with a fail-closed reason, and exit status 0.
func TestClassifyChangeFailsClosed(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/a.md", "b\n")
	head := f.commit("change")
	cases := []struct {
		name   string
		env    []string
		args   []string
		reason string
	}{
		{"an unknown base", nil, []string{"--base", "0123456789abcdef0123456789abcdef01234567", "--head", head}, "not a commit"},
		{"a base that is an option", nil, []string{"--base", "--output=/tmp/x", "--head", head}, "refused ref"},
		{"a base that is a range", nil, []string{"--base", base + ".." + head, "--head", head}, "refused ref"},
		{"a base with shell characters", nil, []string{"--base", "$(id)", "--head", head}, "refused ref"},
		{"rules from an option", nil, []string{"--base", base, "--head", head, "--rules-from", "-p"}, "refused ref"},
		{"a missing value", nil, []string{"--base"}, "--base needs a value"},
		{"an unknown argument", nil, []string{"--everything-is-docs"}, "unknown argument"},
		{"an empty diff", nil, []string{"--base", head, "--head", head}, "an empty diff"},
		{"rules that do not exist", nil, []string{"--base", base, "--head", head, "--rules", "/nonexistent"}, "cannot read the rules file"},
		{"no origin (the local default)", nil, []string{"--rules-from", base}, "no origin remote"},
		{"rules from a commit other than the base", nil, []string{"--base", base, "--head", head, "--rules-from", head}, "--rules-from " + head + " is not the base"},
		{"--rules-from with --rules", nil, []string{"--base", base, "--head", head, "--rules-from", base, "--rules", "x"}, "--rules-from and --rules together"},
		{"an option after --paths", nil, []string{"--paths", "--rules", "tools/validation-classes.txt", "docs/a.md"}, "an option after --paths"},
		{"--paths with a base", nil, []string{"--base", base, "--paths", "docs/a.md"}, "--paths takes no --base"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rest := classify(t, f.dir, c.env, c.args...)
			if got != "full" || !strings.Contains(rest, "fail-closed: "+c.reason) {
				t.Fatalf("class %q, reasons:\n%s\nwant full, fail-closed: %s", got, rest, c.reason)
			}
		})
	}
}

// Rules files that are malformed, name an unknown class, or are missing from
// the base make the classification full.
func TestClassifyChangeRejectsBadRules(t *testing.T) {
	for name, rules := range map[string]string{
		"an unknown class":       "free *\n",
		"no pattern":             "docs\n",
		"a pattern with a space": "docs a b\n",
		"no rules":               "# nothing\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newFixture(t)
			f.write("tools/validation-classes.txt", rules)
			base := f.commit("bad rules")
			f.write("docs/a.md", "b\n")
			head := f.commit("change")
			if got, rest := classify(t, f.dir, nil, "--base", base, "--head", head, "--rules-from", base); got != "full" || !strings.Contains(rest, "fail-closed:") {
				t.Fatalf("class %q, want full, fail-closed:\n%s", got, rest)
			}
		})
	}
	t.Run("a base without rules", func(t *testing.T) {
		f, _ := newFixture(t)
		f.git("rm", "-q", "tools/validation-classes.txt")
		base := f.commit("no rules")
		f.write("docs/a.md", "b\n")
		head := f.commit("change")
		if got, rest := classify(t, f.dir, nil, "--base", base, "--head", head, "--rules-from", base); got != "full" || !strings.Contains(rest, "fail-closed: no tools/validation-classes.txt") {
			t.Fatalf("class %q, want full:\n%s", got, rest)
		}
	})
}

// A shallow clone cannot be trusted for the merge base: full.
func TestClassifyChangeRefusesAShallowClone(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/a.md", "b\n")
	head := f.commit("change")
	clone := filepath.Join(t.TempDir(), "clone")
	c := &fixture{t: t, dir: filepath.Dir(clone)}
	c.git("clone", "-q", "--depth", "1", "file://"+f.dir, clone)
	if got, rest := classify(t, clone, nil, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "fail-closed: a shallow") {
		t.Fatalf("class %q, want full:\n%s", got, rest)
	}
}

// GIT_* variables from the caller do not redirect the classifier to another
// repository or configuration.
func TestClassifyChangeIgnoresGitEnvironment(t *testing.T) {
	f, base := newFixture(t)
	f.write("library/x.go", "package library\n// more\n")
	head := f.commit("change")
	other, _ := newFixture(t)
	env := []string{"GIT_DIR=" + filepath.Join(other.dir, ".git"), "GIT_WORK_TREE=" + other.dir, "GIT_CONFIG_PARAMETERS='diff.external=true'"}
	if got, rest := classify(t, f.dir, env, "--base", base, "--head", head, "--rules-from", base); got != "code" {
		t.Fatalf("class %q with GIT_DIR pointing elsewhere, want code:\n%s", got, rest)
	}
}

// Without --head, the working tree counts: uncommitted changes to tracked
// files, and untracked files, a symlink among them.
func TestClassifyChangeWorkingTree(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/a.md", "uncommitted\n")
	if got, rest := classify(t, f.dir, nil, "--base", base, "--rules-from", base); got != "docs" {
		t.Fatalf("an uncommitted docs change: %q, want docs\n%s", got, rest)
	}
	f.write("docs/new.md", "untracked\n")
	if got, rest := classify(t, f.dir, nil, "--base", base, "--rules-from", base); got != "docs" || !strings.Contains(rest, "docs/new.md") {
		t.Fatalf("an untracked docs file: %q, want docs and the file listed\n%s", got, rest)
	}
	if err := os.Symlink("../library/x.go", filepath.Join(f.dir, "docs/link.md")); err != nil {
		t.Fatal(err)
	}
	if got, rest := classify(t, f.dir, nil, "--base", base, "--rules-from", base); got != "full" {
		t.Fatalf("an untracked symlink: %q, want full\n%s", got, rest)
	}
}

// The classifier, its rules and these tests are full by their own rules.
func TestClassifierFilesAreFull(t *testing.T) {
	_, rest := classify(t, ".", nil, "--rules", "tools/validation-classes.txt", "--paths",
		"tools/classify-change.sh", "tools/validation-classes.txt", "classify_ddsonic_test.go", "citations_ddsonic_test.go", ".github/workflows/ci.yml", "Makefile")
	for _, line := range strings.Split(strings.TrimSpace(rest), "\n") {
		if f := strings.Split(line, "\t"); len(f) < 2 || f[1] != "full" {
			t.Errorf("not full: %q", line)
		}
	}
}

var (
	embedLine  = regexp.MustCompile(`^//go:embed\s+(.+)$`)
	readCall   = regexp.MustCompile(`(?:os\.(?:ReadFile|Open|OpenFile|Stat|ReadDir)|filepath\.Glob)\(\s*"([^"]+)"`)
	joinPrefix = regexp.MustCompile(`filepath\.Join\(\s*((?:"[^"]+"\s*,\s*)*"[^"]+")`)
	quoted     = regexp.MustCompile(`"([^"]+)"`)
)

// A file that a test reads or that Go embeds is at least code: a docs-only
// change must never be able to change what a test or a build sees. The scan
// finds embed patterns, and literal paths given to os.ReadFile, os.Open,
// os.Stat, os.ReadDir, filepath.Glob and filepath.Join in tests, and checks
// every file they name, tracked or untracked (not ignored).
func TestReadOrEmbeddedFilesAreCodeOrAbove(t *testing.T) {
	ls := exec.Command("git", "ls-files", "-z", "-c", "-o", "--exclude-standard")
	ls.Env = gitEnv()
	out, err := ls.Output()
	if err != nil {
		t.Skipf("no git work tree: %v", err)
	}
	tracked := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	isTracked := map[string]bool{}
	for _, p := range tracked {
		isTracked[p] = true
	}
	named := map[string]string{} // tracked file -> where it is referenced
	refer := func(pkgDir, rel, where string) {
		if filepath.IsAbs(rel) {
			return
		}
		pattern := filepath.ToSlash(filepath.Clean(filepath.Join(pkgDir, rel)))
		for _, p := range tracked {
			if ok, _ := filepath.Match(pattern, p); ok || strings.HasPrefix(p, pattern+"/") || p == pattern {
				named[p] = where
			}
		}
	}
	for _, p := range tracked {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		file, err := os.Open(p)
		if err != nil {
			continue
		}
		s := bufio.NewScanner(file)
		s.Buffer(make([]byte, 1<<20), 1<<20)
		for n := 1; s.Scan(); n++ {
			line := s.Text()
			where := p + ":" + strconv.Itoa(n)
			if m := embedLine.FindStringSubmatch(line); m != nil {
				for _, pat := range strings.Fields(m[1]) {
					refer(filepath.Dir(p), strings.Trim(pat, `"`), where)
				}
			}
			if !strings.HasSuffix(p, "_test.go") {
				continue
			}
			for _, m := range readCall.FindAllStringSubmatch(line, -1) {
				refer(filepath.Dir(p), m[1], where)
			}
			for _, m := range joinPrefix.FindAllStringSubmatch(line, -1) {
				var parts []string
				for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
					parts = append(parts, q[1])
				}
				refer(filepath.Dir(p), filepath.Join(parts...), where)
			}
		}
		file.Close()
	}
	if len(named) == 0 {
		t.Fatal("the scan found no file read by a test or embedded; it is not looking")
	}
	var paths []string
	for p := range named {
		paths = append(paths, p)
	}
	_, rest := classify(t, ".", nil, append([]string{"--rules", "tools/validation-classes.txt", "--paths"}, paths...)...)
	for _, line := range strings.Split(strings.TrimSpace(rest), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			t.Fatalf("odd classifier line %q", line)
		}
		if f[1] == "docs" || f[1] == "test" {
			t.Errorf("%s is %s, but %s reads or embeds it: it must be code or full", f[0], f[1], named[f[0]])
		}
	}
	t.Logf("%d files read by tests or embedded, all code or full", len(named))
}
