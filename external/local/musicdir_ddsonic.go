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
	if dir := ExpandPath(strings.TrimSpace(initialDir)); dir != "" {
		return dir
	}
	if dir := ExpandPath(os.Getenv("XDG_MUSIC_DIR")); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Music")
	}
	return ""
}
