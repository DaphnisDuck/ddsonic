package ytdlptest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// A /proc read of a registered process that fails (other than "no such
// process") is reported: it is never taken for the process having exited.
func TestCopierTeardownReportsAFailedProcRead(t *testing.T) {
	{
		c := InstallCopier(t, Hang, "")
		h := startHolder(t, c, "30")
		defer func(f func(int) ([]byte, error)) { readStat = f }(readStat)
		readStat = func(pid int) ([]byte, error) {
			if pid == h.pid {
				return nil, syscall.EIO
			}
			return os.ReadFile(filepath.Join("/proc", fmt.Sprint(pid), "stat"))
		}
		var r recorder
		c.release(r.report)
		if len(r) != 1 || !strings.Contains(r[0], "input/output error") {
			t.Errorf("the teardown reported %q, want the failed read of %d", r, h.pid)
		}
	}
}
