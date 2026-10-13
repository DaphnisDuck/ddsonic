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

// maxLinks is how many symbolic links one path may lead through, the
// kernel's own bound; a loop of links runs into it.
const maxLinks = 40

// resolvePath is where path leads, read the way the filesystem reads it: one
// component at a time, each link followed where it stands, and a ".." taken
// from the directory the links before it led to, never from the spelling.
// What does not exist yet is appended as it is spelled, so a final link is
// followed even when it leads nowhere yet, since a write would go there. A
// path the filesystem itself would not resolve is not resolved here: a loop
// of links, a ".." after a name that does not exist, a component that cannot
// be examined.
func resolvePath(path string) (string, bool) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	sep := string(filepath.Separator)
	rootOf := func(p string) string { return filepath.VolumeName(p) + sep }
	split := func(p string) []string { return strings.Split(strings.TrimPrefix(p, filepath.VolumeName(p)), sep) }

	// resolved has no link, "." or ".." in it; pending is still to be read.
	resolved, pending := rootOf(path), split(path)
	links, missing := 0, false
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		switch name {
		case "", ".":
			continue
		case "..":
			if missing {
				return "", false
			}
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, name)
		if missing {
			resolved = next
			continue
		}
		info, err := os.Lstat(next)
		switch {
		case errors.Is(err, os.ErrNotExist):
			resolved, missing = next, true
		case err != nil:
			return "", false
		case info.Mode()&os.ModeSymlink != 0:
			if links++; links > maxLinks {
				return "", false
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", false
			}
			if filepath.IsAbs(target) {
				resolved = rootOf(target)
			}
			pending = append(split(target), pending...)
		default:
			resolved = next
		}
	}
	return resolved, true
}
