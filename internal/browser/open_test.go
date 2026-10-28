package browser

import (
	"runtime"
	"testing"
)

// TestOpenWithUnreachablePATH confirms Open returns a "not found" error when
// xdg-open / open / rundll32 cannot be located. We set PATH to an empty
// temp dir so the call can't actually spawn a browser — otherwise running
// the tests would pop open a real URL in the user's desktop browser.
func TestOpenWithUnreachablePATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no executables here
	err := Open("about:blank")
	if err == nil {
		t.Error("Open should error when the dispatcher binary is not on PATH")
	}
}

// TestOpenUnsupportedPlatform verifies the default case. We can only reach
// it on non-Linux/Darwin/Windows hosts; elsewhere we skip rather than try
// to duplicate the switch body.
func TestOpenUnsupportedPlatform(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		t.Skipf("host is %s, cannot exercise unsupported-platform branch without build tags", runtime.GOOS)
	}
	err := Open("about:blank")
	if err == nil {
		t.Error("Open on unsupported platform should return error")
	}
}
