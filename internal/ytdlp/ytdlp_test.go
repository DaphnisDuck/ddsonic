package ytdlp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestArgsKeepsTheCallersArgumentsAfterItsOwn(t *testing.T) {
	got := ytdlp.Args("-j", "--", "https://example.invalid/v")
	want := []string{"--ignore-config", "--no-mark-watched", "-j", "--", "https://example.invalid/v"}
	if !slices.Equal(got, want) {
		t.Errorf("Args = %q, want %q", got, want)
	}
}

// The fake has to be steered by the ambient configuration when nothing stops
// it, or a call site passing the tests built on it would show nothing.
func TestFakeTakesUpAmbientConfigurationUnlessArgsStopIt(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantMarked bool
	}{
		{"bare arguments", []string{"-j", "--", "https://example.invalid/v"}, true},
		{"--no-mark-watched alone still reads the configuration", []string{"--no-mark-watched", "--", "https://example.invalid/v"}, false},
		{"Args", ytdlp.Args("-j", "--", "https://example.invalid/v"), false},
		{"--ignore-config after -- is an address, not an option", []string{"--", "--ignore-config"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.Install(t, "")
			if err := exec.Command("yt-dlp", tt.args...).Run(); err != nil {
				t.Fatal(err)
			}
			wantAmbient := tt.name != "Args"
			if got := fake.Ambient(t) != ""; got != wantAmbient {
				t.Errorf("took up ambient options = %v, want %v", got, wantAmbient)
			}
			if got := fake.Marked(t); got != tt.wantMarked {
				t.Errorf("marked watched = %v, want %v", got, tt.wantMarked)
			}
		})
	}
}

// The yt-dlp that is installed, asked for nothing, must not read a user
// configuration or a yt-dlp.conf in the working directory when given Args.
// An option yt-dlp does not know makes it say so as soon as it reads the
// file, before it would reach the network.
func TestInstalledYTDLPDoesNotReadConfigurationGivenArgs(t *testing.T) {
	bin, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp is not installed")
	}
	const unknown = "--ddsonic-ambient-option"
	for _, place := range []string{"user configuration", "working directory"} {
		t.Run(place, func(t *testing.T) {
			xdg, cwd := t.TempDir(), t.TempDir()
			path := filepath.Join(cwd, "yt-dlp.conf")
			if place == "user configuration" {
				path = filepath.Join(xdg, "yt-dlp", "config")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(unknown+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			run := func(args ...string) string {
				cmd := exec.Command(bin, args...)
				cmd.Dir = cwd
				cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+xdg)
				out, _ := cmd.CombinedOutput() // no address given: it exits non-zero either way
				return string(out)
			}
			if out := run(); !strings.Contains(out, unknown) {
				t.Skipf("this yt-dlp does not read %s: %s", path, out)
			}
			if out := run(ytdlp.Args()...); strings.Contains(out, unknown) {
				t.Errorf("yt-dlp read %s despite Args: %s", path, out)
			}
		})
	}
}
