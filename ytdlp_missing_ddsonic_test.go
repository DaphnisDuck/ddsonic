package main

// ddsonic: with yt-dlp missing, ddsonic prints how to install it and runs
// nothing to install it, on every path that turns YouTube on, and even when
// its input is at end of file, where the old prompt ran the installer
// without a keypress (#82).

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// installers are the commands the old prompt could run, directly or through
// sudo.
var installers = []string{"sudo", "apt-get", "apt", "pacman", "brew", "pipx", "pip3", "pip", "winget"}

// fakeInstallers returns a directory of stand-ins for installers that each
// note their run in the returned log, and nothing else; yt-dlp is not among
// them.
func fakeInstallers(t *testing.T) (bin, log string) {
	t.Helper()
	bin = t.TempDir()
	log = filepath.Join(t.TempDir(), "ran")
	for _, name := range installers {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> '" + log + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, log
}

// assertNothingRan fails if any stand-in installer ran.
func assertNothingRan(t *testing.T, log string) {
	t.Helper()
	if ran, err := os.ReadFile(log); err == nil {
		t.Errorf("an installer ran:\n%s", ran)
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestYtdlpReadyOnlyInstructs(t *testing.T) {
	bin, log := fakeInstallers(t)
	t.Setenv("PATH", bin)
	var out bytes.Buffer
	if ytdlpReady(&out) {
		t.Fatal("ytdlpReady = true with no yt-dlp on PATH")
	}
	for _, want := range []string{"YouTube needs yt-dlp", "install yt-dlp", "https://github.com/yt-dlp/yt-dlp#installation", "restart"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("notice %q lacks %q", out.String(), want)
		}
	}
	assertNothingRan(t, log)
}

// Each way YouTube is turned on, run on the built executable with stdin at
// end of file and only the stand-in installers on PATH: as shipped, with no
// built-in credentials, and built with a pool as upstream's, where every
// way reaches the yt-dlp check.
func TestBinaryNeverInstallsYtdlp(t *testing.T) {
	shipped := builtDdsonic(t)
	pooled := filepath.Join(t.TempDir(), appdir.Name)
	if out, err := exec.Command("go", "build", "-tags", "ddsonic_testpool", "-o", pooled, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build -tags ddsonic_testpool: %v\n%s", err, out)
	}
	for _, tt := range []struct {
		name, config string
		ownCreds     bool // reaches the check with no built-in credentials; as shipped the rest leave YouTube off
	}{
		{"built-in credentials, no config", "", false},
		{"default provider ytmusic", "provider = \"ytmusic\"\n", false},
		{"default provider yt", "provider = \"yt\"\n", false},
		{"default provider youtube", "provider = \"youtube\"\n", false},
		{"[ytmusic] enabled", "[ytmusic]\nenabled = true\n", false},
		{"[youtube] section", "[youtube]\nenabled = true\n", false},
		{"own client ID", "[ytmusic]\nclient_id = \"id\"\nclient_secret = \"secret\"\n", true},
		{"browser cookies", "[ytmusic]\ncookies_from = \"firefox\"\n", true},
	} {
		for _, build := range []struct {
			name, bin string
			pool      bool
		}{{"shipped", shipped, false}, {"with a pool", pooled, true}} {
			t.Run(tt.name+", "+build.name, func(t *testing.T) {
				stderr, log := runWithoutYtdlp(t, build.bin, tt.config)
				if strings.Contains(stderr, "control socket") {
					t.Fatalf("ddsonic stopped before the yt-dlp check:\n%s", stderr)
				}
				if tt.ownCreds || build.pool {
					if !strings.Contains(stderr, "YouTube needs yt-dlp") {
						t.Errorf("no install instructions on stderr:\n%s", stderr)
					}
				} else if strings.Contains(stderr, "YouTube needs yt-dlp") {
					t.Errorf("YouTube turned on with no credentials:\n%s", stderr)
				}
				for _, gone := range []string{"Press Enter", "Installing yt-dlp", "installed successfully"} {
					if strings.Contains(stderr, gone) {
						t.Errorf("stderr still has %q:\n%s", gone, stderr)
					}
				}
				assertNothingRan(t, log)
			})
		}
	}
}

// runWithoutYtdlp runs ddsonic with config, stdin at end of file and only
// the stand-in installers on PATH, and returns its stderr and the installers'
// log. Without a terminal or audio the player does not get far; only what
// comes before matters.
func runWithoutYtdlp(t *testing.T, ddsonic, config string) (stderr, log string) {
	t.Helper()
	bin, log := fakeInstallers(t)
	// The config directory holds the control socket, so its path must be
	// short: macOS's t.TempDir (under /var/folders, with the test's name) is
	// past the socket limit, and ddsonic would stop before the yt-dlp check.
	dir, err := os.MkdirTemp("/tmp", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ddsonic)
	cmd.Env = []string{"PATH=" + bin, "HOME=" + t.TempDir(), appdir.ConfigDirEnv + "=" + dir}
	cmd.Stdin = strings.NewReader("") // end of file at once
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	cmd.WaitDelay = 5 * time.Second
	_ = cmd.Run()
	return errOut.String(), log
}
