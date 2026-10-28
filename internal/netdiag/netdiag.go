// Package netdiag decorates network errors with platform-specific hints.
package netdiag

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"syscall"
)

// Explain wraps err with a remediation hint when the failure matches a known
// platform quirk. It currently detects macOS's Local Network privacy denial:
// when the app (or the terminal it runs in) lacks the Local Network
// permission, connections to LAN addresses fail with EHOSTUNREACH without any
// packet being sent, which is indistinguishable from a routing problem.
// All other errors are returned unchanged.
func Explain(err error) error {
	return explain(runtime.GOOS, err)
}

func explain(goos string, err error) error {
	if err == nil || goos != "darwin" {
		return err
	}
	if !errors.Is(err, syscall.EHOSTUNREACH) {
		return err
	}
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return err
	}
	addr, ok := opErr.Addr.(*net.TCPAddr)
	if !ok || addr == nil || !isLocalNetwork(addr.IP) {
		return err
	}
	return fmt.Errorf("%w (macOS may be denying Local Network access: open System Settings > Privacy & Security > Local Network, enable it for your terminal app, then restart cliamp)", err)
}

// isLocalNetwork reports whether ip falls in a range covered by macOS's Local
// Network permission. Loopback is excluded: it is always allowed by TCC.
func isLocalNetwork(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}
