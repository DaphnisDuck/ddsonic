package main

// ddsonic: tests of the local consumers of the classifier (docs/ddsonic/
// acceptance.md, "Proportional validation"): tools/change-class.sh, which
// make validate runs, and tools/validate.sh, make validate's profiles.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeExec(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runSh runs a shell script in dir with env added; it returns stdout,
// stderr and the exit status.
func runSh(t *testing.T, dir string, env []string, shell string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(shell, args...)
	cmd.Dir = dir
	cmd.Env = append(gitEnv(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

// change-class.sh passes a class on only when the classifier's first line is
// exactly one of the four words and it exited 0; everything else is full.
// The classifier runs with a clean environment.
func TestChangeClassFailsClosed(t *testing.T) {
	consumer := readRepo(t, "tools/change-class.sh")
	tests := []struct {
		name, out    string
		exit         int
		class, meta  string
		noClassifier bool
	}{
		{"docs", "docs\n# base=b head=h\ndocs\tM\tdocs/a.md\trule\n", 0, "docs", "# base=b head=h", false},
		{"test", "test\n# m\n", 0, "test", "# m", false},
		{"code", "code\n# m\n", 0, "code", "# m", false},
		{"full", "full\n# fail-closed\n", 0, "full", "# fail-closed", false},
		{"no output", "", 0, "full", "# fail-closed: the classifier gave no class (exit 0)", false},
		{"two words", "docs code\n# m\n", 0, "full", "", false},
		{"a trailing space", "docs \n# m\n", 0, "full", "", false},
		{"a carriage return", "docs\r\n# m\n", 0, "full", "", false},
		{"another case", "Docs\n# m\n", 0, "full", "", false},
		{"a blank first line", "\ndocs\n", 0, "full", "", false},
		{"another word", "none\n", 0, "full", "", false},
		{"a failure after docs", "docs\n# m\n", 1, "full", "# fail-closed: the classifier gave no class (exit 1)", false},
		{"exit 2", "docs\n", 2, "full", "", false},
		{"no description line", "code\nsomething\n", 0, "code", "# no description line", false},
		{"no classifier", "", 0, "full", "# fail-closed: no tools/classify-change.sh", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeExec(t, filepath.Join(dir, "tools/change-class.sh"), consumer)
			if !tt.noClassifier {
				fake := "printf '%s' '" + strings.ReplaceAll(tt.out, "'", `'\''`) + "'\nexit " + string(rune('0'+tt.exit)) + "\n"
				writeExec(t, filepath.Join(dir, "tools/classify-change.sh"), fake)
			}
			stdout, _, code := runSh(t, dir, nil, "/bin/sh", "tools/change-class.sh", "--base", "x")
			if code != 0 {
				t.Fatalf("exit %d, want 0", code)
			}
			lines := strings.Split(stdout, "\n")
			if lines[0] != tt.class {
				t.Fatalf("class %q, want %q\n%s", lines[0], tt.class, stdout)
			}
			if len(lines) < 2 || !strings.HasPrefix(lines[1], "# ") || (tt.meta != "" && lines[1] != tt.meta) {
				t.Fatalf("second line %q, want %q\n%s", lines[1], tt.meta, stdout)
			}
		})
	}
	t.Run("a clean environment and the arguments", func(t *testing.T) {
		dir := t.TempDir()
		writeExec(t, filepath.Join(dir, "tools/change-class.sh"), consumer)
		writeExec(t, filepath.Join(dir, "tools/classify-change.sh"),
			`if [ -z "${LEAK+x}" ] && [ "$HOME" = /nonexistent ] && [ "$LC_ALL" = C ] && [ "$*" = "--base x" ]; then echo docs; else echo code; fi; echo "# m"`+"\n")
		stdout, _, _ := runSh(t, dir, []string{"LEAK=1", "HOME=/tmp", "LC_ALL=en_US.UTF-8"}, "/bin/sh", "tools/change-class.sh", "--base", "x")
		if first, _, _ := strings.Cut(stdout, "\n"); first != "docs" {
			t.Fatalf("the classifier saw the caller's environment or other arguments: %q", stdout)
		}
	})
}

// With the real classifier, each class comes through change-class.sh, and a
// change that edits the rules and the classifier to say docs is full: the
// base's copies decide.
func TestChangeClassClasses(t *testing.T) {
	consumer, err := filepath.Abs("tools/change-class.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, path, data, want string
	}{
		{"docs", "docs/a.md", "b\n", "docs"},
		{"test", "library/x_test.go", "package library\n\n// t\n", "test"},
		{"code", "library/x.go", "package library\n\n// c\n", "code"},
		{"full", "Makefile", "all:\n\ttrue\n", "full"},
		{"the rules and classifier edited to docs, with code", "tools/validation-classes.txt", "docs *\n", "full"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f, base := newFixture(t)
			f.write(tt.path, tt.data)
			if strings.HasPrefix(tt.path, "tools/") {
				f.write("tools/classify-change.sh", "#!/bin/bash -p\necho docs\n")
				f.write("library/x.go", "package library\n\nfunc X() {}\n")
			}
			head := f.commit("change")
			stdout, _, code := runSh(t, f.dir, nil, "/bin/sh", consumer, "--base", base, "--head", head)
			if first, _, _ := strings.Cut(stdout, "\n"); code != 0 || first != tt.want {
				t.Fatalf("%q (exit %d), want %s\n%s", first, code, tt.want, stdout)
			}
		})
	}
}

// fakeMake and fakeClassifier stand in for make and change-class.sh in a
// copy of tools/validate.sh: the classifier answers from out1, then out2.
func validateFixture(t *testing.T, out1, out2 string, makeExit int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	writeExec(t, filepath.Join(dir, "tools/validate.sh"), readRepo(t, "tools/validate.sh"))
	writeExec(t, filepath.Join(dir, "out1"), out1)
	writeExec(t, filepath.Join(dir, "out2"), out2)
	writeExec(t, filepath.Join(dir, "tools/change-class.sh"),
		`d=$(dirname "$0")/..; if [ -f "$d/called" ]; then cat "$d/out2"; else : >"$d/called"; cat "$d/out1"; fi`+"\n")
	writeExec(t, filepath.Join(dir, "make"), `echo "$*" >>"$(dirname "$0")/made"; exit `+string(rune('0'+makeExit))+"\n")
	return dir
}

const sha = "0123456789abcdef0123456789abcdef01234567"

// make validate runs the profile of the class, or a higher PROFILE; a lower
// or unknown PROFILE runs nothing; a class that rose meanwhile fails.
func TestValidateProfiles(t *testing.T) {
	meta := "# base=" + sha + " head=h+worktree rules=" + sha + ":tools/validation-classes.txt\n"
	tests := []struct {
		name, out1, out2, profile string
		makeExit                  int
		wantMade, wantErr         string
		wantExit                  int
	}{
		{"docs", "docs\n" + meta, "docs\n" + meta, "", 0, "validate-docs VALIDATE_BASE=" + sha, "", 0},
		{"test", "test\n" + meta, "test\n" + meta, "", 0, "validate-test", "", 0},
		{"code", "code\n" + meta, "code\n" + meta, "", 0, "ci-core", "", 0},
		{"full", "full\n# fail-closed\n", "full\n# fail-closed\n", "", 0, "ci", "", 0},
		{"an odd class is full", "weird\n" + meta, "docs\n" + meta, "", 0, "ci", "", 0},
		{"PROFILE raises docs to full", "docs\n" + meta, "docs\n" + meta, "full", 0, "ci", "", 0},
		{"PROFILE raises test to code", "test\n" + meta, "test\n" + meta, "code", 0, "ci-core", "", 0},
		{"PROFILE may not lower code to docs", "code\n" + meta, "code\n" + meta, "docs", 0, "", "below the change's class, code: nothing ran", 2},
		{"PROFILE may not lower full to code", "full\n# fail-closed\n", "full\n", "code", 0, "", "below the change's class, full: nothing ran", 2},
		{"an unknown PROFILE", "docs\n" + meta, "docs\n" + meta, "fast", 0, "", "PROFILE must be docs, test, code or full: nothing ran", 2},
		{"PROFILE in another case", "docs\n" + meta, "docs\n" + meta, "Docs", 0, "", "PROFILE must be", 2},
		{"the class rose meanwhile", "docs\n" + meta, "code\n" + meta, "", 0, "validate-docs VALIDATE_BASE=" + sha, "FAILED: the class rose from docs to code", 1},
		{"a rise within the profile passes", "docs\n" + meta, "test\n" + meta, "code", 0, "ci-core", "", 0},
		{"the profile fails", "code\n" + meta, "code\n" + meta, "", 1, "ci-core", "", 1},
		{"docs without a base", "docs\n# m\n", "docs\n# m\n", "", 0, "", "no base for the docs profile", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := validateFixture(t, tt.out1, tt.out2, tt.makeExit)
			stdout, stderr, code := runSh(t, dir, []string{"MAKE=" + filepath.Join(dir, "make"), "PROFILE=" + tt.profile}, "/bin/sh", "tools/validate.sh")
			made, _ := os.ReadFile(filepath.Join(dir, "made"))
			if code != tt.wantExit || strings.TrimSpace(string(made)) != tt.wantMade || !strings.Contains(stderr, tt.wantErr) {
				t.Fatalf("exit %d, made %q, stderr %q; want exit %d, made %q, %q\n%s", code, made, stderr, tt.wantExit, tt.wantMade, tt.wantErr, stdout)
			}
			passed := strings.Contains(stdout, "validate: PASSED class=")
			if passed != (tt.wantExit == 0) {
				t.Fatalf("PASSED line %v with exit %d\n%s", passed, code, stdout)
			}
		})
	}
}

