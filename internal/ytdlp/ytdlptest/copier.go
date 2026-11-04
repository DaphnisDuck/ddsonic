package ytdlptest

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// Modes of a Copier.
const (
	// Finish prints the scripted output and exits, removing its copy.
	Finish = "finish"
	// Hang holds its copy until it is told to stop.
	Hang = "hang"
	// Stubborn holds its copy and ignores every request to stop.
	Stubborn = "stubborn"
	// Unremovable finishes, leaving in its temporary directory a directory
	// that cannot be read, and so cannot be emptied.
	Unremovable = "unremovable"
	// HangUnremovable holds its copy until it is told to stop, and then
	// leaves such a directory.
	HangUnremovable = "hang-unremovable"
	// Fail removes its copy and fails as yt-dlp does for a video it cannot
	// play: FailMessage on standard error, no output, exit status 1.
	Fail = "fail"
)

// FailMessage is what a Fail run says.
const FailMessage = "ERROR: [youtube] synthetic: Video unavailable"

// Copier is a fake yt-dlp that treats temporary storage as yt-dlp treats a
// browser's cookie database: it copies it (here an empty stand-in, never a
// real cookie) into a yt_dlp* directory under $TMPDIR while it works, and
// removes the copy when it finishes or is interrupted (SIGINT). Killed or
// terminated (SIGKILL, SIGTERM), it leaves the copy behind, as yt-dlp
// 2026.08.19 does.
//
// The fixture owns the processes its runs make. A run that holds its copy
// blocks on a child that waits for the test's release mark, polling for it
// (nothing it does blocks on a pipe: no blocking open, no wait for an end
// of file), so that child cannot outlive the test however its run ends
// (finished, interrupted, terminated, killed or never stopped): it ends at
// the mark, when the test's process is gone, or after childPolls looks at the
// latest. Every run and every child records itself in the registry (its pid
// and, where it can, its start time) as its first action, before it touches
// anything else of the fixture, so the test's teardown can wait until each
// one has exited (hold_unix.go).
type Copier struct {
	state, temp string
	alive       *os.File
	teardown    sync.Once
}

// childPoll and childPolls are how often a child looks for the release mark
// (seconds) and how many times at most: 120 s, a bound for a test whose
// process outlives its teardown, far beyond any test's own use of a child.
const (
	childPoll  = "0.05"
	childPolls = 2400
)

// teardownBound is the one deadline of the teardown, from its start to its
// acceptance: the runs and children end at once when released, so it is
// only ever reached by something that should not be there. An observation
// made after it is not accepted.
var teardownBound = 10 * time.Second

const copierScript = `#!/bin/sh
state=%STATE%
owner=%OWNER%
ident() {
	ID=-
	s=
	{ read -r s </proc/self/stat; } 2>/dev/null || return 0
	set -- $s
	[ $# -ge 22 ] && ID=${22}
}
ident
printf 'run %s %s\n' "$$" "$ID" >>"$state/registry"
copy="${TMPDIR:-/tmp}/yt_dlp$$"
mkdir -p "$copy" && : >"$copy/temporary.sqlite"
printf '%s\n' "$$" >>"$state/pids"
printf '%s\n' "${TMPDIR:-}" >>"$state/tmpdir"
printf '%s\n' "$*" >>"$state/args"
mode=$(cat "$state/mode")
child=
lock() {
	mkdir -p "${TMPDIR:-/tmp}/locked" && : >"${TMPDIR:-/tmp}/locked/left" && chmod 000 "${TMPDIR:-/tmp}/locked"
}
finish() {
	[ "$mode" = hang-unremovable ] && lock
	rm -rf "$copy"
	exit "$1"
}
if [ "$mode" = stubborn ]; then
	trap '' INT TERM
else
	trap ': >"$state/interrupted"; finish 130' INT
fi
: >"$state/copying"
if [ "$mode" = fail ]; then
	printf '%s\n' 'ERROR: [youtube] synthetic: Video unavailable' >&2
	rm -rf "$copy"
	exit 1
fi
if [ "$mode" = unremovable ]; then
	lock
	cat "$state/stdout"
	finish 0
fi
if [ "$mode" != finish ]; then
	sh -c 'o=$1 s=; { read -r s </proc/self/stat; } 2>/dev/null; set -- $s; printf "child %s %s\n" "$$" "${22:--}" >>"$0/registry"; exec 8<>"$0/alive"; n=0; while [ ! -e "$0/released" ] && kill -0 "$o" 2>/dev/null && [ "$n" -lt %POLLS% ]; do sleep %POLL%; n=$((n+1)); done' "$state" "$owner" >/dev/null 2>&1 &
	child=$!
	wait "$child"
fi
cat "$state/stdout"
finish 0
`

