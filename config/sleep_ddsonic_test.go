package config

// ddsonic: [ddsonic] sleep_fade_seconds, the sleep timer's fade-out (#35).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSleepFade(t *testing.T) {
	for _, tt := range []struct {
		name string
		toml string
		want time.Duration
	}{
		{"30 s without the section", "", 30 * time.Second},
		{"30 s without the key", "[ddsonic]\nborder = true\n", 30 * time.Second},
		{"set", "[ddsonic]\nsleep_fade_seconds = 10\n", 10 * time.Second},
		{"0 turns it off", "[ddsonic]\nsleep_fade_seconds = 0\n", 0},
		{"negative keeps the default", "[ddsonic]\nsleep_fade_seconds = -5\n", 30 * time.Second},
		{"not a number keeps the default", "[ddsonic]\nsleep_fade_seconds = \"long\"\n", 30 * time.Second},
		{"a duration string is not seconds", "[ddsonic]\nsleep_fade_seconds = \"10s\"\n", 30 * time.Second},
		{"not from another section", "sleep_fade_seconds = 5\n", 30 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Ddsonic.SleepFade != tt.want {
				t.Errorf("SleepFade = %v, want %v", cfg.Ddsonic.SleepFade, tt.want)
			}
		})
	}
}
