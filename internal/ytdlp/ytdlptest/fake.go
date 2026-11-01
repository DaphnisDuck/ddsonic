// Package ytdlptest puts a fake yt-dlp on PATH that reads ambient
// configuration the way yt-dlp does, so a test can show that a call site is
// not steered by it.
package ytdlptest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// AmbientOption is an option only the ambient configuration files carry.
const AmbientOption = "--proxy=http://ambient.invalid"

// Fake is a fake yt-dlp and the ambient configuration around it: a user
// configuration and a yt-dlp.conf in the working directory, each asking for
// --mark-watched and AmbientOption.
//
// Like yt-dlp, the fake reads both files unless --ignore-config is among its
// arguments, places their options before the command line's, and lets the
// last of --mark-watched and --no-mark-watched win.
type Fake struct{ state string }

const script = `#!/bin/sh
state=%STATE%
ignore=0
for a in "$@"; do
	[ "$a" = "--" ] && break
	[ "$a" = "--ignore-config" ] && ignore=1
done
ambient=
if [ "$ignore" = 0 ]; then
	for f in "$XDG_CONFIG_HOME/yt-dlp/config" ./yt-dlp.conf; do
		[ -f "$f" ] && ambient="$ambient $(cat "$f")"
	done
fi
mark=0
for a in $ambient "$@"; do
	case "$a" in
	--) break ;;
	--mark-watched) mark=1 ;;
	--no-mark-watched) mark=0 ;;
	esac
done
printf '%s\n' "$*" >>"$state/args"
[ -n "$ambient" ] && printf '%s\n' "$ambient" >>"$state/ambient"
[ "$mark" = 1 ] && : >"$state/marked"
cat "$state/stdout"
`

// Install puts the fake first on PATH for the rest of the test, points the
// user configuration at one that marks watched, and moves the test into a
// directory holding a yt-dlp.conf that does the same. Each run of the fake
// prints stdout.
func Install(t *testing.T, stdout string) *Fake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake yt-dlp is a shell script")
	}
	root := t.TempDir()
	f := &Fake{state: filepath.Join(root, "state")}
	config := "--mark-watched\n" + AmbientOption + "\n"
	for path, content := range map[string]string{
		filepath.Join(f.state, "stdout"):               stdout,
		filepath.Join(root, "bin", "yt-dlp"):           strings.ReplaceAll(script, "%STATE%", "'"+f.state+"'"),
		filepath.Join(root, "xdg", "yt-dlp", "config"): config,
		filepath.Join(root, "cwd", "yt-dlp.conf"):      config,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	t.Chdir(filepath.Join(root, "cwd"))
	return f
}

// Runs returns the argument list of each run so far, space-joined.
func (f *Fake) Runs(t *testing.T) []string {
	t.Helper()
	logged := strings.TrimSuffix(f.read(t, "args"), "\n")
	if logged == "" {
		return nil
	}
	return strings.Split(logged, "\n")
}

// Marked reports whether any run marked what it read as watched.
func (f *Fake) Marked(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(f.state, "marked"))
	return err == nil
}

// Ambient returns the ambient options the runs so far took up, "" when none
// did.
func (f *Fake) Ambient(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(f.read(t, "ambient"))
}

// RequireIsolated fails the test unless yt-dlp ran, took no option from the
// ambient configuration, and marked nothing as watched. It returns the runs.
func (f *Fake) RequireIsolated(t *testing.T) []string {
	t.Helper()
	runs := f.Runs(t)
	if len(runs) == 0 {
		t.Fatal("yt-dlp was not run")
	}
	if got := f.Ambient(t); got != "" {
		t.Errorf("yt-dlp took options from the ambient configuration: %q (runs: %q)", got, runs)
	}
	if f.Marked(t) {
		t.Errorf("yt-dlp marked what it read as watched (runs: %q)", runs)
	}
	return runs
}

func (f *Fake) read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.state, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}
