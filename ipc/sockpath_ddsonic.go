// ddsonic: a Unix socket's path must fit the kernel's sockaddr_un, which is
// far shorter than a file path may be. A longer path can never be listened
// on or dialed, and without this check it fails as "invalid argument".

package ipc

import (
	"fmt"
	"runtime"
	"syscall"
)

// MaxSocketPathBytes is the longest socket path, in bytes, that this platform
// accepts: sun_path less the NUL that ends the path, so 107 on Linux and 103
// on macOS.
const MaxSocketPathBytes = len(syscall.RawSockaddrUnix{}.Path) - 1

// abstractAddresses says whether Go takes abstract socket addresses here, as
// its syscall package does for Linux (Android included) and Windows.
const abstractAddresses = runtime.GOOS == "linux" || runtime.GOOS == "android" || runtime.GOOS == "windows"

// socketPathLimit is the most bytes path may have. An abstract address
// starts with "@" or a NUL, names no file and has no closing NUL, so it may
// fill sun_path. ddsonic never computes one (its socket is a file in the
// config directory); the allowance keeps what this package took before.
func socketPathLimit(path string) int {
	if abstractAddresses && path != "" && (path[0] == '@' || path[0] == 0) {
		return MaxSocketPathBytes + 1
	}
	return MaxSocketPathBytes
}

// SocketPathError reports a socket path longer than the platform accepts.
// Like ErrNotRunning it carries no advice: the command layer knows where the
// path came from and says what to do.
type SocketPathError struct {
	Path  string
	Bytes int // len(Path): bytes, not characters
	Max   int
}

func (e *SocketPathError) Error() string {
	return fmt.Sprintf("socket path is %d bytes, at most %d fit: %s", e.Bytes, e.Max, e.Path)
}

// ValidateSocketPath returns a *SocketPathError when path is too long to be
// a Unix socket's address on this platform. The server and every client
// check through it (newServer, dialSocket).
func ValidateSocketPath(path string) error {
	limit := socketPathLimit(path)
	if len(path) <= limit {
		return nil
	}
	return &SocketPathError{Path: path, Bytes: len(path), Max: limit}
}
