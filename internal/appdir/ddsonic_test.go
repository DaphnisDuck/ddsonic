package appdir

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tt := range []struct{ xdg, want string }{
		{"/xdg/cache", "/xdg/cache/ddsonic"},
		{"", filepath.Join(home, ".cache", "ddsonic")},
		{"relative", filepath.Join(home, ".cache", "ddsonic")}, // XDG says to ignore relative paths
	} {
		t.Setenv("XDG_CACHE_HOME", tt.xdg)
		if got, err := CacheDir(); err != nil || got != tt.want {
			t.Errorf("XDG_CACHE_HOME=%q: CacheDir = %q, %v; want %q", tt.xdg, got, err, tt.want)
		}
	}
}

// TestOldDdmusFoldersAreNotUsed: up to v1.0.0-rc.1 the folders were named
// ddmus. ddsonic neither reads nor moves them (docs/ddsonic/files.md gives
// the hand move), so with only those present every path is still ddsonic's
// and the old folders are left as they were.
func TestOldDdmusFoldersAreNotUsed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	old := []string{
		filepath.Join(home, ".config", "ddmus", "config.toml"),
		filepath.Join(home, ".local", "share", "ddmus", "library.db"),
		filepath.Join(home, ".cache", "ddmus", "artwork", "a.jpg"),
		filepath.Join(home, "Music", "ddmus", "song.mp3"),
	}
	for _, p := range old {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"Dir", Dir, filepath.Join(home, ".config", "ddsonic")},
		{"DataDir", DataDir, filepath.Join(home, ".local", "share", "ddsonic")},
		{"CacheDir", CacheDir, filepath.Join(home, ".cache", "ddsonic")},
		{"DownloadsDir", DownloadsDir, filepath.Join(home, "Music", "ddsonic")},
		{"LibraryDBPath", LibraryDBPath, filepath.Join(home, ".local", "share", "ddsonic", "library.db")},
	} {
		if got, err := tt.fn(); err != nil || got != tt.want {
			t.Errorf("%s = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	for _, p := range old {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("the old file %s must be left in place: %v", p, err)
		}
	}
}

// ConfigDirEnv is built from Name; its spelling is what users export.
func TestConfigDirEnvName(t *testing.T) {
	if ConfigDirEnv != "DDSONIC_CONFIG_DIR" {
		t.Errorf("ConfigDirEnv = %q, want DDSONIC_CONFIG_DIR", ConfigDirEnv)
	}
}