// copierFor is the fake for a copier whose state is state, owned by this
// process.
func copierFor(state string) string {
	return strings.NewReplacer("%STATE%", "'"+state+"'", "%OWNER%", strconv.Itoa(os.Getpid()),
		"%POLLS%", strconv.Itoa(childPolls), "%POLL%", childPoll).Replace(copierScript)
}

// unrelated is what else the temporary directory holds: things a cleanup by
// name would take for yt-dlp's or ddsonic's own.
var unrelated = []string{likeOurs, "unrelated.txt", "yt_dlp-not-ours"}

// likeOurs is named like a directory ddsonic makes for a run, and is not one.
var likeOurs = appdir.Name + "-yt-dlp-not-ours"

// InstallCopier puts the fake first on PATH for the rest of the test and
// gives the test a temporary directory of its own, holding a few things that
// are nobody's to remove. Each run of the fake behaves as mode says and, when
// it finishes, prints stdout.
func InstallCopier(t *testing.T, mode, stdout string) *Copier {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake yt-dlp is a shell script")
	}
	root := t.TempDir()
	c := &Copier{state: filepath.Join(root, "state"), temp: filepath.Join(root, "tmp")}
	for path, content := range map[string]string{
		filepath.Join(c.state, "stdout"):                 stdout,
		filepath.Join(c.state, "mode"):                   mode,
		filepath.Join(root, "bin", "yt-dlp"):             copierFor(c.state),
		filepath.Join(c.temp, "unrelated.txt"):           "keep",
		filepath.Join(c.temp, "yt_dlp-not-ours", "keep"): "keep",
		filepath.Join(c.temp, likeOurs, "keep"):          "keep",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", c.temp)
	// Whatever a test did to the directories' modes, the test's own
	// directory has to be removable again.
	t.Cleanup(func() {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, _ error) error {
			if d != nil && d.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	if err := c.own(); err != nil {
		t.Fatal(err)
	}
	// Registered after the cleanup above, so it runs before it (cleanups run
	// last-registered first): after the test body and every cleanup the
	// caller registers later, before the directories are made removable and
	// removed.
	t.Cleanup(func() { c.release(t.Errorf) })
	return c
}

// release ends the fixture's part in the runs, within one deadline
// (teardownBound from its start). It lets every child a run blocks on
// finish (a run still holding its copy then finishes too), waits until no
// child holds the alive pipe, and then until every run and child in the
// registry has exited, reading the registry again until it no longer grows.
// It reports what it could not establish, with what is still running
// (stuck), and an acceptance later than the deadline; it signals nothing and
// reaps nothing, so a process that is not the fixture's is never touched,
// and a run's reaping stays with whoever started it (a child's with its
// run, or with the system once the run has gone). It runs once. It is
// registered in InstallCopier after the cleanup that restores the
// directories' modes, so it runs before that and before the directories are
// removed, and after every cleanup the caller registers after InstallCopier.
func (c *Copier) release(report func(format string, args ...any)) {
	c.teardown.Do(func() {
		deadline := time.Now().Add(teardownBound)
		if err := c.unhold(); err != nil {
			report("ytdlptest: the release: %v", err)
		}
		if err := c.join(deadline); err != nil {
			report("ytdlptest: %v", err)
		}
		if err := c.awaitRegistered(deadline); err != nil {
			report("ytdlptest: %v", err)
		}
		if late := time.Since(deadline); late > 0 {
			report("ytdlptest: the teardown completed %v after its deadline (%v from its start); what it observed is not accepted", late, teardownBound)
		}
	})
}

// Unlock makes what an Unremovable run left removable again.
func (c *Copier) Unlock(t *testing.T) {
	t.Helper()
	err := filepath.WalkDir(c.temp, func(path string, d os.DirEntry, err error) error {
		if d != nil && d.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// DenyTemp makes the temporary directory one nothing can be created in.
func (c *Copier) DenyTemp(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root creates files wherever it likes")
	}
	if err := os.Chmod(c.temp, 0o500); err != nil {
		t.Fatal(err)
	}
}

// WaitCopying waits until a run of the fake holds its copy.
func (c *Copier) WaitCopying(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(c.state, "copying")); err == nil {
			return
		}
	}
	t.Fatal("yt-dlp did not start copying within 10 s")
}

// WaitRuns waits until n runs of the fake have started and hold their copies.
func (c *Copier) WaitRuns(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if len(c.lines(t, "args")) >= n {
			return
		}
	}
	t.Fatalf("yt-dlp was started %d times within 10 s, want %d", len(c.lines(t, "args")), n)
}

// Live returns how many runs of the fake are still running, or have ended
// and not been reaped: the processes the fake reported the pids of that the
// system still knows (alive, in alive_unix.go).
func (c *Copier) Live(t *testing.T) int {
	t.Helper()
	live := 0
	for _, line := range c.lines(t, "pids") {
		pid, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("pid %q: %v", line, err)
		}
		if alive(pid) {
			live++
		}
	}
	return live
}

