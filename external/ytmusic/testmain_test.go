package ytmusic

import (
	"fmt"
	"os"
	"testing"
)

// ddsonic: no test of this package may resolve the user's own config
// directory, where the stored YouTube Music sign-in is. The overrides are
// unset, as upstream has them, and HOME is a temporary directory for the
// whole run.
func TestMain(m *testing.M) {
	os.Unsetenv("CLIAMP_CONFIG_DIR")
	os.Unsetenv("DDSONIC_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	home, err := os.MkdirTemp("", "ddsonic-ytmusic-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
