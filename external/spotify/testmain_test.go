package spotify

import (
	"fmt"
	"os"
	"testing"
)

// ddsonic: no test of this package may resolve the user's own config
// directory. The overrides are unset, as upstream has them (its tests set
// HOME themselves), and HOME is a temporary directory for the whole run, so
// a test that sets nothing gets an empty config directory, not the user's
// stored sign-in. The credential file's own guard (credsFile) refuses a path
// outside the temporary directory as well.
func TestMain(m *testing.M) {
	os.Unsetenv("CLIAMP_CONFIG_DIR")
	os.Unsetenv("DDSONIC_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	home, err := os.MkdirTemp("", "ddsonic-spotify-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "test home:", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
