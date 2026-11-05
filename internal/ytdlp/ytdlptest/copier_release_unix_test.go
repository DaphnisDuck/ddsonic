//go:build unix

package ytdlptest

// R-02: the child a run blocks on is released by a mark it polls for, never
// by a pipe's end, so a teardown that meets a child still starting cannot
// leave it waiting; the teardown proves exits from ps where there is no
// /proc, and says what is still running when it gives up.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A teardown made at once after a run starts, while its child may be
// anywhere in its start (forked, registering, opening the alive pipe,
// polling), reports nothing, and the run ends: no step of the child can
// wait on the test.
func TestCopierTeardownRacingAStartingChild(t *testing.T) {
	for i := range 30 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var run *ownedRun
			t.Cleanup(func() { run.finish(t) })
			c := InstallCopier(t, Hang, "")
			run = startRun(t)
			time.Sleep(time.Duration(i%6) * time.Millisecond)
			var r recorder
			c.release(r.report)
			if len(r) != 0 {
				t.Errorf("the teardown reported %q", r)
			}
			if !run.result(runBound) {
				t.Errorf("the run had not ended %v after the teardown", runBound)
			}
		})
	}
}

// A child whose test's process is gone does not wait for a release mark
// that nobody will make: it ends, and its run with it.
func TestCopierChildEndsWithoutItsTest(t *testing.T) {
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	var run *ownedRun
	t.Cleanup(func() { run.finish(t) })
	c := InstallCopier(t, Hang, "")
	// The same fake, owned by a process that has exited.
	fake := strings.Replace(copierFor(c.state), "owner="+strconv.Itoa(os.Getpid()), "owner="+strconv.Itoa(gone.Process.Pid), 1)
	if fake == copierFor(c.state) {
		t.Fatal("the script names no owner")
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(c.state), "bin", "yt-dlp"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	run = startRun(t)
	if !run.result(runBound) {
		t.Fatalf("the run of a gone test had not ended %v later", runBound)
	}
	if run.err != nil {
		t.Errorf("the run ended with %v, want it to finish", run.err)
	}
}

// useListed makes the teardown prove exits from ps, as off Linux.
func useListed(t *testing.T) {
	t.Helper()
	if _, err := psList(); err != nil {
		t.Skipf("no usable ps here: %v", err)
	}
	defer func(p bool) { t.Cleanup(func() { procProof = p }) }(procProof)
	procProof = false
}

// From ps, the teardown accepts runs and children that have exited, and
// refuses, with what is still running, one that has not.
func TestCopierExitProofFromPS(t *testing.T) {
	t.Run("a run interrupted, its child released", func(t *testing.T) {
		useListed(t)
		var run *ownedRun
		t.Cleanup(func() { run.finish(t) })
		c := InstallCopier(t, Hang, "")
		run = startRun(t)
		c.WaitCopying(t)
		if err := run.cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		if !run.result(runBound) {
			t.Fatalf("the run had not ended %v after it was interrupted", runBound)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 0 {
			t.Errorf("the teardown reported %q, want nothing", r)
		}
	})
	t.Run("a run never stopped", func(t *testing.T) {
		useListed(t)
		var run *ownedRun
		t.Cleanup(func() { run.finish(t) })
		c := InstallCopier(t, Hang, "")
		run = startRun(t)
		c.WaitCopying(t)
		var r recorder
		c.release(r.report)
		if len(r) != 0 {
			t.Errorf("the teardown reported %q, want nothing", r)
		}
	})
	t.Run("a registered process that does not end", func(t *testing.T) {
		useListed(t)
		defer func(b time.Duration) { teardownBound = b }(teardownBound)
		teardownBound = 300 * time.Millisecond
		c := InstallCopier(t, Hang, "")
		// A process of the fixture that names its root and ignores the release.
		sleeper := exec.Command("sh", "-c", `exec sleep 30`, filepath.Join(c.state, "named"))
		if err := sleeper.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
		if err := os.WriteFile(filepath.Join(c.state, "registry"), []byte(fmt.Sprintf("child %d -\n", sleeper.Process.Pid)), 0o644); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		all := strings.Join(r, " ")
		for _, want := range []string{
			fmt.Sprintf("child %d (state", sleeper.Process.Pid),
			"still running when the teardown's deadline ended",
			fmt.Sprintf("still running: child %d state", sleeper.Process.Pid),
			"sleep 30",
		} {
			if !strings.Contains(all, want) {
				t.Errorf("the teardown reported %q, want %q", r, want)
			}
		}
	})
}

// What the teardown says of a process carries no path of the user's: the
// fixture's root is written <fixture>, the home directory ~, and a long
// command is cut short.
func TestStuckRedacts(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		t.Skip("no home directory to redact")
	}
	root := filepath.Join(home, "tmp", "TestX", "001")
	got := redact("/bin/sh "+root+"/bin/yt-dlp --cookies "+home+"/c.txt -- "+strings.Repeat("x", 200), root)
	if strings.Contains(got, home) || !strings.Contains(got, "<fixture>/bin/yt-dlp") || !strings.Contains(got, "~/c.txt") {
		t.Errorf("redact = %q", got)
	}
	if len(got) > 123 || !strings.HasSuffix(got, "...") {
		t.Errorf("redact kept %d bytes: %q", len(got), got)
	}
}
