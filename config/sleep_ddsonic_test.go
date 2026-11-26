package config

// ddsonic: [ddsonic] sleep_fade_seconds, the sleep timer's fade-out (#35),
// and sleep_timer_presets_minutes, its presets (#204).

import (
	"os"
	"path/filepath"
	"slices"
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
		{"the maximum", "[ddsonic]\nsleep_fade_seconds = 3600\n", time.Hour},
		{"one over the maximum keeps the default", "[ddsonic]\nsleep_fade_seconds = 3601\n", 30 * time.Second},
		{"9223372036 keeps the default", "[ddsonic]\nsleep_fade_seconds = 9223372036\n", 30 * time.Second},
		{"9223372037 (overflows a Duration) keeps the default", "[ddsonic]\nsleep_fade_seconds = 9223372037\n", 30 * time.Second},
		{"huge keeps the default", "[ddsonic]\nsleep_fade_seconds = 99999999999999999999999\n", 30 * time.Second},
		{"not a number keeps the default", "[ddsonic]\nsleep_fade_seconds = \"long\"\n", 30 * time.Second},
		{"a duration string is not seconds", "[ddsonic]\nsleep_fade_seconds = \"10s\"\n", 30 * time.Second},
		{"not from another section", "sleep_fade_seconds = 5\n", 30 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := loadSleepTOML(t, tt.toml)
			if cfg.Ddsonic.SleepFade != tt.want {
				t.Errorf("SleepFade = %v, want %v", cfg.Ddsonic.SleepFade, tt.want)
			}
		})
	}
}

func TestLoadSleepPresets(t *testing.T) {
	m := time.Minute
	for _, tt := range []struct {
		name string
		val  string // "" leaves the key out
		want []time.Duration
	}{
		{"absent keeps the built-in", "", nil},
		{"one minute and up", "[1, 2, 5]", []time.Duration{m, 2 * m, 5 * m}},
		{"a trailing comma", "[15, 30,]", []time.Duration{15 * m, 30 * m}},
		{"the maximum", "[1440]", []time.Duration{1440 * m}},
		{"empty keeps the default", "[]", nil},
		{"zero keeps the default", "[0, 5]", nil},
		{"over a day keeps the default", "[1441]", nil},
		{"unsorted keeps the default", "[5, 2]", nil},
		{"a duplicate keeps the default", "[5, 5]", nil},
		{"not a number keeps the default", "[1, \"2m\"]", nil},
		{"not an array keeps the default", "15", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			toml := "[ddsonic]\n"
			if tt.val != "" {
				toml += "sleep_timer_presets_minutes = " + tt.val + "\n"
			}
			cfg := loadSleepTOML(t, toml)
			if !slices.Equal(cfg.Ddsonic.SleepPresets, tt.want) {
				t.Errorf("SleepPresets = %v, want %v", cfg.Ddsonic.SleepPresets, tt.want)
			}
		})
	}
}

// loadSleepTOML loads toml as the config file of a fresh HOME.
func loadSleepTOML(t *testing.T, toml string) Config {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(os.Getenv("HOME"), ".config", "ddsonic", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
