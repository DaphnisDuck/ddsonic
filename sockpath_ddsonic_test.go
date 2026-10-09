package main

// ddsonic: an IPC socket path that is too long stops the player from
// starting and is explained by the commands, whichever variable made it long.
// sockpath_ddsonic_binary_test.go covers DDSONIC_CONFIG_DIR itself, which go
// test ignores.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/ipc"
)

const socketFile = appdir.Name + ".sock"

// fromConfigDir is what the refusal says when the socket's directory came
// from one of the config directory's variables.
var fromConfigDir = []string{"config directory", "CLIAMP_CONFIG_DIR", appdir.ConfigDirEnv, "XDG_CONFIG_HOME", "HOME", "shorter"}

// setConfigDirEnv sets every variable the socket's directory can come from;
// the ones not named in env are emptied.
func setConfigDirEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, name := range []string{"CLIAMP_CONFIG_DIR", "XDG_CONFIG_HOME", "HOME", "TMPDIR"} {
		t.Setenv(name, env[name])
	}
}

func TestSocketPathAdvice(t *testing.T) {
	if err := socketPathAdvice(nil); err != nil {
		t.Errorf("socketPathAdvice(nil) = %v", err)
	}
	other := errors.New("connect: permission denied")
	if err := socketPathAdvice(other); err != other {
		t.Errorf("socketPathAdvice changed an unrelated error: %v", err)
	}
	if socketPathTooLong(other) || socketPathTooLong(ipc.ErrNotRunning) || socketPathTooLong(nil) {
		t.Error("socketPathTooLong is true for an unrelated error")
	}

	path := "/" + strings.Repeat("x", ipc.MaxSocketPathBytes+4)
	cause := ipc.ValidateSocketPath(path)
	// Clients return it wrapped ("connect: ..."), and through userIPCError.
	for name, err := range map[string]error{
		"socketPathAdvice": socketPathAdvice(fmt.Errorf("connect: %w", cause)),
		"userIPCError":     userIPCError(fmt.Errorf("connect: %w", cause)),
	} {
		if !socketPathTooLong(err) {
			t.Fatalf("%s lost the cause: %v", name, err)
		}
		got := err.Error()
		for _, want := range []string{
			fmt.Sprintf("%d bytes", ipc.MaxSocketPathBytes+5),
			fmt.Sprintf("at most %d", ipc.MaxSocketPathBytes),
			path,
			"CLIAMP_CONFIG_DIR", appdir.ConfigDirEnv, "XDG_CONFIG_HOME", "HOME",
			"at least 5 bytes shorter",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s lacks %q:\n%s", name, want, got)
			}
		}
		if strings.Contains(got, "not running") || strings.HasPrefix(got, "connect:") {
			t.Errorf("%s is misleading:\n%s", name, got)
		}
	}
	one := socketPathAdvice(ipc.ValidateSocketPath(path[:ipc.MaxSocketPathBytes+1])).Error()
	if !strings.Contains(one, "at least 1 byte shorter") {
		t.Errorf("one byte over reads:\n%s", one)
	}
}

