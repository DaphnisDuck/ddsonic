package main

// ddsonic: the version a source build reports (#87): tools/version.sh, run
// by the Makefile, against throwaway repositories.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo is a throwaway repository with its own identity and no user or
// system git configuration.
type gitRepo struct {
	t   *testing.T
	dir string
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r := &gitRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	return r
}

// cleanEnv is the environment without the outer make's or git's variables.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "VERSION", name == "MAKEFLAGS", name == "MFLAGS", name == "MAKELEVEL", name == "MAKEOVERRIDES":
		case strings.HasPrefix(name, "GIT_"):
		default:
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Dir, cmd.Env = r.dir, cleanEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit makes a commit that changes file f, and returns its short hash.
func (r *gitRepo) commit(f string) string {
	r.t.Helper()
	path := filepath.Join(r.dir, f)
	old, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(old, 'x'), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", f)
	r.git("commit", "-q", "-m", f)
	return r.git("rev-parse", "--short", "HEAD")
}

// version runs tools/version.sh in the repository.
func (r *gitRepo) version() string {
	r.t.Helper()
	script, err := filepath.Abs(filepath.Join("tools", "version.sh"))
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command("sh", script)
	cmd.Dir, cmd.Env = r.dir, cleanEnv()
	out, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("tools/version.sh: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestSourceBuildVersion(t *testing.T) {
	tests := []struct {
		name  string
		build func(r *gitRepo) (want string)
	}{
		{"no tags: the commit, as git describe --always", func(r *gitRepo) string {
			return r.commit("a")
		}},
		{"no tags, modified tree", func(r *gitRepo) string {
			sha := r.commit("a")
			r.commit("b")
			r.git("reset", "-q", "--soft", "HEAD~1") // b staged, not committed
			return sha + "-dirty"
		}},
		{"only a pre-release tag: git describe, as before", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0-rc.1")
			sha := r.commit("a")
			return "v1.0.0-rc.1-1-g" + sha
		}},
		{"on the stable tag", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			return "v1.0.0"
		}},
		{"on an annotated stable tag", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "-a", "-m", "release", "v1.0.0")
			return "v1.0.0"
		}},
		{"on the stable tag, modified tree", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			if err := os.WriteFile(filepath.Join(r.dir, "a"), []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "v1.0.0-dirty"
		}},
		{"an untracked file is not a modification", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			if err := os.WriteFile(filepath.Join(r.dir, "new"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			return "v1.0.0"
		}},
		{"commits after the stable tag", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			r.commit("a")
			sha := r.commit("a")
			return "v1.0.0-2-g" + sha
		}},
		{"commits after the stable tag, modified tree", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			sha := r.commit("a")
			if err := os.WriteFile(filepath.Join(r.dir, "a"), []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
			return "v1.0.0-1-g" + sha + "-dirty"
		}},
		{"pre-release tags are never the base, before or after the stable tag", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0-rc.3")
			r.commit("a")
			r.git("tag", "v1.0.0")
			r.commit("a")
			r.git("tag", "v1.1.0-rc.1") // nearer, and higher
			sha := r.commit("a")
			return "v1.0.0-2-g" + sha
		}},
		{"the highest stable tag by number, not the nearest or by text", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v0.10.0")
			r.commit("a")
			r.git("tag", "v0.9.0") // nearer, lower, and above v0.10.0 as text
			sha := r.commit("a")
			return "v0.10.0-2-g" + sha
		}},
		{"a stable tag that is not an ancestor is not the base", func(r *gitRepo) string {
			r.commit("a")
			r.git("tag", "v1.0.0")
			r.git("switch", "-q", "-c", "side")
			r.commit("side")
			r.git("tag", "v2.0.0")
			r.git("switch", "-q", "main")
			sha := r.commit("a")
			return "v1.0.0-1-g" + sha
		}},
		{"history merged in from before the tag is not counted", func(r *gitRepo) string {
			// root - x1 - x2 (older history on a branch) and root - tag - m1;
			// main then merges the branch. The tag's descendants are m1 and
			// the merge: 2. A plain count of tag..HEAD would be 4.
			r.commit("root")
			r.git("switch", "-q", "-c", "old")
			r.commit("x")
			r.commit("x")
			r.git("switch", "-q", "main")
			r.commit("a")
			r.git("tag", "v1.0.0")
			r.commit("a")
			r.git("merge", "-q", "--no-edit", "old")
			if plain := r.git("rev-list", "--count", "v1.0.0..HEAD"); plain != "4" {
				t.Fatalf("plain count = %s, want 4: the case does not test the ancestry path", plain)
			}
			return "v1.0.0-2-g" + r.git("rev-parse", "--short", "HEAD")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGitRepo(t)
			want := tt.build(r)
			if got := r.version(); got != want {
				t.Errorf("version = %q, want %q", got, want)
			}
		})
	}
}

// The Makefile builds with the script's version, falls back to "dev" where
// it fails (outside a git work tree), and VERSION= still overrides it.
func TestMakeBuildVersion(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	makefile, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("tools", "version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// A copy of the Makefile and the script in dir, and what make -n build
	// says the version is there.
	ldflagsVersion := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), makefile, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "tools", "version.sh"), script, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("make", append([]string{"-n", "build"}, args...)...)
		cmd.Dir, cmd.Env = dir, append(cleanEnv(), "HOME="+t.TempDir())
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("make -n build: %v\n%s", err, out)
		}
		_, after, ok := strings.Cut(string(out), "-X main.version=")
		if !ok {
			t.Fatalf("make -n build sets no version:\n%s", out)
		}
		return strings.TrimRight(strings.Fields(after)[0], `"`)
	}

	t.Run("in a repository", func(t *testing.T) {
		r := newGitRepo(t)
		r.commit("a")
		r.git("tag", "v1.0.0")
		sha := r.commit("a")
		r.git("tag", "v1.0.1-rc.1")
		want := "v1.0.0-1-g" + sha // the copied build files are untracked: not a modification
		if got := ldflagsVersion(t, r.dir); got != want {
			t.Errorf("version = %q, want %q", got, want)
		}
	})
	t.Run("VERSION overrides", func(t *testing.T) {
		r := newGitRepo(t)
		r.commit("a")
		r.git("tag", "v1.0.0")
		if got := ldflagsVersion(t, r.dir, "VERSION=v9.9.9-custom"); got != "v9.9.9-custom" {
			t.Errorf("version = %q, want the override", got)
		}
	})
	t.Run("outside a git work tree", func(t *testing.T) {
		if got := ldflagsVersion(t, t.TempDir()); got != "dev" {
			t.Errorf("version = %q, want dev", got)
		}
	})
}
