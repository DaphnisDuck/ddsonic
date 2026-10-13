package spotify

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// DefaultClientID is the librespot keymaster client_id, shared by spotify-player
// and other librespot-based players. Used when the user hasn't configured their
// own client_id — Spotify's loopback exception lets it work with any 127.0.0.1
// port, and it predates the Nov 27, 2024 dev-mode quota restriction so /v1/search
// and other catalog endpoints stay accessible.
const DefaultClientID = "65b708073fc0480ea92a077233ca87bd"

// CredsPath returns the absolute path to the stored Spotify credentials file.
func CredsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "spotify_credentials.json"), nil
}

// DeleteCreds removes the stored Spotify credentials file.
// Returns true if a file was removed, false if it did not exist.
func DeleteCreds() (bool, error) {
	path, err := credsFile() // ddsonic: refuses the real file under go test
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// credsFile is CredsPath for the code that reads, writes or deletes the
// credential file. ddsonic: under go test it refuses a path outside the
// system's temporary directory. A test that has not pointed the config
// directory at a temporary one would otherwise read the user's own stored
// sign-in, and write it; like the token and browser guards
// (session_auth_ddsonic.go), this fails such a test instead, in whichever
// package it is.
func credsFile() (string, error) {
	path, err := CredsPath()
	if err != nil {
		return "", err
	}
	if testing.Testing() && !underTempDir(path) {
		return "", fmt.Errorf("spotify: the credential file is outside the test's temporary directory: %w", errNoStandIn)
	}
	return path, nil
}

func underTempDir(path string) bool {
	rel, err := filepath.Rel(os.TempDir(), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
