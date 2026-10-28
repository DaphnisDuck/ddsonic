package ytdlptest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// seen is a process this test observed, identified by its pid and its
// start time (field 22 of /proc/<pid>/stat), so that a pid used again by
// another process is never taken for it. The test reads /proc itself: it
// does not rely on the fixture's own identification of its processes.
type seen struct {
	pid   int
	start string
}

// procStat reads /proc/<pid>/stat: the state and the start time. ok is false
// when the process is gone (no such file); any other failure is an error,
// never "gone".
func procStat(pid int) (state, start string, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if os.IsNotExist(err) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", "", false, fmt.Errorf("/proc/%d/stat: %q", pid, s)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", "", false, fmt.Errorf("/proc/%d/stat: %q", pid, s)
	}
	return f[0], f[19], true, nil
}

// running reports whether p still runs: it exists, is the same process
// (same start time) and has not exited (not a zombie).
func (p seen) running() (bool, error) {
	state, start, ok, err := procStat(p.pid)
	if err != nil || !ok || start != p.start {
		return false, err
	}
	return state != "Z", nil
}

// children returns the children of pid that the kernel lists
// (/proc/<pid>/task/<pid>/children), with their start times.
func children(pid int) ([]seen, error) {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "task", strconv.Itoa(pid), "children"))
	if err != nil {
		return nil, err
	}
	var ps []seen
	for _, field := range strings.Fields(string(b)) {
		c, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("children of %d: %q", pid, b)
		}
		_, start, ok, err := procStat(c)
		if err != nil {
			return nil, err
		}
		if ok {
			ps = append(ps, seen{c, start})
		}
	}
	return ps, nil
}

// blocker waits until the run with pid has exactly one child, the child
// it blocks on while it holds its copy, and returns it.
func blocker(t *testing.T, pid int) seen {
	t.Helper()
	var last []seen
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		ps, err := children(pid)
		if err != nil {
			t.Fatal(err)
		}
		if len(ps) == 1 {
			return ps[0]
		}
		last = ps
	}
	t.Fatalf("the run %d has children %v within 5 s, want exactly the one it blocks on", pid, last)
	return seen{}
}

// requireGone fails unless p no longer runs.
func requireGone(t *testing.T, what string, p seen) {
	t.Helper()
	running, err := p.running()
	if err != nil {
		t.Fatalf("%s %d: %v", what, p.pid, err)
	}
	if running {
		t.Errorf("%s %d still runs after the fixture's teardown", what, p.pid)
	}
}

// startUnrelated starts a process that is not the fixture's, and registers
// its cleanup at once (before anything that can fail), and returns its
// identity.
func startUnrelated(t *testing.T, name string, args ...string) seen {
	t.Helper()
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(runBound):
			t.Errorf("the unrelated %s %d was not reaped %v after it was killed", name, cmd.Process.Pid, runBound)
		}
	})
	_, start, ok, err := procStat(cmd.Process.Pid)
	if err != nil || !ok {
		t.Fatalf("the unrelated %s: %v (found %v)", name, err, ok)
	}
	return seen{cmd.Process.Pid, start}
}

// A run of the fake that holds its copy blocks on a child; however the run
// is stopped (or not stopped at all), that child does not outlive the test
// that made it. The run itself behaves as before: killed or terminated it
// leaves its copy, interrupted it removes it, and a Stubborn run ignores
// both requests until it is killed. Each case observes the child while the
// run holds its copy, then stops the run, and checks the child once the
// subtest, and so the fixture's teardown, is over. Unrelated processes
// running the programs the fake uses are never touched.
func TestCopierOwnsTheChildARunBlocksOn(t *testing.T) {
	if _, err := os.Stat("/proc/self/task/" + strconv.Itoa(os.Getpid()) + "/children"); err != nil {
		t.Skipf("the kernel lists no children here: %v", err)
	}
	others := map[string]seen{"sleep": startUnrelated(t, "sleep", "60"), "cat": startUnrelated(t, "cat"), "sh": startUnrelated(t, "sh", "-c", "exec cat")}

	tests := []struct {
		name    string
		mode    string
		stop    func(t *testing.T, c *Copier, cmd *exec.Cmd)
		wantErr bool // the run's own exit
		left    bool // its copy is left behind
	}{
		{"killed", Hang, signal(syscall.SIGKILL), true, true},
		{"terminated", Hang, signal(syscall.SIGTERM), true, true},
		{"interrupted", Hang, signal(syscall.SIGINT), true, false},
		{"stubborn, then killed", Stubborn, func(t *testing.T, c *Copier, cmd *exec.Cmd) {
			if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond)
			if live := c.Live(t); live != 1 {
				t.Fatalf("%d runs alive after INT and TERM, want the Stubborn one", live)
			}
			signal(syscall.SIGKILL)(t, c, cmd)
		}, true, true},
	}
	for _, tt := range tests {
		var child seen
		var copies []string
		var run *ownedRun
		ok := t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(func() { run.finish(t) })
			c := InstallCopier(t, tt.mode, "")
			run = startRun(t)
			c.WaitCopying(t)
			child = blocker(t, run.cmd.Process.Pid)
			tt.stop(t, c, run.cmd)
			if !run.result(runBound) {
				t.Fatalf("the run had not ended %v after it was stopped", runBound)
			}
			copies = c.CopiesLeft(t)
		})
		if !ok {
			t.Fatalf("%s: the subtest failed", tt.name)
		}
		if (run.err != nil) != tt.wantErr {
			t.Errorf("%s: the run exited with %v, want an error = %v", tt.name, run.err, tt.wantErr)
		}
		if (len(copies) > 0) != tt.left {
			t.Errorf("%s: copies left %q, want some = %v", tt.name, copies, tt.left)
		}
		requireGone(t, tt.name+": the child its run blocked on", child)
	}

	// A test that ends while its run still holds the copy, stopping nothing
	// (an early return: what a failing t.Fatal also does): the teardown lets
	// the run finish as it would have, and its child too. The run's Wait is
	// received by its cleanup, after the teardown; the parent checks it.
	var child seen
	var run *ownedRun
	t.Run("never stopped", func(t *testing.T) {
		t.Cleanup(func() { run.finish(t) })
		c := InstallCopier(t, Hang, "")
		run = startRun(t)
		c.WaitCopying(t)
		child = blocker(t, run.cmd.Process.Pid)
		t.SkipNow()
	})
	switch {
	case run == nil || !run.waited:
		t.Errorf("never stopped: the run's Wait was not received after the teardown")
	case run.err != nil:
		t.Errorf("never stopped: the run ended with %v after the teardown, want it to finish", run.err)
	}
	requireGone(t, "never stopped: the child its run blocked on", child)

	for name, p := range others {
		running, err := p.running()
		if err != nil || !running {
			t.Errorf("the unrelated %s %d was touched: running = %v, %v", name, p.pid, running, err)
		}
	}
}

func signal(s syscall.Signal) func(*testing.T, *Copier, *exec.Cmd) {
	return func(t *testing.T, _ *Copier, cmd *exec.Cmd) {
		if err := cmd.Process.Signal(s); err != nil {
			t.Fatal(err)
		}
	}
}
