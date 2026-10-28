//go:build sdkcontract

package sdkcontract

// Steps 11 and 12 of the gate: patch 0002's symbols gate, from its own
// module (tools/sdkgate), and its behavioural mutations B1 and B2.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// gateModules are what tools/sdkgate builds from, which the offline module
// source must hold: a prerequisite checked here, never fetched.
var gateModules = []struct{ path, version string }{
	{"golang.org/x/tools", "v0.50.0"},
	{"golang.org/x/mod", "v0.41.0"},
	{"golang.org/x/sync", "v0.23.0"},
}

// gateTests are the symbols gate's tests: C0, the source controls, T-S2 and
// its control.
var gateTests = []string{"TestSymbolsWhitelist", "TestSymbolsControls", "TestPublicAPIDelta", "TestPublicAPIDeltaControl"}

// symbolsGate is step 11: tools/sdkgate's tests over the staged tree, the
// scratch module, the overlay and the base.
func (r *runner) symbolsGate(scratch, staged, base, overlayPath string) error {
	for _, m := range gateModules {
		for _, ext := range []string{".info", ".mod", ".zip"} {
			p := filepath.Join(r.proxy, filepath.FromSlash(m.path), "@v", m.version+ext)
			if _, err := os.Stat(p); err != nil {
				return &stepError{11, fmt.Sprintf("the offline module source lacks %s@%s%s, which the symbols gate's module needs (fetch it once with go mod download in tools/sdkgate)", m.path, m.version, ext), ""}
			}
		}
	}
	out, err := r.toolEnv(11, "the symbols gate", filepath.Join(r.repo, "tools", "sdkgate"), 10*time.Minute,
		[]string{"SDKGATE_SCRATCH=" + scratch, "SDKGATE_STAGED=" + staged, "SDKGATE_BASE=" + base, "SDKGATE_OVERLAY=" + overlayPath},
		"test", "-buildvcs=false", "-count=1", "-json", "./...")
	if err != nil {
		return err
	}
	passed, bad := testOutcomes(out)
	if len(bad) != 0 {
		return &stepError{11, "the symbols gate did not pass: " + strings.Join(bad, ", "), string(out)}
	}
	for _, want := range gateTests {
		if !slices.Contains(passed, want) {
			return &stepError{11, want + " was not shown to pass", string(out)}
		}
	}
	r.notes = append(r.notes, fmt.Sprintf("symbols gate: %d tests and controls passed", len(passed)))
	return nil
}

// behaviouralMutations are B1 and B2: a mutation of the staged ap/ap.go, the
// one test it must turn red, and what its failure must say.
var behaviouralMutations = []struct {
	name, test, old, new string
	markers              []string
}{
	{
		// C5's unconditional write: two constructors both own the access point.
		name: "B1", test: "TestDDSonicContractOneOwnerPerAccessPoint",
		old:     "\t\tif a.announcer != nil {\n\t\t\treturn keylink.ErrTaken\n\t\t}\n",
		new:     "",
		markers: []string{"B1-RED"},
	},
	{
		// The slot cleared under connMu.Lock immediately before W2's
		// snapshot: the announcement is lost, L7 ends the request with
		// DeadlineExceeded, and nothing revives it.
		name: "B2", test: "TestDDSonicContractAnnouncementDefersExpiry",
		old:     "\tobs := ap.announcer\n",
		new:     "\tap.announcer = nil\n\tobs := ap.announcer\n",
		markers: []string{"B2-RED", "B2-OUTCOME deadline", "B2-NORESURRECTION ok"},
	},
}