// The Makefile's validate targets: make validate is tools/validate.sh, and
// the docs profile takes only a commit ID and checks citations strictly.
func TestValidateMakeTargets(t *testing.T) {
	out, err := exec.Command("make", "-n", "validate").Output()
	if err != nil || !strings.Contains(string(out), "sh tools/validate.sh") {
		t.Fatalf("make -n validate: %v\n%s", err, out)
	}
	out, err = exec.Command("make", "-n", "validate-docs", "VALIDATE_BASE="+sha).Output()
	if err != nil || !strings.Contains(string(out), "git diff --check "+sha) ||
		!strings.Contains(string(out), "DDSONIC_CITATION_BASE="+sha+" DDSONIC_CITATION_STRICT=1 go test -count=1 .") {
		t.Fatalf("make -n validate-docs: %v\n%s", err, out)
	}
	for _, bad := range []string{"", "HEAD", "main; true", sha[:39]} {
		cmd := exec.Command("make", "validate-docs", "VALIDATE_BASE="+bad)
		cmd.Env = append(os.Environ(), "DDSONIC_DEVTOOLS=/nonexistent")
		if out, err := cmd.CombinedOutput(); err == nil || strings.Contains(string(out), "go test") {
			t.Fatalf("VALIDATE_BASE=%q was accepted\n%s", bad, out)
		}
	}
}
