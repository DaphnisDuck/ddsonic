//go:build unix

package ytdlptest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// recorder collects what a teardown reports, in place of t.Errorf.
type recorder []string

func (r *recorder) report(format string, args ...any) { *r = append(*r, fmt.Sprintf(format, args...)) }

// The fixture's teardown reports every way it cannot establish that the
// runs and their children are gone, and nothing when they are. Each case
// tears down by hand (the later automatic teardown is then a no-op).
func TestCopierTeardownReportsWhatItCannotEstablish(t *testing.T) {
	t.Run("nothing ran", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		var r recorder
		c.release(r.report)
		if len(r) != 0 {
			t.Errorf("the teardown reported %q, want nothing", r)
		}
	})
	t.Run("a holder that does not end", func(t *testing.T) {
		defer func(b time.Duration) { teardownBound = b }(teardownBound)
		teardownBound = 300 * time.Millisecond
		c := InstallCopier(t, Hang, "")
		ready := filepath.Join(t.TempDir(), "ready")
		holder := exec.Command("sh", "-c", `exec 8<>"$0"; : >"$1"; exec sleep 30`, filepath.Join(c.state, "alive"), ready)
		if err := holder.Start(); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = holder.Wait(); close(done) }()
		t.Cleanup(func() {
			_ = holder.Process.Kill()
			select {
			case <-done:
			case <-time.After(runBound):
				t.Errorf("the holder %d was not reaped %v after it was killed", holder.Process.Pid, runBound)
			}
		})
		for deadline := time.Now().Add(runBound); ; time.Sleep(2 * time.Millisecond) {
			if _, err := os.Stat(ready); err == nil {
				break // the holder has opened the pipe
			}
			if time.Now().After(deadline) {
				t.Fatalf("the holder did not open the pipe within %v", runBound)
			}
		}
		var r recorder
		start := time.Now()
		c.release(r.report)
		if took := time.Since(start); took > teardownBound+time.Second {
			t.Errorf("the teardown took %v, want about its bound %v", took, teardownBound)
		}
		// The join reaches the deadline; release's final check then refuses
		// the late completion as well.
		if !strings.Contains(strings.Join(r, " "), "still holds the alive pipe") {
			t.Errorf("the teardown reported %q, want that something still holds the alive pipe", r)
		}
	})
	t.Run("a read that fails", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		_ = c.alive.Close()
		var r recorder
		c.release(r.report)
		// Each step that needs the pipe reports it (Linux: the identification
		// of the live processes and the join; elsewhere the join alone).
		if len(r) == 0 {
			t.Error("the teardown reported nothing, want the failed read")
		}
		for _, m := range r {
			if !strings.Contains(m, "the alive pipe") {
				t.Errorf("the teardown reported %q, want only the alive pipe's failures", r)
			}
		}
	})
	t.Run("data on the alive pipe", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		w, err := os.OpenFile(filepath.Join(c.state, "alive"), os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.WriteString("x"); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "holds data") {
			t.Errorf("the teardown reported %q, want the unexpected data", r)
		}
	})
	t.Run("the hold pipe already gone", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		if err := os.Remove(filepath.Join(c.state, "hold")); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "the hold pipe:") {
			t.Errorf("the teardown reported %q, want the hold pipe's failure", r)
		}
	})
	t.Run("an unreadable registry", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("only Linux reads the registry (awaitRegistered)")
		}
		if os.Geteuid() == 0 {
			t.Skip("root reads every file")
		}
		c := InstallCopier(t, Hang, "")
		reg := filepath.Join(c.state, "registry")
		if err := os.WriteFile(reg, nil, 0o000); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "the registry") {
			t.Errorf("the teardown reported %q, want the unreadable registry", r)
		}
	})
	t.Run("a malformed registry line", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("only Linux reads the registry (awaitRegistered)")
		}
		c := InstallCopier(t, Hang, "")
		if err := os.WriteFile(filepath.Join(c.state, "registry"), []byte("child 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "not a run or child line") {
			t.Errorf("the teardown reported %q, want the malformed line", r)
		}
	})
	t.Run("a registered process without a start time", func(t *testing.T) {
		if runtime.GOOS != "linux" {
			t.Skip("only Linux has the start time")
		}
		c := InstallCopier(t, Hang, "")
		if err := os.WriteFile(filepath.Join(c.state, "registry"), []byte(fmt.Sprintf("child %d -\n", os.Getpid())), 0o644); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "without its start time") {
			t.Errorf("the teardown reported %q, want the unresolved identity", r)
		}
	})
	t.Run("a registered process that is gone", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		gone := exec.Command("sh", "-c", `s=; read -r s </proc/self/stat; set -- $s; printf "child %s %s\n" "$$" "${22:--}" >>"$0/registry"`, c.state)
		if err := gone.Run(); err != nil {
			t.Fatal(err)
		}
		var r recorder
		c.release(r.report)
		if len(r) != 0 {
			t.Errorf("the teardown reported %q, want nothing for a process that has exited", r)
		}
	})
	t.Run("reads interrupted until the deadline", func(t *testing.T) {
		defer func(b time.Duration) { teardownBound = b }(teardownBound)
		teardownBound = 300 * time.Millisecond
		c := InstallCopier(t, Hang, "")
		defer func(f func(int, []byte) (int, error)) { readAlive = f }(readAlive)
		readAlive = func(int, []byte) (int, error) { return -1, syscall.EINTR }
		var r recorder
		done := make(chan struct{})
		start := time.Now()
		go func() { c.release(r.report); close(done) }()
		select {
		case <-done:
		case <-time.After(teardownBound + runBound):
			t.Fatalf("the teardown was still waiting %v after its bound %v", time.Since(start), teardownBound)
		}
		if !strings.Contains(strings.Join(r, " "), "before the deadline") {
			t.Errorf("the teardown reported %q, want that the alive pipe could not be read before the deadline", r)
		}
	})
	t.Run("an acceptance after the deadline", func(t *testing.T) {
		defer func(b time.Duration) { teardownBound = b }(teardownBound)
		teardownBound = time.Nanosecond
		c := InstallCopier(t, Hang, "")
		var r recorder
		c.release(r.report)
		if !strings.Contains(strings.Join(r, " "), "deadline") {
			t.Errorf("the teardown reported %q, want the deadline", r)
		}
	})
	t.Run("a run started after the teardown does not hang", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		var r recorder
		c.release(r.report)
		if len(r) != 0 {
			t.Fatalf("the teardown reported %q", r)
		}
		run := startRun(t)
		if !run.result(runBound) {
			_ = run.cmd.Process.Kill()
			if !run.result(runBound) {
				t.Errorf("a run started after the teardown was not reaped %v after it was killed", runBound)
			}
			t.Errorf("a run started after the teardown was still running %v later", runBound)
		}
	})
}

