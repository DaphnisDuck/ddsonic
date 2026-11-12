package main

// ddsonic: the #168 memory diagnostics are off unless DDSONIC_MEMDIAG is set.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/memdiag"
)

// Unset (or empty), the variable starts nothing: no goroutine, no file, no
// directory, no sequence number on an open's context.
func TestMemDiagOffByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(wd)
	for _, v := range []string{"", "unset"} {
		env := func(string) string { return "" }
		if v == "unset" {
			env = os.Getenv
			t.Setenv(memdiag.Env, "")
			os.Unsetenv(memdiag.Env)
		}
		g := runtime.NumGoroutine()
		stop, err := startMemDiag(env)
		if err != nil {
			t.Fatal(err)
		}
		if memdiag.On() || memdiag.Dir() != "" {
			t.Fatalf("%s: diagnostics are on", v)
		}
		ctx := context.Background()
		if memdiag.WithOpen(ctx) != ctx {
			t.Errorf("%s: an open's context was changed", v)
		}
		if n := runtime.NumGoroutine(); n > g {
			time.Sleep(50 * time.Millisecond)
			if n = runtime.NumGoroutine(); n > g {
				t.Errorf("%s: %d goroutines, %d before", v, n, g)
			}
		}
		stop()
	}
	if ents, _ := os.ReadDir(home); len(ents) != 0 {
		t.Errorf("files were made in the home directory: %v", ents)
	}
	if after, _ := os.ReadDir(wd); len(after) != len(before) {
		t.Errorf("the working directory changed: %d entries, %d before", len(after), len(before))
	}
}

// A directory that cannot be used stops the program, and leaves them off.
func TestMemDiagUnusableDirectoryFails(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := startMemDiag(func(string) string { return file }); err == nil {
		t.Error("a file was accepted as the output directory")
	}
	if memdiag.On() {
		t.Error("diagnostics are on after a failed start")
	}
}

// Set, they are on until stopped, and write in a private run directory.
func TestMemDiagOnWhenSet(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diag")
	stop, err := startMemDiag(func(k string) string {
		if k == memdiag.Env {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !memdiag.On() || filepath.Dir(memdiag.Dir()) != dir {
		t.Errorf("on %t, run directory %q", memdiag.On(), memdiag.Dir())
	}
	stop()
	if memdiag.On() {
		t.Error("still on after stop")
	}
}
