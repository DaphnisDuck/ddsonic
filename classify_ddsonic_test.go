package main

// ddsonic: tests of tools/classify-change.sh, the classifier of proportional
// validation (docs/ddsonic/acceptance.md). Each case builds a repository in
// a temporary directory: a base commit carrying the real rules file, then a
// change, and classifies it with the base's rules (--rules-from), as make
// validate does. The classifier must fail closed: what it cannot decide
// safely is full.

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
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

// The ways a caller can start the classifier. supported is the one make
// validate uses (the usage text and tools/change-class.sh);
// bashP and direct are also supported; plainBash is not, and must classify
// nothing.
const (
	supported = "env -i … /bin/bash -p"
	bashP     = "/bin/bash -p script"
	direct    = "the script itself (#!/bin/bash -p)"
	plainBash = "bash script (unsupported)"
)

// classifyAs starts the repository's classifier in dir the given way, with
// env added to the test's environment (supported drops it all), and
// returns its first line and the rest of its standard output.
func classifyAs(t *testing.T, mode, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	stdout, _ := classifyStreams(t, mode, dir, env, args...)
	first, rest, _ := strings.Cut(stdout, "\n")
	return first, rest
}

// classifyStreams is classifyAs's run, returning all of stdout and stderr.
func classifyStreams(t *testing.T, mode, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	script, err := filepath.Abs("tools/classify-change.sh")
	if err != nil {
		t.Fatal(err)
	}
	// A classifier that does not finish is a failure of its own, not a hang
	// of the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch mode {
	case supported:
		cmd = exec.CommandContext(ctx, "/usr/bin/env", append([]string{"-i", "PATH=/usr/local/bin:/usr/bin:/bin", "LC_ALL=C", "HOME=/nonexistent",
			"/bin/bash", "--noprofile", "--norc", "-p", script}, args...)...)
	case bashP:
		cmd = exec.CommandContext(ctx, "/bin/bash", append([]string{"-p", script}, args...)...)
	case direct:
		cmd = exec.CommandContext(ctx, script, args...)
	case plainBash:
		cmd = exec.CommandContext(ctx, "/bin/bash", append([]string{script}, args...)...)
	}
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		t.Fatalf("the classifier (%s) did not finish within 60 s", mode)
	}
	if err != nil {
		t.Fatalf("the classifier exited with %v; it must exit 0\n%s", err, out)
	}
	return string(out), stderr.String()
}

// classify starts it with bash -p, the caller's environment and env passed
// on, so that what the script does about an environment is tested.
func classify(t *testing.T, dir string, env []string, args ...string) (string, string) {
	t.Helper()
	return classifyAs(t, bashP, dir, env, args...)
}

// rule is how the classifier names a line of the real rules file in a
// reason: by its line number and class, never its text.
func rule(line string) string {
	data, err := os.ReadFile("tools/validation-classes.txt")
	if err != nil {
		panic(err)
	}
	for i, l := range strings.Split(string(data), "\n") {
		if l == line {
			return "rule " + strconv.Itoa(i+1) + " (" + strings.Fields(line)[0] + ")"
		}
	}
	panic("no rule " + line)
}

