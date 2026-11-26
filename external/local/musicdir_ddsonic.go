package local

// ddsonic: the folder Local's Albums, Artists and Genres are indexed from,
// shared by the player and setup.

import (
	"os"
	"path/filepath"
	"strings"
)

// MusicDir is the configured initial_directory, else $XDG_MUSIC_DIR, else
// ~/Music.
func MusicDir(initialDir string) string {
	dir, _ := ResolveMusicDir(initialDir)
	return dir
}

// ResolveMusicDir is MusicDir, and whether it is the implicit ~/Music:
// neither initial_directory nor $XDG_MUSIC_DIR named a folder.
func ResolveMusicDir(initialDir string) (dir string, implicit bool) {
	if dir := ExpandPath(strings.TrimSpace(initialDir)); dir != "" {
		return dir, false
	}
	if dir := ExpandPath(os.Getenv("XDG_MUSIC_DIR")); dir != "" {
		return dir, false
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Music"), true
	}
	return "", false
}