// mutations is step 12. Each mutation must end its test in a behavioural
// failure: a deadline, a build failure, a panic or a data race is no red.
func (r *runner) mutations(scratch, staged string, replace map[string]string) error {
	target := filepath.Join(staged, "ap", "ap.go")
	src, err := os.ReadFile(target)
	if err != nil {
		return &stepError{12, err.Error(), ""}
	}
	for _, m := range behaviouralMutations {
		if n := bytes.Count(src, []byte(m.old)); n != 1 {
			return &stepError{12, fmt.Sprintf("mutation %s: its anchor occurs %d times in ap/ap.go, want once", m.name, n), ""}
		}
		mutated := filepath.Join(r.dir, "mutation-"+m.name+".go")
		if err := os.WriteFile(mutated, bytes.Replace(src, []byte(m.old), []byte(m.new), 1), 0o644); err != nil {
			return &stepError{12, err.Error(), ""}
		}
		rep := maps.Clone(replace)
		rep[target] = mutated
		overlay, _ := json.Marshal(map[string]any{"Replace": rep})
		overlayPath := filepath.Join(r.dir, "overlay-"+m.name+".json")
		if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
			return &stepError{12, err.Error(), ""}
		}
		out, err := r.tool(12, "go test under mutation "+m.name, scratch, 10*time.Minute, "test", "-overlay="+overlayPath, "-race", "-count=1", "-json",
			"-run", "^"+m.test+"$", sdkModule+"/audio")
		var se *stepError
		switch {
		case err == nil:
			return &stepError{12, fmt.Sprintf("mutation %s: %s passed, so the mutation is not caught", m.name, m.test), string(out)}
		case !errors.As(err, &se) || strings.Contains(se.what, "killed at its deadline"):
			return &stepError{12, fmt.Sprintf("mutation %s: the run did not end in a test failure: %v", m.name, err), ""}
		}
		text := string(out)
		for _, invalid := range []string{"panic:", "[build failed]", "[setup failed]", "WARNING: DATA RACE"} {
			if strings.Contains(text, invalid) {
				return &stepError{12, fmt.Sprintf("mutation %s: the run shows %q, which is no behavioural red", m.name, invalid), text}
			}
		}
		passed, bad := testOutcomes(out)
		if slices.Contains(passed, m.test) || !slices.Equal(bad, []string{m.test + " (fail)", sdkModule + "/audio (fail)"}) {
			return &stepError{12, fmt.Sprintf("mutation %s: failed %q, passed %q; want %s alone to fail", m.name, bad, passed, m.test), text}
		}
		for _, marker := range m.markers {
			if !strings.Contains(text, marker) {
				return &stepError{12, fmt.Sprintf("mutation %s: %s failed without %q", m.name, m.test, marker), text}
			}
		}
		r.notes = append(r.notes, fmt.Sprintf("mutation %s: %s red, with %s", m.name, m.test, strings.Join(m.markers, ", ")))
	}
	return nil
}

// wantLateStep runs r and requires it to fail at step, after the contract
// tests ran.
func wantLateStep(t *testing.T, r *runner, step int, contains string) {
	t.Helper()
	_, err := r.run()
	var se *stepError
	if !errors.As(err, &se) || se.step != step {
		t.Fatalf("the runner ended with %v; want it to fail at step %d", err, step)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("the failure does not say %q:\n%v", contains, err)
	}
}

// An offline source without the symbols gate's loader fails at step 11; it
// is never fetched and never skipped.
func TestSDKContractMissingGateModuleFails(t *testing.T) {
	r := newRunner(t)
	r.proxy = offlineSource(t, func(string) {})
	// A symbolic link in the test's own copy of the source, not the archive.
	if err := os.Remove(filepath.Join(r.proxy, "golang.org", "x", "tools", "@v", "v0.50.0.zip")); err != nil {
		t.Fatal(err)
	}
	wantLateStep(t, r, 11, "golang.org/x/tools@v0.50.0.zip")
}

// A mutation that its test does not turn red fails step 12.
func TestSDKContractUncaughtMutationFails(t *testing.T) {
	saved := behaviouralMutations
	t.Cleanup(func() { behaviouralMutations = saved })
	anchor := "\tobs := ap.announcer\n"
	behaviouralMutations = append(behaviouralMutations[:0:0], struct {
		name, test, old, new string
		markers              []string
	}{name: "B0", test: "TestDDSonicContractAnnouncementDefersExpiry", old: anchor, new: anchor})
	r := newRunner(t)
	wantLateStep(t, r, 12, "is not caught")
}
