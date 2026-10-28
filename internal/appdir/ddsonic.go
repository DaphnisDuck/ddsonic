// ddsonic: the fork's own on-disk identity, so ddsonic and cliamp keep separate
// files and can run side by side. See docs/ddsonic/files.md.

package appdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Name is the short name of DaphnisDuck's Music Player, and the one place it
// is defined: its directories, binary, config section and variable, and the
// name it gives servers, MPRIS and the IPC socket all derive from it.
// TestProductNameIsWrittenInOnePlace (identity_ddsonic_test.go) keeps it so.
const Name = "ddsonic"

// ConfigDirEnv is the variable that overrides the config directory for
// ddsonic alone: DDSONIC_CONFIG_DIR, the name in upper case.
var ConfigDirEnv = strings.ToUpper(Name) + "_CONFIG_DIR"

// configDirOverride returns DDSONIC_CONFIG_DIR, or "" under go test.
// Upstream's tests isolate themselves with CLIAMP_CONFIG_DIR or a temporary
// HOME and know nothing of this variable, so a developer who exports it must
// not send those tests to the real config. Dir checks CLIAMP_CONFIG_DIR first
// for the same reason.
func configDirOverride() string {
	if testing.Testing() {
		return ""
	}
	return os.Getenv(ConfigDirEnv)
}

// LibraryDBPath is the catalog database, library.db in DataDir
// (~/.local/share/ddsonic).
func LibraryDBPath() (string, error) {
	dir, err := DataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "library.db"), nil
}

// DownloadsDir is the default directory for saved tracks (~/Music/ddsonic).
func DownloadsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Music", Name), nil
}

// CacheDir is the directory for files ddsonic can fetch again, such as album
// artwork: $XDG_CACHE_HOME/ddsonic, else ~/.cache/ddsonic.
func CacheDir() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return filepath.Join(dir, Name), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", Name), nil
}
