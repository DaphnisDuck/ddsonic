package main

// ddsonic: what a user is told when the IPC socket's path is too long to be
// a Unix socket's address (ipc/sockpath_ddsonic.go). The socket sits in the
// config directory, so a long DDSONIC_CONFIG_DIR, XDG_CONFIG_HOME or HOME is
// what causes it. There is no second location to fall back to (owner,
// 2026-10-09): the player refuses to start and the commands say why.

import (
	"errors"
	"fmt"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/ipc"
)

// checkSocketPath is run's first step: with a socket path that can never
// work, the player does not start, and nothing has been set up yet.
func checkSocketPath() error {
	return socketPathAdvice(ipc.ValidateSocketPath(ipc.DefaultSocketPath()))
}

// socketPathTooLong reports whether err is, or wraps, an overlong socket path.
func socketPathTooLong(err error) bool {
	var tooLong *ipc.SocketPathError
	return errors.As(err, &tooLong)
}

// socketPathAdvice words an overlong socket path for the user. Any other
// error, and nil, pass through unchanged.
func socketPathAdvice(err error) error {
	var tooLong *ipc.SocketPathError
	if !errors.As(err, &tooLong) {
		return err
	}
	// With no config directory to be found (no HOME), ipc.DefaultSocketPath
	// puts the socket in the temporary directory.
	_, noConfigDir := appdir.Dir()
	return socketPathAdviceError{cause: tooLong, inTempDir: noConfigDir != nil}
}

// socketPathAdviceError is an ipc.SocketPathError as the user reads it: the
// two lengths in bytes, the path, where the path comes from and by how much
// to shorten it.
type socketPathAdviceError struct {
	cause     *ipc.SocketPathError
	inTempDir bool // the path is the temporary-directory fallback
}

func (e socketPathAdviceError) Unwrap() error { return e.cause }

func (e socketPathAdviceError) Error() string {
	over, unit := e.cause.Bytes-e.cause.Max, "bytes"
	if over == 1 {
		unit = "byte"
	}
	where := fmt.Sprintf("The socket is kept in the config directory, which is the first of CLIAMP_CONFIG_DIR, %s, XDG_CONFIG_HOME and HOME that is set. "+
		"Use a directory whose path is at least %d %s shorter.", appdir.ConfigDirEnv, over, unit)
	if e.inTempDir {
		where = fmt.Sprintf("The socket is in the temporary directory (TMPDIR), because no config directory was found: there is no home directory. "+
			"Shorten the temporary directory's path by at least %d %s, or set %s to a short directory.", over, unit, appdir.ConfigDirEnv)
	}
	return fmt.Sprintf("%s cannot use its control socket: the path is %d bytes and this system allows at most %d: %s\n%s",
		appmeta.ClientName(), e.cause.Bytes, e.cause.Max, e.cause.Path, where)
}
