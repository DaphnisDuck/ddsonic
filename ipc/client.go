package ipc

import (
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// DefaultSocketPath returns the default IPC socket path (ddsonic.sock in the app config directory).
func DefaultSocketPath() string {
	dir, err := appdir.Dir()
	if err != nil {
		return filepath.Join(os.TempDir(), appdir.Name+".sock") // ddsonic
	}
	return filepath.Join(dir, appdir.Name+".sock") // ddsonic: ddsonic.sock
}
