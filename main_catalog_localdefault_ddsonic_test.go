package main

// ddsonic: openCatalog tells Local's index whether its folder is the
// implicit ~/Music (#82).

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/config"
)

// isolatedHome is a home with no ~/Music and nothing set that names
// another music folder or catalog.
func isolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_MUSIC_DIR", "")
	t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(home, "config"))
	return home
}

// syncLocal opens the catalog with cfg, indexes Local once and returns
// Local's stored error after a restart and the sync's error.
func syncLocal(t *testing.T, cfg config.Config) (string, error) {
	t.Helper()
	rt := openCatalog(nil, nil, cfg)
	if rt.engine == nil {
		t.Fatal("the catalog did not open")
	}
	err := rt.engine.Sync(context.Background(), catalog.Local)
	rt.close()
	again := openCatalog(nil, nil, cfg)
	defer again.close()
	ps := again.providers[catalog.Local]
	if ps == nil {
		t.Fatal("Local is not indexed")
	}
	if err == nil && ps.startup.LastSuccess.IsZero() {
		t.Errorf("a successful index left no success: %+v", ps.startup)
	}
	return ps.startup.LastError, err
}

func TestLocalIndexOfTheDefaultMusicFolder(t *testing.T) {
	t.Run("implicit ~/Music, never created", func(t *testing.T) {
		home := isolatedHome(t)
		for run := 1; run <= 2; run++ {
			lastErr, err := syncLocal(t, config.Config{})
			if err != nil || lastErr != "" {
				t.Fatalf("run %d: sync = %v, stored error %q; want an empty library", run, err, lastErr)
			}
		}
		if _, err := os.Stat(filepath.Join(home, "Music")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("~/Music: stat = %v, want it not created", err)
		}
	})
	t.Run("configured initial_directory, missing", func(t *testing.T) {
		home := isolatedHome(t)
		lastErr, err := syncLocal(t, config.Config{InitialDirectory: filepath.Join(home, "Music")})
		if err == nil || lastErr == "" {
			t.Errorf("sync = %v, stored error %q; want the failure kept", err, lastErr)
		}
	})
	t.Run("XDG_MUSIC_DIR, missing", func(t *testing.T) {
		home := isolatedHome(t)
		t.Setenv("XDG_MUSIC_DIR", filepath.Join(home, "Music"))
		lastErr, err := syncLocal(t, config.Config{})
		if err == nil || lastErr == "" {
			t.Errorf("sync = %v, stored error %q; want the failure kept", err, lastErr)
		}
	})
}
