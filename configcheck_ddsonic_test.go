package main

// ddsonic: a malformed config.toml stops the player's start and nothing
// else. The executable itself is covered by the acceptance case CFG-002
// (acceptance/binarycases).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// shortConfigDir is a config directory whose socket path fits, whatever
// TMPDIR is.
func shortConfigDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": dir})
	return dir
}

func TestRunRefusesAMalformedConfig(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string
	}{
		{"an unclosed section", "volume = 0\n[spotify\nbitrate = 320\n", []string{":3:", "to end table name", "line 3 (or just before"}}, // the parser notices it on the next line
		{"an unclosed string", "[spotify]\nclient_id = \"oops\n", []string{":2:", "strings cannot contain newlines", "line 2"}},
		{"a line that is no setting", "this is not toml\n", []string{":1:", "expected '.' or '='"}},
		{"a missing value", "volume =\n", []string{":1:", "expected value"}},
		{"a bare word for a string", "volume = 0\ntheme = Tokyo Night\n", []string{":2:", "expected value", "line 2"}},
		{"a key given twice", "volume = 1\nvolume = 2\n", []string{":2:", "already been defined"}},
		{"an invalid number", "volume = 01\n", []string{":1:", "Invalid integer"}},
		// The one tolerated legacy line does not excuse anything beside it,
		// nor itself in another shape.
		{"an error beside the legacy device line", "audio_device = hw0\n[spotify\n", []string{":3:", "to end table name"}},
		{"a device line that is not the legacy shape", "audio_device = \"hw0\n", []string{":1:", "strings cannot contain newlines"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := shortConfigDir(t)
			path := filepath.Join(dir, "config.toml")
			if err := os.WriteFile(path, []byte(tt.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			err := run(config.Overrides{}, nil, false, false)
			var syntax *config.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("run = %v, want the config refused", err)
			}
			got := err.Error()
			for _, want := range append(tt.want, path, "did not start", "was not changed") {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal lacks %q:\n%s", want, got)
				}
			}
			// Left exactly as it was, and nothing set up beside it.
			if data, _ := os.ReadFile(path); string(data) != tt.toml {
				t.Errorf("the malformed file was changed to %q", data)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 1 {
				t.Errorf("the config directory holds %d entries after the refusal, want the one file", len(entries))
			}
		})
	}
}

// No config file, a well-formed one, and one with the device line older
// versions wrote all pass the check: the player goes on to start. (It is not
// started here; the acceptance cases START-001 and CFG-002 do that.)
func TestStartupChecksPassWithoutOrWithAGoodConfig(t *testing.T) {
	dir := shortConfigDir(t)
	path := filepath.Join(dir, "config.toml")
	if err := startupChecks(); err != nil {
		t.Fatalf("with no config file: %v", err)
	}
	// Unknown keys and sections are not this check's business. The comment
	// after [spotify] is valid TOML that Load does not read as a section: an
	// existing limit of Load, and no reason to refuse the file.
	good := "# mine\nvolume = -3\nflux_capacitor = true\n\n[spotify] # on\nbitrate = 320\n\n[nobody.knows]\nx = [1, 2]\n"
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startupChecks(); err != nil {
		t.Fatalf("with a well-formed config, unknown keys and sections included: %v", err)
	}
	if cfg, err := config.Load(); err != nil || cfg.Volume != -3 {
		t.Errorf("Load = volume %v, err %v: a valid config must load as before", cfg.Volume, err)
	}

	legacy := "volume = -6\naudio_device = alsa_output.pci-0000_00_1f.3.analog-stereo\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := startupChecks(); err != nil {
		t.Fatalf("with the device line older versions wrote: %v", err)
	}
	cfg, err := config.Load()
	if err != nil || cfg.AudioDevice != "alsa_output.pci-0000_00_1f.3.analog-stereo" || cfg.Volume != -6 {
		t.Errorf("Load = device %q, volume %v, err %v", cfg.AudioDevice, cfg.Volume, err)
	}
	// Reading did not rewrite the file.
	if data, _ := os.ReadFile(path); string(data) != legacy {
		t.Errorf("the legacy file was changed by being read: %q", data)
	}
	// The next save of a device writes it quoted, and the file is then TOML.
	if err := config.Save("audio_device", "hw:1,0"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "audio_device = \"hw:1,0\"") {
		t.Errorf("the saved device is not quoted: %q", data)
	}
	if err := startupChecks(); err != nil {
		t.Errorf("after the save: %v", err)
	}
}

// The remote commands do not read the config: a malformed one does not
// change what they say.
func TestCommandsIgnoreAMalformedConfig(t *testing.T) {
	dir := shortConfigDir(t)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ipcState()
	if err == nil || !strings.Contains(err.Error(), "is not running (no socket at ") {
		t.Errorf("status with a malformed config = %v, want not running", err)
	}
}
