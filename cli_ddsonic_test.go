package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// cliSurface renders the public command-line surface: every visible command
// with its aliases, and every flag with its type, names, default and scope.
// Help prose stays out, so rewording doesn't churn the golden file.
func cliSurface(app *cli.Command) string {
	var b strings.Builder
	var walk func(path string, c *cli.Command)
	flags := func(path string, fs []cli.Flag) {
		for _, f := range fs {
			scope := ""
			if lf, ok := f.(cli.LocalFlag); ok && lf.IsLocal() {
				scope = " local"
			}
			def := ""
			switch f := f.(type) {
			case *cli.BoolWithInverseFlag: // GetValue is empty for these
				def = fmt.Sprintf(" default=%v", f.Value)
			case *cli.BoolFlag:
				def = fmt.Sprintf(" default=%v", f.Value)
			case cli.DocGenerationFlag:
				if f.TakesValue() {
					def = " default=" + f.GetValue()
				}
			}
			fmt.Fprintf(&b, "%s flag %s %s%s%s\n", path, strings.Join(f.Names(), ","), flagKind(f), def, scope)
		}
	}
	walk = func(path string, c *cli.Command) {
		if c.Hidden {
			return
		}
		aliases := ""
		if len(c.Aliases) > 0 {
			aliases = " aliases=" + strings.Join(c.Aliases, ",")
		}
		fmt.Fprintf(&b, "command %s%s\n", path, aliases)
		flags(path, c.Flags)
		for _, sub := range c.Commands {
			walk(path+" "+sub.Name, sub)
		}
	}
	walk(app.Name, app)
	return b.String()
}

// flagKind names a flag's value type: urfave's flags are generic, and %T
// spells out every type parameter.
func flagKind(f cli.Flag) string {
	if _, ok := f.(*cli.BoolWithInverseFlag); ok {
		return "bool+no"
	}
	kind := fmt.Sprintf("%T", f)
	if m := regexp.MustCompile(`FlagBase\[([^,]+),`).FindStringSubmatch(kind); m != nil {
		return m[1]
	}
	return kind
}

// The golden file pins what ddsonic 1.0 promises on the command line. A
// deliberate change: DDSONIC_UPDATE_GOLDEN=1 go test -run TestCLISurfaceGolden .
func TestCLISurfaceGolden(t *testing.T) {
	got := cliSurface(ddsonicApp())
	path := filepath.Join("testdata", "cli-surface.golden")
	if os.Getenv("DDSONIC_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with DDSONIC_UPDATE_GOLDEN=1)", err)
	}
	if got != string(want) {
		t.Errorf("the CLI surface changed; if that's intended, regenerate %s and review the diff.\ngot:\n%s", path, got)
	}
}

func TestDdsonicAppRemovesUnsupported(t *testing.T) {
	upstream, shipped := buildApp(), ddsonicApp()
	flagNames := func(c *cli.Command) []string {
		var names []string
		for _, f := range c.Flags {
			names = append(names, f.Names()...)
		}
		return names
	}
	for _, name := range removedCommands {
		if upstream.Command(name) == nil {
			t.Errorf("upstream has no %q command: the removal list is stale", name)
		}
		stub := shipped.Command(name)
		if stub == nil || !stub.Hidden {
			t.Errorf("ddsonic still lists the %q command", name)
			continue
		}
		if err := stub.Action(context.Background(), stub); err == nil || !strings.Contains(err.Error(), "not part of ddsonic 1.0") {
			t.Errorf("%s stub: %v", name, err)
		}
	}
	for _, name := range removedFlags {
		if !slices.Contains(flagNames(upstream), name) {
			t.Errorf("upstream has no --%s flag: the removal list is stale", name)
		}
		if slices.Contains(flagNames(shipped), name) {
			t.Errorf("ddsonic still ships --%s", name)
		}
	}
}

func TestHistoryClearTakesNoOptions(t *testing.T) {
	history := ddsonicApp().Command("history")
	if history == nil || len(history.Flags) == 0 {
		t.Fatal("history lost its listing options")
	}
	for _, f := range history.Flags {
		if lf, ok := f.(cli.LocalFlag); !ok || !lf.IsLocal() {
			t.Errorf("history's --%s reaches history clear", f.Names()[0])
		}
	}
}

func TestCLIErrorNamesDdsonic(t *testing.T) {
	got := cliError(errors.New("usage: cliamp volume <dB>"))
	if got != "usage: ddsonic volume <dB>" {
		t.Errorf("cliError = %q", got)
	}
	if got := cliError(errors.New("cliamp radio is gone")); got != "cliamp radio is gone" {
		t.Errorf("cliError changed a non-usage message: %q", got)
	}
}

func TestCheckModeName(t *testing.T) {
	tests := []struct {
		op, name string
		ok       bool
	}{
		{"shuffle", "on", true},
		{"shuffle", "toggle", true},
		{"shuffle", "bogus", false},
		{"repeat", "one", true},
		{"repeat", "cycle", true},
		{"repeat", "bogus", false},
	}
	for _, tt := range tests {
		err := checkModeName(tt.op, tt.name)
		if (err == nil) != tt.ok {
			t.Errorf("checkModeName(%q, %q) = %v", tt.op, tt.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), tt.name) {
			t.Errorf("error doesn't name the bad value: %v", err)
		}
	}
}

func TestVersionFlagWithoutBuildVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = ""
	if got := ddsonicApp().Version; got != "dev" {
		t.Errorf("Version = %q, want dev", got)
	}
}

// A changed boolean default must change the snapshot too (Codex, cli1 R1-F1).
func TestCLISurfacePinsBoolDefaults(t *testing.T) {
	app := ddsonicApp()
	before := cliSurface(app)
	for _, f := range app.Flags {
		if b, ok := f.(*cli.BoolWithInverseFlag); ok && b.Name == "help-bar" {
			b.Value = !b.Value
		}
	}
	if cliSurface(app) == before {
		t.Error("flipping --help-bar's default left the surface unchanged")
	}
}

// The first line of --help carries the logo's tagline, not cliamp's.
func TestDdsonicAppUsageIsTheTagline(t *testing.T) {
	if got, want := ddsonicApp().Usage, "music for your terminal"; got != want {
		t.Errorf("Usage = %q, want %q", got, want)
	}
}