func TestClassifyChange(t *testing.T) {
	tests := []struct {
		name   string
		change func(f *fixture)
		want   string
		reason string // in the output, when set
	}{
		{"docs", func(f *fixture) { f.write("docs/a.md", "b\n") }, "docs", rule("docs docs/*.md")},
		{"test", func(f *fixture) { f.write("library/x_test.go", "package library\n// more\n") }, "test", rule("test *_test.go")},
		{"code", func(f *fixture) { f.write("library/x.go", "package library\n// more\n") }, "code", rule("code *.go")},
		{"full: the Makefile", func(f *fixture) { f.write("Makefile", "all:\n\ttrue\n") }, "full", rule("full Makefile")},
		{"mixed: docs and code is code", func(f *fixture) {
			f.write("docs/a.md", "b\n")
			f.write("library/x.go", "package library\n// more\n")
		}, "code", ""},
		{"mixed: test and full is full", func(f *fixture) {
			f.write("library/x_test.go", "package library\n// more\n")
			f.write("go.mod", "module x\n")
		}, "full", rule("full go.mod")},
		{"README is code, not docs", func(f *fixture) { f.write("README.md", "changed\n") }, "code", rule("code README.md")},
		{"acceptance material is full", func(f *fixture) { f.write("docs/ddsonic/acceptance.md", "x\n") }, "full", rule("full docs/ddsonic/acceptance.md")},
		{"policy prose is docs: CLAUDE.md", func(f *fixture) { f.write("CLAUDE.md", "x\n") }, "docs", rule("docs CLAUDE.md")},
		{"policy prose is docs: SECURITY.md", func(f *fixture) { f.write("SECURITY.md", "x\n") }, "docs", rule("docs SECURITY.md")},
		{"policy prose is docs: releasing.md", func(f *fixture) { f.write("docs/ddsonic/releasing.md", "x\n") }, "docs", rule("docs docs/ddsonic/releasing.md")},
		{"policy prose is docs: a design note", func(f *fixture) { f.write("docs/ddsonic/design/n.md", "x\n") }, "docs", rule("docs docs/ddsonic/design/*.md")},
		{"policy prose is docs: a nested design note", func(f *fixture) { f.write("docs/ddsonic/design/sub/n.md", "x\n") }, "docs", rule("docs docs/ddsonic/design/*.md")},
		{"not prose in the design notes is full: text", func(f *fixture) { f.write("docs/ddsonic/design/n.txt", "x\n") }, "full", rule("full docs/ddsonic/design/*")},
		{"not prose in the design notes is full: Go", func(f *fixture) { f.write("docs/ddsonic/design/x.go", "package design\n") }, "full", rule("full docs/ddsonic/design/*")},
		{"not prose in the design notes is full: a test", func(f *fixture) { f.write("docs/ddsonic/design/x_test.go", "package design\n") }, "full", rule("full docs/ddsonic/design/*")},
		{"not prose in the design notes is full: svg", func(f *fixture) { f.write("docs/ddsonic/design/d.svg", "<svg/>\n") }, "full", rule("full docs/ddsonic/design/*")},
		{"not prose in the design notes is full: nested Go", func(f *fixture) { f.write("docs/ddsonic/design/sub/y.go", "package sub\n") }, "full", rule("full docs/ddsonic/design/*")},
		{"AGENTS.md is full", func(f *fixture) { f.write("AGENTS.md", "x\n") }, "full", rule("full AGENTS.md")},
		{"mixed: policy prose and code is code", func(f *fixture) {
			f.write("CLAUDE.md", "x\n")
			f.write("library/x.go", "package library\n// more\n")
		}, "code", ""},
		{"mixed: policy prose and the Makefile is full", func(f *fixture) {
			f.write("CLAUDE.md", "x\n")
			f.write("Makefile", "all:\n\ttrue\n")
		}, "full", rule("full Makefile")},
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
		{"the classifier itself", func(f *fixture) { f.write("tools/classify-change.sh", "echo docs\n") }, "full", rule("full tools/*")},
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
// dev-mode rules file, which make validate never passes, would let the
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
		for _, mode := range []string{bashP, direct} {
			t.Run(name+", "+mode, func(t *testing.T) {
				if got, rest := classifyAs(t, mode, f.dir, env, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "no rule matches") {
					t.Errorf("DOCS/new.md: %q, want full (no rule matches)\n%s", got, rest)
				}
				if got, rest := classifyAs(t, mode, g.dir, env, "--base", gbase, "--head", ghead); got != "docs" {
					t.Errorf("a docs change: %q, want docs\n%s", got, rest)
				}
			})
		}
	}
}

