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
// credential file. ddsonic: under go test it refuses a path that leads
// outside the system's temporary directory. A test that has not pointed the
// config directory at a temporary one would otherwise read the user's own
// stored sign-in, and write it; like the token and browser guards
// (session_auth_ddsonic.go), this fails such a test instead, in whichever
// package it is.
//
// The path is judged by where it leads, not by how it is spelled: symbolic
// links in it are followed, the credential file's own included, whether or
// not anything exists at their end. Outside go test nothing is checked and
// links are followed as they always were.
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

// underTempDir reports whether path, with every symbolic link in it
// followed, lies inside the temporary directory, itself resolved. A path
// that cannot be resolved is not inside.
func underTempDir(path string) bool {
	root, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return false
	}
	resolved, ok := resolvePath(path)
	if !ok {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolvePath is where path leads: the longest part of it that exists, with
// its links followed, and the rest appended. A final link is followed even
// when it leads nowhere yet, since a write would go there.
func resolvePath(path string) (string, bool) {
	p, rest := filepath.Clean(path), ""
	for range 64 { // links followed; a loop of them is not resolved
		info, err := os.Lstat(p)
		switch {
		case err == nil && info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return "", false
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(p), target)
			}
			p = filepath.Clean(target)
		case err == nil:
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				return "", false
			}
			return filepath.Join(resolved, rest), true
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(p)
			if parent == p {
				return "", false
			}
			p, rest = parent, filepath.Join(filepath.Base(p), rest)
		default:
			return "", false
		}
	}
	return "", false
}
