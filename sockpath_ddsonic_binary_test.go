package main

// ddsonic: the socket path's limit, checked on the built executable. Under go
// test DDSONIC_CONFIG_DIR is ignored (appdir.configDirOverride), so only a
// real build exercises the variable a user sets.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// sunPathLimit is the longest socket path the platform takes, derived here
// independently of the code under test: sun_path less its NUL.
const sunPathLimit = len(syscall.RawSockaddrUnix{}.Path) - 1

// builtDdsonic builds the executable into a temporary directory.
func builtDdsonic(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the executable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command to build the executable with")
	}
	bin := filepath.Join(t.TempDir(), appdir.Name)
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// configDirForSocketLen returns a config directory, not yet created, whose
// socket path is exactly n bytes. fill is repeated to pad the name.
func configDirForSocketLen(t *testing.T, n int, fill string) string {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "dd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	pad := n - len(base) - 1 - len("/"+appdir.Name+".sock")
	if pad < len(fill) {
		t.Fatalf("no room for a %d-byte socket path under %s", n, base)
	}
	name := strings.Repeat(fill, pad/len(fill)) + strings.Repeat("x", pad%len(fill))
	dir := filepath.Join(base, name)
	if got := len(filepath.Join(dir, appdir.Name+".sock")); got != n {
		t.Fatalf("built a %d-byte socket path, want %d", got, n)
	}
	return dir
}

// runDdsonic runs the executable with DDSONIC_CONFIG_DIR set and none of the
// other variables the config directory can come from.
func runDdsonic(t *testing.T, bin, configDir string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		appdir.ConfigDirEnv + "=" + configDir,
	}
	cmd.WaitDelay = 5 * time.Second
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("%s %v did not exit within 30s", appdir.Name, args)
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		exit = exitErr.ExitCode()
	default:
		t.Fatalf("%s %v: %v", appdir.Name, args, err)
	}
	return out.String(), errOut.String(), exit
}

func TestBinarySocketPathLimit(t *testing.T) {
	if runtime.GOOS == "linux" && sunPathLimit != 107 {
		t.Fatalf("the limit is %d on Linux, want 107", sunPathLimit)
	}
	bin := builtDdsonic(t)

	// At the limit (107 bytes on Linux) nothing changes: with no player
	// running, a client says so and names the socket in the directory
	// DDSONIC_CONFIG_DIR gave.
	t.Run("at the limit", func(t *testing.T) {
		dir := configDirForSocketLen(t, sunPathLimit, "x")
		_, stderr, exit := runDdsonic(t, bin, dir, "status")
		want := fmt.Sprintf("%s is not running (no socket at %s)", appdir.Name, filepath.Join(dir, appdir.Name+".sock"))
		if exit != 1 || strings.TrimSpace(stderr) != want {
			t.Errorf("status = exit %d, %q; want exit 1, %q", exit, stderr, want)
		}
	})

	// One byte over (108 on Linux): the player does not start, and every
	// command that needs the socket says why.
	over := configDirForSocketLen(t, sunPathLimit+1, "x")
	// Over in bytes while under in characters: the limit counts bytes.
	accented := configDirForSocketLen(t, sunPathLimit+1, "é")
	if n := utf8.RuneCountInString(filepath.Join(accented, appdir.Name+".sock")); n > sunPathLimit {
		t.Fatalf("the accented path has %d characters, over the limit too", n)
	}
	for _, tt := range []struct {
		name string
		dir  string
		args []string
	}{
		{"startup", over, nil},
		{"startup with a file", over, []string{"song.mp3"}},
		{"startup, multi-byte", accented, nil},
		{"status", over, []string{"status"}},
		{"status, multi-byte", accented, []string{"status"}},
		{"pause", over, []string{"pause"}},
		{"theme", over, []string{"theme", "nord"}},
		{"vis list", over, []string{"vis", "list"}},
		{"visstream", over, []string{"visstream"}},
		{"remote state", over, []string{"remote", "state"}},
		{"remote capabilities", over, []string{"remote", "capabilities"}},
		{"remote call", over, []string{"remote", "call", "pause"}},
		{"remote job", over, []string{"remote", "job", "1"}},
		{"remote cancel", over, []string{"remote", "cancel", "1"}},
		{"remote events", over, []string{"remote", "events", "runtime.state"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, exit := runDdsonic(t, bin, tt.dir, tt.args...)
			if exit == 0 {
				t.Errorf("exit 0, want a failure; stderr %q", stderr)
			}
			for _, want := range []string{
				fmt.Sprintf("%d bytes", sunPathLimit+1),
				fmt.Sprintf("at most %d", sunPathLimit),
				filepath.Join(tt.dir, appdir.Name+".sock"),
				appdir.ConfigDirEnv,
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(stderr, "not running") || strings.Contains(stderr, "invalid argument") {
				t.Errorf("stderr is misleading:\n%s", stderr)
			}
			// Nothing was set up on the way to the refusal.
			if _, err := os.Stat(tt.dir); !os.IsNotExist(err) {
				t.Errorf("the config directory exists after the refusal (stat: %v)", err)
			}
		})
	}

	// Commands that never use the socket are left alone.
	t.Run("theme list is unaffected", func(t *testing.T) {
		stdout, stderr, exit := runDdsonic(t, bin, over, "theme", "list")
		if exit != 0 || !strings.Contains(stdout, "nord") {
			t.Errorf("theme list = exit %d, stdout %q, stderr %q", exit, stdout, stderr)
		}
	})
}
