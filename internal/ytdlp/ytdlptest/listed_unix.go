//go:build unix

package ytdlptest

// The teardown's exit proof where there is no /proc (macOS), from ps, and
// the listing of what is still running when a teardown gives up (R-02).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// procProof is whether the teardown proves exits from /proc (Linux), or
// from ps (awaitListed). A variable, so that a test on Linux can take the
// ps path.
var procProof = runtime.GOOS == "linux"

// listed is one process in a ps listing.
type listed struct {
	uid     int
	state   string
	command string
}

// zombie is whether a ps state is one of a process that has ended (its
// reaping is its parent's).
func zombie(state string) bool { return strings.HasPrefix(state, "Z") || strings.HasPrefix(state, "X") }

// psBound is how long one ps listing may take.
const psBound = 2 * time.Second

// psList lists every process: pid, uid, state and command line, by ps,
// which macOS and Linux both have with these options. A ps that fails, or a
// line it prints that is not a process, is an error, never "nothing runs".
var psList = func() (map[int]listed, error) {
	ctx, cancel := context.WithTimeout(context.Background(), psBound)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=", "-o", "uid=", "-o", "stat=", "-o", "command=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	ps := map[int]listed{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("ps printed %q, not a process", line)
		}
		pid, err1 := strconv.Atoi(f[0])
		uid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("ps printed %q, not a process", line)
		}
		ps[pid] = listed{uid: uid, state: f[2], command: strings.Join(f[3:], " ")}
	}
	return ps, nil
}

// awaitListed is awaitRegistered from ps: it waits, until deadline, until
// no registered run or child is listed as running, and no process of this
// user names this copier's root, and reads again until a reading finds
// nothing new. A process is known by its pid alone here: one that has
// exited and whose pid another process of the same moment has taken counts
// as still running, so a mistake costs a failed teardown, never a false
// "exited". A zombie has exited; its reaping is its parent's.
func (c *Copier) awaitListed(deadline time.Time) error {
	root := filepath.Dir(c.state) + "/"
	uid := os.Getuid()
	seen := -1
	for {
		rs, err := c.registry()
		if err != nil {
			return err
		}
		ps, err := psList()
		if err != nil {
			return err
		}
		var running []string
		for _, r := range rs {
			if p, ok := ps[r.pid]; ok && !zombie(p.state) {
				running = append(running, fmt.Sprintf("%s %d (state %s)", r.kind, r.pid, p.state))
			}
		}
		for pid, p := range ps {
			if pid != os.Getpid() && p.uid == uid && !zombie(p.state) && strings.Contains(p.command, root) {
				running = append(running, fmt.Sprintf("process %d (state %s)", pid, p.state))
			}
		}
		if len(running) > 0 && time.Now().After(deadline) {
			return fmt.Errorf("%s still running when the teardown's deadline ended; %s", strings.Join(running, ", "), c.stuck())
		}
		if len(running) == 0 && len(rs) == seen {
			return nil
		}
		if len(running) == 0 {
			seen = len(rs)
			continue
		}
		seen = -1
		time.Sleep(20 * time.Millisecond)
	}
}

// stuckMax is how many processes stuck lists.
const stuckMax = 8

// stuck says what of the fixture is still running, for a teardown that gave
// up: each registered run or child that ps still lists, and each process of
// this user whose command line names this copier's root, with its state and
// command line. The root is written <fixture> and the home directory ~, and
// each command is cut short, so the listing carries no path of the user's.
func (c *Copier) stuck() string {
	ps, err := psList()
	if err != nil {
		return "what is still running is unknown: " + err.Error()
	}
	root := filepath.Dir(c.state)
	kinds := map[int]string{}
	if rs, err := c.registry(); err == nil {
		for _, r := range rs {
			kinds[r.pid] = r.kind
		}
	}
	uid := os.Getuid()
	var lines []string
	for pid, p := range ps {
		kind, registered := kinds[pid]
		if !registered {
			if pid == os.Getpid() || p.uid != uid || !strings.Contains(p.command, root+"/") {
				continue
			}
			kind = "process"
		}
		lines = append(lines, fmt.Sprintf("%s %d state %s: %s", kind, pid, p.state, redact(p.command, root)))
	}
	if len(lines) == 0 {
		return "ps lists no process of the fixture"
	}
	sort.Strings(lines)
	more := ""
	if len(lines) > stuckMax {
		more = fmt.Sprintf(" (and %d more)", len(lines)-stuckMax)
		lines = lines[:stuckMax]
	}
	return "still running: " + strings.Join(lines, "; ") + more
}

// redact writes root as <fixture> and the home directory as ~ in command,
// and cuts it to 120 bytes.
func redact(command, root string) string {
	command = strings.ReplaceAll(command, root, "<fixture>")
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		command = strings.ReplaceAll(command, home, "~")
	}
	if len(command) > 120 {
		command = command[:120] + "..."
	}
	return command
}