// Every process of the fake records itself before it touches anything else
// of the fixture: the run before anything (copy, pids, mode), the child
// before it opens the alive pipe or blocks on the hold pipe; and no run
// holds the alive pipe, so a process the run starts does not inherit it
// before it has recorded itself. (The order the teardown's admission rests
// on, read from the script itself.)
func TestCopierScriptRegistersBeforeItHoldsAnything(t *testing.T) {
	s := copierScript
	at := func(sub string) int {
		i := strings.Index(s, sub)
		if i < 0 {
			t.Fatalf("the script has no %q", sub)
		}
		return i
	}
	run := at(`printf 'run %s %s\n' "$$" "$ID" >>"$state/registry"`)
	for _, later := range []string{`copy="${TMPDIR:-/tmp}/yt_dlp$$"`, `>>"$state/pids"`, `mode=$(cat "$state/mode")`, `sh -c '`} {
		if at(later) < run {
			t.Errorf("the script does %q before the run records itself", later)
		}
	}
	child := at(`printf "child %s %s\n" "$$" "${22:--}" >>"$0/registry"`)
	for _, later := range []string{`exec 8<>"$0/alive"`, `exec cat "$0/hold"`} {
		if at(later) < child {
			t.Errorf("the child does %q before it records itself", later)
		}
	}
	if strings.Count(s, "8<>") != 1 {
		t.Errorf("the script opens the alive pipe %d times, want once (the child's own)", strings.Count(s, "8<>"))
	}
	if strings.Contains(s, `kill "$child"`) {
		t.Error("the run signals its child; nothing may stop a child before it has recorded itself")
	}
}