// Every input to the socket's directory is checked, because the check is on
// the path ipc.DefaultSocketPath computes.
func TestCheckSocketPathFollowsTheConfigDirectory(t *testing.T) {
	short := "/tmp/s"
	long := "/" + strings.Repeat("x", ipc.MaxSocketPathBytes)
	tests := []struct {
		name     string
		variable string
		sockDir  func(value string) string // the socket's directory, given the variable
		advice   []string                  // what the refusal says to change
		never    []string                  // and what it must not claim
	}{
		{"CLIAMP_CONFIG_DIR", "CLIAMP_CONFIG_DIR", func(v string) string { return v }, fromConfigDir, []string{"TMPDIR", "temporary"}},
		{"XDG_CONFIG_HOME", "XDG_CONFIG_HOME", func(v string) string { return filepath.Join(v, appdir.Name) }, fromConfigDir, []string{"TMPDIR", "temporary"}},
		{"HOME", "HOME", func(v string) string { return filepath.Join(v, ".config", appdir.Name) }, fromConfigDir, []string{"TMPDIR", "temporary"}},
		// With no config directory to be found, the socket goes to the
		// temporary directory, and the advice is about that.
		{"the temporary directory, with no home", "TMPDIR", func(v string) string { return v },
			[]string{"temporary directory", "TMPDIR", "no config directory", "Shorten", appdir.ConfigDirEnv},
			[]string{"which is the first of", "XDG_CONFIG_HOME"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setConfigDirEnv(t, map[string]string{tt.variable: short})
			want := filepath.Join(tt.sockDir(short), socketFile)
			if got := ipc.DefaultSocketPath(); got != want {
				t.Fatalf("socket path = %q, want %q", got, want)
			}
			if err := checkSocketPath(); err != nil {
				t.Errorf("a short %s is refused: %v", tt.variable, err)
			}

			setConfigDirEnv(t, map[string]string{tt.variable: long})
			want = filepath.Join(tt.sockDir(long), socketFile)
			err := checkSocketPath()
			var tooLong *ipc.SocketPathError
			if !errors.As(err, &tooLong) {
				t.Fatalf("a long %s gives %v, want a *ipc.SocketPathError", tt.variable, err)
			}
			if tooLong.Path != want || tooLong.Bytes != len(want) {
				t.Errorf("error = %+v, want the path %q and its %d bytes", tooLong, want, len(want))
			}
			for _, text := range tt.advice {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("the refusal lacks %q:\n%s", text, err)
				}
			}
			for _, text := range tt.never {
				if strings.Contains(err.Error(), text) {
					t.Errorf("the refusal says %q:\n%s", text, err)
				}
			}
		})
	}

	t.Run("the boundary is exact", func(t *testing.T) {
		room := ipc.MaxSocketPathBytes - len("/"+socketFile)
		setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": "/" + strings.Repeat("x", room-1)})
		if err := checkSocketPath(); err != nil {
			t.Errorf("a %d-byte socket path is refused: %v", len(ipc.DefaultSocketPath()), err)
		}
		// Two bytes each: one byte more than fits, with far fewer characters.
		setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": "/" + strings.Repeat("é", room/2) + strings.Repeat("x", room%2)})
		if got := len(ipc.DefaultSocketPath()); got != ipc.MaxSocketPathBytes+1 {
			t.Fatalf("built a %d-byte socket path, want %d", got, ipc.MaxSocketPathBytes+1)
		}
		if err := checkSocketPath(); !socketPathTooLong(err) {
			t.Errorf("a %d-byte socket path is accepted: %v", ipc.MaxSocketPathBytes+1, err)
		}
	})
}

// The player refuses to start before it loads or creates anything.
func TestRunRefusesAnOverlongSocketPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), strings.Repeat("x", ipc.MaxSocketPathBytes))
	setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": dir})
	err := run(config.Overrides{}, nil, false, false)
	if !socketPathTooLong(err) {
		t.Fatalf("run = %v, want the socket path refused", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, socketFile)) {
		t.Errorf("the refusal does not name the path:\n%s", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the config directory exists after the refusal (stat: %v)", err)
	}
}

func TestCommandsReportAnOverlongSocketPath(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("x", ipc.MaxSocketPathBytes))
	for _, args := range [][]string{
		{"vis", "list"},
		{"vis", "next"},
		{"status"},
		{"toggle"},
		{"visstream"},
		{"remote", "state"},
		{"remote", "events", "runtime.state"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": long})
			err := ddsonicApp().Run(context.Background(), append([]string{appdir.Name}, args...))
			if !socketPathTooLong(err) {
				t.Fatalf("error = %v, want the socket path refused", err)
			}
			if got := err.Error(); !strings.Contains(got, appdir.ConfigDirEnv) || strings.Contains(got, "not running") {
				t.Errorf("unhelpful message:\n%s", got)
			}
		})
	}

	// With a usable path and no player, vis list still lists, and the rest
	// still say the player is not running.
	t.Run("a short path is as before", func(t *testing.T) {
		// Not t.TempDir(): under a long TMPDIR (macOS's default is about 50
		// bytes) the test's own name would push this path over the limit.
		dir, err := os.MkdirTemp("/tmp", "dd")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		setConfigDirEnv(t, map[string]string{"CLIAMP_CONFIG_DIR": dir})
		if got := len(ipc.DefaultSocketPath()); got > ipc.MaxSocketPathBytes {
			t.Fatalf("the short fixture's socket path is %d bytes, over the limit of %d", got, ipc.MaxSocketPathBytes)
		}
		if err := ddsonicApp().Run(context.Background(), []string{appdir.Name, "vis", "list"}); err != nil {
			t.Errorf("vis list = %v, want the list", err)
		}
		err = ddsonicApp().Run(context.Background(), []string{appdir.Name, "status"})
		if err == nil || !strings.Contains(err.Error(), "is not running (no socket at ") {
			t.Errorf("status = %v, want not running", err)
		}
	})
}
