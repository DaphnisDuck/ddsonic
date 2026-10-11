package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// Grace is how long a yt-dlp run that was told to stop has to do so before
// it is killed.
const Grace = 3 * time.Second

// shutdownWait is how long Shutdown waits for the runs it stopped to be
// reaped and released: Grace for the slowest of them, and time for its
// caller to clean up.
const shutdownWait = Grace + 2*time.Second

// ErrShutdown is Command's error once Shutdown has been called.
var ErrShutdown = errors.New("yt-dlp: " + appdir.Name + " is quitting")

// running is the yt-dlp runs that have been made and whose release is not
// over yet, and the directories a release could not remove.
var running = struct {
	sync.Mutex
	stop   map[*exec.Cmd]context.CancelFunc
	left   map[string]error // by directory; the error of the last attempt
	closed bool
}{stop: map[*exec.Cmd]context.CancelFunc{}, left: map[string]error{}}

// Command returns a yt-dlp run of args, through Args, that leaves nothing in
// temporary storage, and release, which the caller calls once the run is
// over: after Run, Output or Wait has returned, or when the run was never
// started. Finish does it for a function that returns an error.
//
// yt-dlp reads a browser's cookies from a copy of its cookie database, which
// it makes in a temporary directory and removes when it has read it. Killed
// in between, it leaves the copy behind. So:
//
//   - when ctx ends, or ddsonic quits (Shutdown), the run is interrupted, as
//     Ctrl+C would, and yt-dlp removes what it made; only a run still alive
//     after Grace is killed (cmd.Cancel and cmd.WaitDelay);
//   - the run's temporary directory is one made for it alone, and release
//     removes it with whatever is left in it, however the run ended. Nothing
//     else in temporary storage is looked at or removed.
//
// Without that directory there is no run: Command fails rather than let
// yt-dlp copy the cookies somewhere a kill would leave them. release
// reports a directory it could not remove, and can be called again.
func Command(ctx context.Context, args ...string) (cmd *exec.Cmd, release func() error, err error) {
	dir, err := os.MkdirTemp("", appdir.Name+"-yt-dlp-")
	if err != nil {
		return nil, nil, fmt.Errorf("yt-dlp: no temporary directory of its own, so it is not run: %w", err)
	}
	ctx, stop := context.WithCancel(ctx)
	cmd = exec.CommandContext(ctx, "yt-dlp", Args(args...)...)
	cmd.Cancel = func() error { return interrupt(cmd.Process) }
	cmd.WaitDelay = Grace
	cmd.Env = append(os.Environ(), "TMPDIR="+dir, "TEMP="+dir, "TMP="+dir)

	running.Lock()
	closed := running.closed
	if !closed {
		running.stop[cmd] = stop
	}
	running.Unlock()
	release = func() error {
		stop()
		// The run stays in running until the attempt is over, so that
		// Shutdown waits for it and sees how it went.
		err := os.RemoveAll(dir)
		if err != nil {
			err = fmt.Errorf("yt-dlp: its temporary directory %s could not be removed, and may hold a copy of the browser's cookies: %w", dir, err)
		}
		running.Lock()
		delete(running.stop, cmd)
		if err != nil {
			running.left[dir] = err
		} else {
			delete(running.left, dir)
		}
		running.Unlock()
		return err
	}
	if closed {
		return nil, nil, errors.Join(ErrShutdown, release())
	}
	return cmd, release, nil
}

// Finish releases a run when the function that made it returns, and adds a
// directory that could not be removed to the function's error:
//
//	cmd, release, err := ytdlp.Command(ctx, args...)
//	if err != nil {
//		return err
//	}
//	defer ytdlp.Finish(release, &err)
func Finish(release func() error, err *error) {
	*err = errors.Join(*err, release())
}

// Shutdown is for ddsonic's normal exit. It stops every run that has not
// been released, as their contexts ending would (interrupted, and killed
// after Grace), and waits for their callers to reap and release them. It
// reports the runs that were still there when the wait ended, and every
// directory a release could not remove and that is still there: a caller's
// own report of that may have nowhere left to go. No run is made after it.
//
// A ddsonic that is itself killed runs none of this, and what its yt-dlp
// runs leave is theirs.
func Shutdown() error {
	running.Lock()
	running.closed = true
	for _, stop := range running.stop {
		stop()
	}
	running.Unlock()

	for deadline := time.Now().Add(shutdownWait); ; time.Sleep(5 * time.Millisecond) {
		running.Lock()
		left := len(running.stop)
		running.Unlock()
		switch {
		case left == 0:
			return leftBehind()
		case time.Now().After(deadline):
			return errors.Join(leftBehind(), fmt.Errorf("yt-dlp: %d run(s) had not stopped %v after they were asked to; their temporary directories (%s-yt-dlp-*) may remain", left, shutdownWait, appdir.Name))
		}
	}
}

// leftBehind is the errors of the directories that could not be removed and
// still exist.
func leftBehind() error {
	running.Lock()
	defer running.Unlock()
	var errs []error
	for _, dir := range slices.Sorted(maps.Keys(running.left)) {
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			delete(running.left, dir)
			continue
		}
		errs = append(errs, running.left[dir])
	}
	return errors.Join(errs...)
}

// Reopen undoes Shutdown. Tests of quitting use it.
func Reopen() {
	running.Lock()
	running.closed = false
	running.Unlock()
}

// interrupt asks p to stop and clean up after itself. Where a process cannot
// be interrupted (Windows), it is killed.
func interrupt(p *os.Process) error {
	err := p.Signal(os.Interrupt)
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return p.Kill()
}