// WaitLive waits until exactly n runs are still running, and reports how
// long that took.
func (c *Copier) WaitLive(t *testing.T, n int, within time.Duration) time.Duration {
	t.Helper()
	start := time.Now()
	for time.Since(start) < within {
		if c.Live(t) == n {
			return time.Since(start)
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%d yt-dlp run(s) still running after %v, want %d", c.Live(t), within, n)
	return 0
}

// WaitNoCopy waits until the runs have left nothing in the temporary
// directory: a cleanup that runs on a goroutine of its own takes a moment.
func (c *Copier) WaitNoCopy(t *testing.T, within time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if len(c.CopiesLeft(t)) == 0 {
			return
		}
	}
	t.Fatalf("still in the temporary directory after %v: %q", within, c.CopiesLeft(t))
}

// Runs returns the argument list of each run so far, space-joined.
func (c *Copier) Runs(t *testing.T) []string {
	t.Helper()
	return c.lines(t, "args")
}

// RequireNoCopy fails the test unless yt-dlp ran and nothing of the runs is
// left in the temporary directory: no copy, and no directory made for one.
// What was there before must still be there.
func (c *Copier) RequireNoCopy(t *testing.T) {
	t.Helper()
	if len(c.Runs(t)) == 0 {
		t.Fatal("yt-dlp was not run")
	}
	entries, err := os.ReadDir(c.temp)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, unrelated) {
		t.Errorf("the temporary directory holds %q after the run, want only what was there before, %q", names, unrelated)
	}
	for _, keep := range []string{"unrelated.txt", "yt_dlp-not-ours/keep", likeOurs + "/keep"} {
		if _, err := os.Stat(filepath.Join(c.temp, keep)); err != nil {
			t.Errorf("a file that was not the run's is gone: %v", err)
		}
	}
}

// Interrupted reports whether a run was interrupted, and so removed its copy
// itself.
func (c *Copier) Interrupted(t *testing.T) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(c.state, "interrupted"))
	return err == nil
}

// CopiesLeft returns what the runs so far left in the temporary directory.
func (c *Copier) CopiesLeft(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(c.temp)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		if !slices.Contains(unrelated, e.Name()) {
			left = append(left, e.Name())
		}
	}
	return left
}

// RequireOwnTemp fails the test unless every run was given a temporary
// directory of its own, inside the test's and not the test's itself.
func (c *Copier) RequireOwnTemp(t *testing.T) {
	t.Helper()
	dirs := c.lines(t, "tmpdir")
	if len(dirs) == 0 {
		t.Fatal("yt-dlp was not run")
	}
	for _, dir := range dirs {
		if filepath.Dir(dir) != c.temp || slices.Contains(unrelated, filepath.Base(dir)) {
			t.Errorf("yt-dlp ran with TMPDIR %q, want a directory of its own inside %q", dir, c.temp)
		}
	}
}

func (c *Copier) lines(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.state, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	logged := strings.TrimSuffix(string(b), "\n")
	if logged == "" {
		return nil
	}
	return strings.Split(logged, "\n")
}
