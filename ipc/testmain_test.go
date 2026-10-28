package ipc

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Unsetenv("CLIAMP_CONFIG_DIR")
	os.Unsetenv("XDG_CONFIG_HOME")
	os.Exit(m.Run())
}
