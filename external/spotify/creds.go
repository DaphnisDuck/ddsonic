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
// credential file. ddsonic: under go test it permits only a plain path inside
// the system's temporary directory, and returns that path with the temporary
// directory's own links resolved. A test that has not pointed the config
// directory at a temporary one would otherwise read the user's own stored
// sign-in, and write it; like the token and browser guards
// (session_auth_ddsonic.go), this fails such a test instead, in whichever
// package it is.
//
// Outside go test nothing is checked and the path is CredsPath's.
func credsFile() (string, error) {
	path, err := CredsPath()
	if err != nil {
		return "", err
	}
	if !testing.Testing() {
		return path, nil
	}
	plain, ok := plainTempPath(path)
	if !ok {
		return "", fmt.Errorf("spotify: the credential file is not a plain path inside the test's temporary directory: %w", errNoStandIn)
	}
	return plain, nil
}

// plainTempPath is the test guard's one rule. It makes no attempt to work
// out where a symbolic link leads; a path with one in it is refused. path is
// accepted when all of this holds:
//
//   - as spelled, it lies inside the temporary directory, in a directory
//     below it (the config directory is never the temporary directory
//     itself, which a save would otherwise re-mode);
//   - no component below the temporary directory is a symbolic link, the
//     credential file's own entry included;
//   - every component that exists and is not the last is a directory, and the
//     last, if it exists, is a regular file;
//   - nothing in it fails to be examined for a reason other than not existing.
//
// The temporary directory itself may be reached through links (macOS's is);
// it is resolved once, and the path returned is spelled from that resolved
// directory. So what a caller is given has no link in it anywhere, and each
// thing done with it touches only that path and its directory: the read, the
// directory an atomic save creates and re-modes, the temporary file it writes
// beside the destination, the rename, and a reset's removal of the entry.
// There is no second location whose order of resolution could matter.
//
// It is a check, not a sandbox: it assumes the test owns that directory and
// nothing re-links it between the check and the use. An entry that is a
// regular file with another hard link passes, and a load then reads an inode
// shared with wherever that link is. A bind or shared mount is not seen as a
// boundary. It covers Spotify's credential file only, and has been run on
// Linux only.
func plainTempPath(path string) (string, bool) {
	root, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return "", false
	}
	// A relative temporary directory resolves to a relative path.
	if root, err = filepath.Abs(root); err != nil {
		return "", false
	}
	spelled, err := filepath.Abs(os.TempDir())
	if err != nil {
		return "", false
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, ok := relInside(spelled, path)
	if !ok {
		if rel, ok = relInside(root, path); !ok {
			return "", false
		}
	}
	names := strings.Split(rel, string(filepath.Separator))
	if len(names) < 2 {
		return "", false
	}
	cur := root
	for i, name := range names {
		cur = filepath.Join(cur, name)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			break // nothing below a missing name exists either
		}
		last := i == len(names)-1
		if err != nil || (last && !info.Mode().IsRegular()) || (!last && !info.IsDir()) {
			return "", false // a link is neither a directory nor a regular file
		}
	}
	return filepath.Join(root, rel), true
}

// relInside is path relative to dir when path, as spelled, is strictly
// inside dir.
func relInside(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
