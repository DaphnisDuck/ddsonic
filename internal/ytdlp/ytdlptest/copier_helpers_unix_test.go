//go:build unix

package ytdlptest

import (
	"os/exec"
	"testing"
	"time"
)

// runBound bounds each wait of the test for a run it started (the fixture's
// own teardown has its own bound).
const runBound = 5 * time.Second

// ownedRun is a run of the fake this test started. Its one Wait is on a
// goroutine started at once (done); the test receives it once, with a
// bound, through result.
type ownedRun struct {
	cmd    *exec.Cmd
	done   chan error
	err    error
	waited bool
}

// startRun starts a run of the fake yt-dlp and its Wait.
func startRun(t *testing.T) *ownedRun {
	t.Helper()
	r := &ownedRun{cmd: exec.Command("yt-dlp", "--", "https://example.invalid/v"), done: make(chan error, 1)}
	if err := r.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { r.done <- r.cmd.Wait() }()
	return r
}

// result receives the run's Wait once, within bound; false when the bound
// ended first (the run is then still the test's, for finish).
func (r *ownedRun) result(bound time.Duration) bool {
	if r.waited {
		return true
	}
	select {
	case r.err = <-r.done:
		r.waited = true
		return true
	case <-time.After(bound):
		return false
	}
}

// finish is the run's failure-path cleanup, registered BEFORE InstallCopier
// so that it runs after the fixture's teardown (cleanups run last-registered
// first): a run that is still blocked can only end once the teardown has
// released it. If the run has not ended within runBound, it is killed and
// its Wait received within runBound more; either way that is reported.
func (r *ownedRun) finish(t *testing.T) {
	if r == nil || r.result(runBound) {
		return
	}
	t.Errorf("the run %d had not ended %v after the fixture's teardown; killed", r.cmd.Process.Pid, runBound)
	_ = r.cmd.Process.Kill()
	if !r.result(runBound) {
		t.Errorf("the run %d was not reaped %v after it was killed", r.cmd.Process.Pid, runBound)
	}
}
