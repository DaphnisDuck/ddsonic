//go:build unix

package ytdlptest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// own makes the hold and alive pipes (FIFOs in the state directory) and
// opens them: the hold pipe for reading and writing, so that the test is a
// writer of it and a child reading it waits for as long as the test holds
// it open; the alive pipe for reading, without blocking, so that the test
// can tell when no child holds it any more. Both are closed on exec, so no
// process the test starts inherits them.
func (c *Copier) own() error {
	for _, name := range []string{"hold", "alive"} {
		if err := syscall.Mkfifo(filepath.Join(c.state, name), 0o600); err != nil {
			return fmt.Errorf("ytdlptest: the %s pipe: %w", name, err)
		}
	}
	hold, err := os.OpenFile(filepath.Join(c.state, "hold"), os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("ytdlptest: the hold pipe: %w", err)
	}
	alive, err := os.OpenFile(filepath.Join(c.state, "alive"), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		_ = hold.Close()
		return fmt.Errorf("ytdlptest: the alive pipe: %w", err)
	}
	c.hold, c.alive = hold, alive
	return nil
}

// unhold releases the children. The two pipes' names are removed first, so
// that a child started from now on finds nothing to open (it has registered
// already, and exits at once); then the hold pipe is closed, so that every
// child reading it reaches its end and exits.
func (c *Copier) unhold() error {
	return errors.Join(os.Remove(filepath.Join(c.state, "hold")), os.Remove(filepath.Join(c.state, "alive")), c.hold.Close())
}

// join waits, until deadline, until no child holds the alive pipe: a read
// of a pipe nobody holds ends (end of file). Anything else is reported: the
// deadline reached (something still holds it), data on it (nothing writes
// there), or a read that fails. None of those is ever taken for "none
// left". A process closes its descriptors early in its exit, so this does
// not show that any process has exited; awaitRegistered does.
//
// The pipe is read without blocking, again every few milliseconds until the
// deadline. Go cannot put a read deadline on a FIFO on macOS (it does not
// poll FIFOs there, Go issue 24164), so the wait is bounded here, the same
// way on every system.
func (c *Copier) join(deadline time.Time) error {
	defer func() { _ = c.alive.Close() }()
	bound := time.Until(deadline)
	raw, err := c.alive.SyscallConn()
	if err != nil {
		return fmt.Errorf("the alive pipe: %w", err)
	}
	// own opened it without blocking; made sure of here, as a blocking read
	// would wait past the deadline.
	var nbErr error
	if err := raw.Control(func(fd uintptr) { nbErr = syscall.SetNonblock(int(fd), true) }); err != nil {
		return fmt.Errorf("the alive pipe: %w", err)
	}
	if nbErr != nil {
		return fmt.Errorf("the alive pipe: %w", nbErr)
	}
	var b [64]byte
	for {
		var n int
		var readErr error
		if err := raw.Read(func(fd uintptr) bool {
			n, readErr = readAlive(int(fd), b[:])
			return true // one attempt; the loop does the waiting
		}); err != nil {
			return fmt.Errorf("the alive pipe: %w", err)
		}
		switch {
		case readErr == nil && n > 0:
			return fmt.Errorf("the alive pipe holds data (%q); nothing of the fixture writes there", b[:n])
		case readErr == nil:
			return nil // end of file: nobody holds it
		case !errors.Is(readErr, syscall.EAGAIN) && !errors.Is(readErr, syscall.EINTR):
			return fmt.Errorf("the alive pipe: %w", readErr)
		}
		// EAGAIN: something still holds it, and nothing was written; EINTR:
		// the read was interrupted. Either way, read again until the deadline.
		if !time.Now().Before(deadline) {
			if errors.Is(readErr, syscall.EINTR) {
				return fmt.Errorf("the alive pipe could not be read before the deadline, %v after it was released: %w", bound, readErr)
			}
			return fmt.Errorf("a child of a run of the fake yt-dlp still holds the alive pipe %v after it was released", bound)
		}
		time.Sleep(min(2*time.Millisecond, time.Until(deadline)))
	}
}

// readAlive reads the alive pipe once; a variable, so that a test can
// interrupt the reading.
var readAlive = syscall.Read

// registered is one line of the registry: a run ("run") or the child a run
// blocks on ("child"), by its pid and its start time ("-" where it could
// not read its own: no /proc).
type registered struct {
	kind  string
	pid   int
	start string
}

// registry reads every run and child that recorded itself. Each one writes
// its line as its first action, before it opens the alive pipe or blocks
// on the hold pipe, so a process of the fixture that could still run is
// there. A registry that does not exist means that nothing ran; any other
// failure, or a line that is not "run|child PID START", is an error.
func (c *Copier) registry() ([]registered, error) {
	b, err := os.ReadFile(filepath.Join(c.state, "registry"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the registry: %w", err)
	}
	if len(b) == 0 {
		return nil, nil
	}
	var rs []registered
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || (f[0] != "run" && f[0] != "child") {
			return nil, fmt.Errorf("the registry holds %q, not a run or child line", line)
		}
		pid, err := strconv.Atoi(f[1])
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("the registry holds %q, not a pid", line)
		}
		if f[2] != "-" {
			if _, err := strconv.ParseUint(f[2], 10, 64); err != nil {
				return nil, fmt.Errorf("the registry holds %q, not a start time", line)
			}
		}
		rs = append(rs, registered{f[0], pid, f[2]})
	}
	return rs, nil
}

