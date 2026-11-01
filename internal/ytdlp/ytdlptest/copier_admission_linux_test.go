package ytdlptest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startHolder starts a CONTROL process (not the fake) that registers itself
// exactly as the fake's children do, opens the alive pipe, closes it again,
// creates ready, and then lives for life seconds: a registered process
// whose descriptor is closed before the teardown looks, and which has not
// exited (a stand-in for the kernel's interval between a process closing its
// descriptors and its exit). Its cleanup (kill, bounded reap) is registered
// at once. It returns the process's identity once ready exists.
func startHolder(t *testing.T, c *Copier, life string) seen {
	t.Helper()
	return startControl(t, `ready=$1 life=$2; s=; read -r s </proc/self/stat; set -- $s; printf "child %s %s\n" "$$" "${22}" >>"$0/registry"; exec 8<>"$0/alive"; exec 8>&-; : >"$ready"; exec sleep "$life"`, c, life)
}

// startControl starts the CONTROL script with its arguments: $0 the
// copier's state directory (so its command line names the copier's root),
// $1 the ready path, $2 life. Its cleanup is registered at once; it returns
// the process's identity once ready exists.
func startControl(t *testing.T, script string, c *Copier, life string) seen {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	h := exec.Command("sh", "-c", script, c.state, ready, life)
	if err := h.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = h.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = h.Process.Kill()
		select {
		case <-done:
		case <-time.After(runBound):
			t.Errorf("the control holder %d was not reaped %v after it was killed", h.Process.Pid, runBound)
		}
	})
	for deadline := time.Now().Add(runBound); ; time.Sleep(2 * time.Millisecond) {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the control holder was not ready within %v", runBound)
		}
	}
	_, start, ok, err := procStat(h.Process.Pid)
	if err != nil || !ok {
		t.Fatalf("the control holder: %v (found %v)", err, ok)
	}
	return seen{h.Process.Pid, start}
}

// The teardown does not accept a registered process that has closed its
// descriptors but not exited: it returns only once that process has exited
// (here about a second after it closed the pipe), reporting nothing; and it
// reports one that is still running at its deadline.
func TestCopierTeardownAwaitsARegisteredProcessThatClosedThePipe(t *testing.T) {
	t.Run("it exits within the bound", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		h := startHolder(t, c, "1")
		var reports []string
		start := time.Now()
		c.release(func(f string, a ...any) { reports = append(reports, fmt.Sprintf(f, a...)) })
		took := time.Since(start)
		if len(reports) != 0 {
			t.Errorf("the teardown reported %q, want nothing", reports)
		}
		running, err := h.running()
		if err != nil || running {
			t.Errorf("the teardown returned after %v while the registered process %d still ran (%v, %v)", took, h.pid, running, err)
		}
	})
	// The handoff: a process of the fixture that has not recorded itself yet
	// (here a CONTROL that never records itself, whose command line names the
	// copier's root, as a run's shell, and a child it has just forked, do)
	// is awaited too; it then writes into the state directory, as a child's
	// late registration would, which must happen before the teardown
	// returns, not while the directory is removed.
	t.Run("an unregistered process naming the copier's root", func(t *testing.T) {
		c := InstallCopier(t, Hang, "")
		h := startControl(t, `ready=$1 life=$2; : >"$ready"; sleep "$life"; printf late >>"$0/late"`, c, "1")
		var reports []string
		c.release(func(f string, a ...any) { reports = append(reports, fmt.Sprintf(f, a...)) })
		if len(reports) != 0 {
			t.Errorf("the teardown reported %q, want nothing", reports)
		}
		running, err := h.running()
		if err != nil || running {
			t.Errorf("the teardown returned while the unregistered process %d still ran (%v, %v)", h.pid, running, err)
		}
		if _, err := os.Stat(filepath.Join(c.state, "late")); err != nil {
			t.Errorf("the process's last write did not happen before the teardown returned: %v", err)
		}
	})
	t.Run("it is still running at the deadline", func(t *testing.T) {
		defer func(b time.Duration) { teardownBound = b }(teardownBound)
		teardownBound = 300 * time.Millisecond
		c := InstallCopier(t, Hang, "")
		h := startHolder(t, c, "30")
		var reports []string
		c.release(func(f string, a ...any) { reports = append(reports, fmt.Sprintf(f, a...)) })
		if len(reports) == 0 || !strings.Contains(strings.Join(reports, " "), fmt.Sprint(h.pid)) {
			t.Errorf("the teardown reported %q, want the registered process %d still running", reports, h.pid)
		}
	})
}
