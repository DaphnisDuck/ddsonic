//go:build unix

package ytdlptest

import (
	"errors"
	"syscall"
)

// alive reports whether the system still has a process pid: one that is
// running, or one that has ended and that its parent has not reaped yet.
// Signal 0 delivers nothing and only asks, on Linux and macOS alike.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