// A BASH_ENV could define functions the script calls (here an iconv that
// accepts anything); in privileged mode bash reads no BASH_ENV.
func TestClassifyChangeIgnoresBashEnvFunctions(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/\xff.md", "x\n")
	head := f.commit("an invalid name")
	bashenv := filepath.Join(t.TempDir(), "bashenv")
	if err := os.WriteFile(bashenv, []byte("iconv() { cat >/dev/null; return 0; }\nexport -f iconv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{bashP, direct} {
		if got, rest := classifyAs(t, mode, f.dir, []string{"BASH_ENV=" + bashenv}, "--base", base, "--head", head); got != "full" || !strings.Contains(rest, "not valid UTF-8") {
			t.Fatalf("%s with a BASH_ENV iconv: %q, want full, not valid UTF-8\n%s", mode, got, rest)
		}
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

// Byte forms that glibc's iconv accepts but that are not UTF-8 (beyond
// U+10FFFF, the old five- and six-byte forms), and forms it refuses
// (overlong, surrogates), are full in a path, in both starts, and never
// reach the output; in a rule they make the rules malformed. Supplementary
// characters, up to U+10FFFF, classify normally.
func TestClassifyChangeRefusesNonCanonicalUTF8(t *testing.T) {
	bad := map[string]string{
		"F4 90 80 80 (above U+10FFFF)": "\xf4\x90\x80\x80", "F4 BF BF BF": "\xf4\xbf\xbf\xbf",
		"F5 80 80 80": "\xf5\x80\x80\x80", "F7 BF BF BF": "\xf7\xbf\xbf\xbf",
		"F8 five bytes": "\xf8\x88\x80\x80\x80", "FB five bytes": "\xfb\xbf\xbf\xbf\xbf",
		"FC six bytes": "\xfc\x84\x80\x80\x80\x80", "FD six bytes": "\xfd\xbf\xbf\xbf\xbf\xbf",
		"C0 80 overlong": "\xc0\x80", "E0 80 80 overlong": "\xe0\x80\x80", "F0 8F BF BF overlong": "\xf0\x8f\xbf\xbf",
		"ED A0 80 surrogate": "\xed\xa0\x80",
	}
	for name, b := range bad {
		for _, mode := range []string{supported, bashP} {
			t.Run(name+", "+mode, func(t *testing.T) {
				f, base := newFixture(t)
				f.write("docs/a"+b+"b.md", "x\n")
				head := f.commit("a non-canonical name")
				stdout, _ := classifyStreams(t, mode, f.dir, nil, "--base", base, "--head", head)
				first, rest, _ := strings.Cut(stdout, "\n")
				if first != "full" || !strings.Contains(rest, "fail-closed: a path that is not valid UTF-8 (or no iconv)") {
					t.Fatalf("%q, want full with the constant reason\n%s", first, strings.ToValidUTF8(stdout, "?"))
				}
				if strings.Contains(stdout, b) {
					t.Fatalf("the bytes reached the output: %q", stdout)
				}
			})
		}
		t.Run(name+", in a rule", func(t *testing.T) {
			f, _ := newFixture(t)
			f.write("tools/validation-classes.txt", "# rules\ndocs docs/"+b+"*.md\n")
			base := f.commit("a non-canonical rule")
			f.write("docs/a.md", "b\n")
			head := f.commit("change")
			stdout, _ := classifyStreams(t, bashP, f.dir, nil, "--base", base, "--head", head, "--rules-from", base)
			first, rest, _ := strings.Cut(stdout, "\n")
			if first != "full" || !strings.Contains(rest, "fail-closed: a malformed rule at line 2") {
				t.Fatalf("%q, want full, a malformed rule at line 2\n%s", first, strings.ToValidUTF8(stdout, "?"))
			}
			if strings.Contains(stdout, b) {
				t.Fatalf("the bytes reached the output: %q", stdout)
			}
		})
	}
	for name, cp := range map[string]string{"U+10000": "\U00010000", "U+1F600": "\U0001f600", "U+10FFFD": "\U0010fffd"} {
		t.Run(name, func(t *testing.T) {
			f, base := newFixture(t)
			f.write("docs/a"+cp+"b.md", "x\n")
			head := f.commit("a supplementary character")
			if got, rest := classifyAs(t, supported, f.dir, nil, "--base", base, "--head", head); got != "docs" {
				t.Fatalf("%s: %q, want docs\n%s", name, got, rest)
			}
		})
	}
}

// A path with a code point that can move a terminal's cursor, break a
// line, reorder or hide text, or is not a character, is full, in any
// locale, and its bytes never reach the output. Letters beyond ASCII, and
// punctuation next to the refused ranges, classify normally.
func TestClassifyChangeRefusesInvisibleCodePoints(t *testing.T) {
	odd := map[string]string{
		"U+0085 NEL": "\u0085", "U+009B CSI": "\u009b", "U+00AD soft hyphen": "\u00ad", "U+061C ALM": "\u061c",
		"U+200B ZWSP": "\u200b", "U+200F RLM": "\u200f", "U+2028 line separator": "\u2028", "U+202E RLO": "\u202e",
		"U+2066 LRI": "\u2066", "U+FEFF BOM": "\ufeff", "U+FFFE": "\ufffe", "U+1FFFE": "\U0001fffe", "U+E0041 tag": "\U000e0041",
	}
	for name, cp := range odd {
		for _, mode := range []string{supported, bashP} {
			t.Run(name+", "+mode, func(t *testing.T) {
				f, base := newFixture(t)
				f.write("docs/a"+cp+"b.md", "x\n")
				head := f.commit("an odd name")
				stdout, _ := classifyStreams(t, mode, f.dir, nil, "--base", base, "--head", head)
				first, rest, _ := strings.Cut(stdout, "\n")
				if first != "full" || !strings.Contains(rest, "fail-closed: a path with an invisible, control or non-character code point") {
					t.Fatalf("%q, want full with the constant reason\n%s", first, stdout)
				}
				if strings.Contains(stdout, cp) {
					t.Fatalf("the code point reached the output: %q", stdout)
				}
			})
		}
	}
	for _, name := range []string{"docs/é.md", "docs/straße.md", "docs/日本語.md", "docs/Привет.md", "docs/a–b….md"} {
		t.Run(name, func(t *testing.T) {
			f, base := newFixture(t)
			f.write(name, "x\n")
			head := f.commit("a valid name")
			if got, rest := classifyAs(t, supported, f.dir, nil, "--base", base, "--head", head); got != "docs" {
				t.Fatalf("%q: %q, want docs\n%s", name, got, rest)
			}
		})
	}
}

// bash -p imports an exported FUNCNAME or BASH_EXECUTION_STRING, which make
// a normal start look embedded: the script then prints no class (full, never
// a downgrade). The supported start, under env -i, is not affected.
func TestClassifyChangeExportedSpecialVariables(t *testing.T) {
	f, base := newFixture(t)
	f.write("docs/a.md", "b\n")
	head := f.commit("a docs change")
	script, err := filepath.Abs("tools/classify-change.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"FUNCNAME=x", "BASH_EXECUTION_STRING=x"} {
		// The refused return fails at a script's top level (status 2); what
		// matters is that no class is printed.
		for name, argv := range map[string][]string{bashP: {"/bin/bash", "-p", script}, direct: {script}} {
			cmd := exec.Command(argv[0], append(argv[1:], "--base", base, "--head", head)...)
			cmd.Dir, cmd.Env = f.dir, append(gitEnv(), v)
			out, _ := cmd.Output()
			if len(out) != 0 {
				t.Errorf("%s, %s: stdout %q, want nothing", v, name, out)
			}
		}
		if got, rest := classifyAs(t, supported, f.dir, []string{v}, "--base", base, "--head", head); got != "docs" {
			t.Errorf("%s, supported: %q, want docs\n%s", v, got, rest)
		}
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
// only when the repository's config holds nothing but the keys a plain
// clone has: anything else that could redirect, weaken or extend the fetch
// is refused before any network access.
func TestClassifyChangeTrustsOnlyTheProjectOrigin(t *testing.T) {
	const gh = "https://github.com/DaphnisDuck/ddsonic.git"
	for name, config := range map[string][][]string{
		"origin is a local path":       {{"remote.origin.url", "/tmp/elsewhere"}},
		"origin is another repository": {{"remote.origin.url", "https://github.com/someone/ddsonic.git"}},
		"no origin":                    {},
		"origin twice":                 {{"remote.origin.url", gh}, {"--add", "remote.origin.url", gh}},
		"a URL rewrite":                {{"remote.origin.url", gh}, {"url./tmp/elsewhere.insteadOf", "https://github.com/"}},
		"an include":                   {{"remote.origin.url", gh}, {"include.path", "/tmp/other.config"}},
		"an ssh command":               {{"remote.origin.url", gh}, {"core.sshCommand", "/tmp/evil"}},
		"http.sslVerify off":           {{"remote.origin.url", gh}, {"http.sslVerify", "false"}},
		"an extra HTTP header":         {{"remote.origin.url", gh}, {"http.extraHeader", "X: y"}},
		"an HTTP proxy":                {{"remote.origin.url", gh}, {"http.proxy", "http://127.0.0.1:9"}},
		"a git proxy":                  {{"remote.origin.url", gh}, {"core.gitProxy", "/tmp/evil"}},
		"a push URL":                   {{"remote.origin.url", gh}, {"remote.origin.pushurl", "/tmp/elsewhere"}},
		"an extension":                 {{"remote.origin.url", gh}, {"extensions.worktreeConfig", "true"}},
		"an fsmonitor":                 {{"remote.origin.url", gh}, {"core.fsmonitor", "/tmp/evil"}},
		"a credential helper":          {{"remote.origin.url", gh}, {"credential.helper", "/tmp/evil"}},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newFixture(t)
			for _, kv := range config {
				f.git(append([]string{"config", "--local"}, kv...)...)
			}
			got, rest := classifyAs(t, supported, f.dir, nil)
			refused := false
			for _, reason := range []string{"fail-closed: the repository config sets ", "fail-closed: remote.origin.url is not one of the project's", "fail-closed: remote.origin.url is set "} {
				refused = refused || strings.Contains(rest, reason)
			}
			if got != "full" || !refused {
				t.Fatalf("%q, want full with a refusal of the config or origin, before any fetch\n%s", got, rest)
			}
		})
	}
}

// A plain clone's config passes, and the base is what this run fetched:
// never a ref left from before (here two, pointing at the fixture's base,
// which would make the docs change classify as docs). Offline, the fetch
// fails: full. Online, the fetched main is not the fixture's history:
// full, naming the fetched commit, never the stale one. It reaches GitHub,
// so it runs only with DDSONIC_NETWORK_TESTS=1, and says SKIPPED otherwise.
func TestClassifyChangeUsesOnlyAFreshlyFetchedBase(t *testing.T) {
	if os.Getenv("DDSONIC_NETWORK_TESTS") != "1" {
		t.Skip("SKIPPED: fetches main from GitHub; set DDSONIC_NETWORK_TESTS=1 to run it")
	}
	f, base := newFixture(t)
	f.git("config", "--local", "remote.origin.url", "https://github.com/DaphnisDuck/ddsonic.git")
	f.git("config", "--local", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	f.git("config", "--local", "branch.main.remote", "origin")
	f.git("config", "--local", "branch.main.merge", "refs/heads/main")
	f.git("update-ref", "refs/ddsonic/classify-base", base)
	f.git("update-ref", "refs/ddsonic/classify-base-1-1", base)
	f.write("docs/a.md", "a docs change\n")
	got, rest := classifyAs(t, supported, f.dir, nil)
	if got != "full" || strings.Contains(rest, base) {
		t.Fatalf("%q, want full and nothing measured from the stale base %s\n%s", got, base, rest)
	}
	// Offline: the fetch fails. Online: the fetched main has no rules file yet
	// (it predates the classifier), or shares no history with the fixture.
	if !strings.Contains(rest, "cannot fetch main") && !strings.Contains(rest, "no merge base of ") && !strings.Contains(rest, "no tools/validation-classes.txt in ") {
		t.Fatalf("want a failed fetch, or a fail-closed result about the fetched main:\n%s", rest)
	}
	t.Logf("result: %s", strings.TrimSpace(rest))
}

// Every way to start it gives the right class or no class at all (full to
// a consumer), never one taken from an attacker's repository or forged by
// an exported function: functions named after the commands the script uses,
// a forged marker of the old clean-start scheme, and GIT_DIR and
// GIT_WORK_TREE pointing at a repository whose change is only docs.
func TestClassifyChangeResistsExportedFunctions(t *testing.T) {
	f, base := newFixture(t)
	f.write("library/x.go", "package library\n// a code change\n")
	attacker := filepath.Join(t.TempDir(), "attacker")
	a := &fixture{t: t, dir: filepath.Dir(attacker)}
	a.git("clone", "-q", "file://"+f.dir, attacker)
	a.dir = attacker
	a.write("docs/a.md", "only docs\n")
	env := []string{"DDSONIC_CLASSIFY_CLEAN=1", "GIT_DIR=" + filepath.Join(attacker, ".git"), "GIT_WORK_TREE=" + attacker}
	// Each stands in for a command and prints docs; echo stays the builtin
	// they print with (a function calling itself would only crash bash).
	for _, name := range []string{"compgen", "exec", "builtin", "command", "unset", "env", "printf", "set", "declare",
		"read", "git", "iconv", "[", "cd", "pwd", "which", "exit", "return", "cat", "shopt", "export", "mktemp", "rm", "tr", "wc"} {
		env = append(env, "BASH_FUNC_"+name+"%%=() { echo docs; }")
	}
	want := map[string]string{supported: "code", bashP: "code", direct: "code", plainBash: ""}
	for _, mode := range []string{supported, bashP, direct, plainBash} {
		t.Run(mode, func(t *testing.T) {
			got, rest := classifyAs(t, mode, f.dir, env, "--base", base)
			if got != want[mode] {
				t.Fatalf("first line %q, want %q\n%s", got, want[mode], rest)
			}
			if strings.Contains(got+rest, "docs\t") {
				t.Fatalf("the attacker's change or a function's output leaked:\n%s\n%s", got, rest)
			}
		})
	}
}

// Run inside another shell, however its $0 is set, it refuses: nothing on
// stdout, a message on stderr, and the caller's shell goes on with its
// directory and traps as they were.
func TestClassifyChangeRefusesToRunInsideAnotherShell(t *testing.T) {
	script, err := filepath.Abs("tools/classify-change.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	after := `echo "alive $? $(pwd) $(trap -p USR1)"`
	setup := `cd / && trap 'echo T' USR1; `
	caller := filepath.Join(dir, "caller.sh")
	if err := os.WriteFile(caller, []byte(setup+`source "$S" --paths x; `+after+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"source from -c":                {"/bin/bash", "-p", "-c", setup + `source "$S" --paths x; ` + after},
		"dot from -c":                   {"/bin/bash", "-p", "-c", setup + `. "$S" --paths x; ` + after},
		"source with $0 set to it":      {"/bin/bash", "-p", "-c", setup + `source "$S" --paths x; ` + after, script},
		"exec -a: argv[0] spoofed":      {"/bin/bash", "-c", `exec -a "$S" /bin/bash -p -c "$0"`, setup + `source "$S" --paths x; ` + after},
		"source from another script":    {"/bin/bash", "-p", caller},
		"source from an interactive -c": {"/bin/bash", "-p", "-i", "-c", setup + `source "$S" --paths x; ` + after},
		"source in a function":          {"/bin/bash", "-p", "-c", setup + `f() { source "$S" --paths x; }; f; ` + after},
		"source from stdin":             {"/bin/bash", "-p", "-s"},
	}
	for name, argv := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Env = append(gitEnv(), "S="+script)
			if name == "source from stdin" {
				cmd.Stdin = strings.NewReader(setup + `source "$S" --paths x; ` + after + "\n")
			}
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			want := "alive 1 / trap -- 'echo T' SIGUSR1"
			if err != nil || !strings.Contains(string(out), want) {
				t.Fatalf("the caller did not go on unchanged: %v\nstdout:\n%s\nstderr:\n%s", err, out, stderr.String())
			}
			for _, line := range strings.Split(string(out), "\n") {
				if line == "docs" || line == "test" || line == "code" || line == "full" || strings.Contains(line, "\t") {
					t.Errorf("a class or path line on stdout: %q", line)
				}
			}
			if !strings.Contains(stderr.String(), "do not source it") {
				t.Errorf("no refusal on stderr:\n%s", stderr.String())
			}
		})
	}
}

// Nothing from the repository's config reaches the output: an origin or a
// value with a secret in it is refused without showing it, and a key whose
// subsection holds one is shown as its section and variable only.
func TestClassifyChangeNeverPrintsConfigSecrets(t *testing.T) {
	const gh = "https://github.com/DaphnisDuck/ddsonic.git"
	for name, config := range map[string][][]string{
		"origin with user and password": {{"remote.origin.url", "https://user:SECRET@github.com/other/repo"}},
		"origin with a token query":     {{"remote.origin.url", "https://github.com/DaphnisDuck/ddsonic?token=SECRET"}},
		"origin with a fragment":        {{"remote.origin.url", "git@github.com:DaphnisDuck/ddsonic.git#SECRET"}},
		"a credential helper":           {{"remote.origin.url", gh}, {"credential.helper", "!echo SECRET"}},
		"an Authorization header":       {{"remote.origin.url", gh}, {"http.extraHeader", "Authorization: Bearer SECRET"}},
		"a URL rewrite with a password": {{"remote.origin.url", gh}, {"url.https://x:SECRET@github.com/.insteadOf", "https://github.com/"}},
		"a per-URL HTTP setting":        {{"remote.origin.url", gh}, {"http.https://SECRET@github.com/.proxy", "http://127.0.0.1:9"}},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := newFixture(t)
			for _, kv := range config {
				f.git("config", "--local", kv[0], kv[1])
			}
			stdout, stderr := classifyStreams(t, supported, f.dir, nil)
			if first, _, _ := strings.Cut(stdout, "\n"); first != "full" {
				t.Fatalf("class %q, want full\n%s", first, stdout)
			}
			if strings.Contains(stdout+stderr, "SECRET") {
				t.Fatalf("the output shows the secret:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
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
		{"an unknown base", nil, []string{"--base", "0123456789abcdef0123456789abcdef01234567", "--head", head}, "a ref that is not a commit"},
		{"a base that is an option", nil, []string{"--base", "--output=/tmp/x", "--head", head}, "refused a ref"},
		{"a base that is a range", nil, []string{"--base", base + ".." + head, "--head", head}, "refused a ref"},
		{"a base with shell characters", nil, []string{"--base", "$(id)", "--head", head}, "refused a ref"},
		{"rules from an option", nil, []string{"--base", base, "--head", head, "--rules-from", "-p"}, "refused a ref"},
		{"a missing value", nil, []string{"--base"}, "--base needs a value"},
		{"an unknown argument", nil, []string{"--everything-is-docs"}, "an unknown argument"},
		{"an empty diff", nil, []string{"--base", head, "--head", head}, "an empty diff"},
		{"rules that do not exist", nil, []string{"--base", base, "--head", head, "--rules", "/nonexistent"}, "cannot read the rules file"},
		{"no origin (the local default)", nil, []string{"--rules-from", base}, "remote.origin.url is set 0 times"},
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
		"an invisible character": "docs docs/\u202e*.md\n",
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
		"tools/classify-change.sh", "tools/change-class.sh", "tools/validate.sh", "tools/validation-classes.txt", "classify_ddsonic_test.go", "citations_ddsonic_test.go", "validate_ddsonic_test.go", ".github/workflows/ci.yml", "Makefile")
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