// awaitRegistered waits, until deadline, until no process of the fixture
// runs, and reads again until a reading finds nothing new. Two readings
// together establish it, on Linux:
//   - the registry: every run and child that recorded itself, by pid and
//     start time, followed through its exit (when its pid is gone, is
//     another process's, or is a zombie: it runs nothing, and its reaping is
//     its parent's), even after it has closed its descriptors and its
//     command line can no longer be read;
//   - a /proc scan for every live process of this user whose command line
//     names this copier's own root (its unique temporary directory): the
//     runs' shells, a child the shell has forked and that has not recorded
//     itself yet, the child, its cat, the run's mkdir and cat. Nothing else
//     names that directory.
//
// Elsewhere there is no /proc, and the teardown rests on join (the children
// have closed the alive pipe), without an exit proof. A /proc read that
// fails, a registered process without a start time, or one still running at
// the deadline is reported, never taken for "exited". An observation made
// after the deadline is refused by release's final check, against the same
// deadline.
func (c *Copier) awaitRegistered(deadline time.Time) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	done := map[registered]bool{}
	seen := -1
	for {
		rs, err := c.registry()
		if err != nil {
			return err
		}
		running, err := c.scan()
		if err != nil {
			return err
		}
		for _, r := range rs {
			if done[r] {
				continue
			}
			if r.start == "-" {
				return fmt.Errorf("the %s %d registered without its start time; it cannot be told from another process with its pid", r.kind, r.pid)
			}
			state, ok, err := procState(r.pid, r.start)
			if err != nil {
				return fmt.Errorf("the %s %d: %w", r.kind, r.pid, err)
			}
			if ok && state != "Z" && state != "X" {
				running = append(running, fmt.Sprintf("%s %d (state %s)", r.kind, r.pid, state))
				continue
			}
			done[r] = true
		}
		if len(running) > 0 && time.Now().After(deadline) {
			return fmt.Errorf("%s still running when the teardown's deadline ended", strings.Join(running, ", "))
		}
		if len(running) == 0 && len(rs) == seen {
			return nil
		}
		if len(running) == 0 {
			seen = len(rs)
			continue
		}
		seen = -1
		time.Sleep(2 * time.Millisecond)
	}
}

// readStat reads /proc/<pid>/stat; a variable, so that a test can make the
// reading fail.
var readStat = func(pid int) ([]byte, error) {
	return os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
}

// procState is the state (field 3 of /proc/<pid>/stat) of the process with
// pid and start time start (field 22; field 2, the command name, may hold
// spaces). ok is false when there is no such process: the pid is gone, or
// another process now has it. Any other failure is an error, never "gone".
func procState(pid int, start string) (state string, ok bool, err error) {
	b, err := readStat(pid)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false, fmt.Errorf("/proc/%d/stat: %q", pid, s)
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return "", false, fmt.Errorf("/proc/%d/stat: %q", pid, s)
	}
	if f[19] != start {
		return "", false, nil
	}
	return f[0], true, nil
}

// scan lists the live processes of this user (not zombies) whose command
// line names this copier's root. A process of another user is not the
// fixture's (the fixture's run as the test's user); a process that is gone
// while it is read is skipped; any other failure to read one is an error.
func (c *Copier) scan() ([]string, error) {
	root := []byte(filepath.Dir(c.state) + "/")
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("listing /proc: %w", err)
	}
	uid := uint32(os.Getuid())
	var found []string
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join("/proc", e.Name())
		fi, err := os.Stat(dir)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("process %d: %w", pid, err)
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != uid {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("process %d: %w", pid, err)
		}
		if !bytesContains(cmdline, root) {
			continue
		}
		b, err := readStat(pid)
		if gone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("process %d: %w", pid, err)
		}
		state := statState(b)
		if state == "" {
			return nil, fmt.Errorf("/proc/%d/stat: %q", pid, b)
		}
		if state != "Z" && state != "X" {
			found = append(found, fmt.Sprintf("process %d (state %s) naming %s", pid, state, root))
		}
	}
	return found, nil
}

// gone is whether err says the process no longer exists.
func gone(err error) bool { return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH) }

func bytesContains(b, sub []byte) bool { return strings.Contains(string(b), string(sub)) }

// statState is field 3 of a /proc/<pid>/stat (after the command name, which
// may hold spaces and parentheses), or "" when it cannot be read.
func statState(b []byte) string {
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return ""
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 1 {
		return ""
	}
	return f[0]
}
