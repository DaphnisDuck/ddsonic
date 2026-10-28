//go:build sdkcontract

package sdkcontract

// ddsonic: patch 0003's negative controls (design reader-correction rev5,
// section 8.3; review 41). Each runs named contract tests against a scratch
// variant of the staged reader and must turn exactly its declared assertions
// red, with every other assertion it runs reported as it came out. A
// timeout, a build failure, a panic, a data race or a SETUP failure is no
// red.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// prefetchM2BaseSHA is the authentic, unpatched reader (v0.9.1, which
// patches 0001 and 0002 leave unchanged). M2 is those bytes, taken from the
// authenticated base, never a copy kept here.
const prefetchM2BaseSHA = "aa9fc09b71af131a599ec1fb61c84e65cb2b49fd82fa194ccf90b3f89ce8865a"

type prefetchMutant struct {
	name     string
	old, new string // an anchored replacement in the staged reader; "" for M2
	tests    []string
	red      []string // assertions that must be red
	green    []string // assertions that must pass
}

var prefetchMutants = []prefetchMutant{
	{
		name:  "M1",
		old:   "\t\tchunk.Broadcast()\n\t\tr.prefetchWg.Done()\n\t\tchunk.L.Unlock()\n\t\treturn\n",
		new:   "\t\tchunk.Broadcast()\n\t\tchunk.L.Unlock()\n\t\tddsonicPrefetchM1Wait()\n\t\tr.prefetchWg.Done()\n\t\treturn\n",
		tests: []string{"TestDDSonicContractPrefetchC10AccountingOrder"},
		red:   []string{"C10-WAIT"},
		green: []string{"C10-L"},
	},
	{
		name:  "M2",
		tests: []string{"TestDDSonicContractPrefetchC1SingleFlight", "TestDDSonicContractPrefetchC2AtomicReservation", "TestDDSonicContractPrefetchC3CancelledStreamQuiesces"},
		red:   []string{"C1-A1", "C1-A2", "C2-A1", "C2-A2", "C3-A4"},
		green: []string{"C1-A3", "C2-A3", "C3-A5", "C3-A6"},
	},
	{
		name:  "M3",
		old:   "\t\tchunk.fetching = false\n\t\tchunk.Broadcast()\n\t\tr.prefetchWg.Done()\n",
		new:   "\t\tchunk.fetching = false\n\t\tr.prefetchWg.Done()\n",
		tests: []string{"TestDDSonicContractPrefetchC8WaiterWakeup"},
		red:   []string{"C8-WAKE"},
		green: []string{"C8-BYTES", "C8-REQ", "C8-CLS"},
	},
}

var prefetchAssertLine = regexp.MustCompile(`^\s+\S+:\d+: ASSERT (\S+) expect=.* (PASS|RED \[(\S+)\])$`)

// prefetchAllowedLines are the only other lines a mutant run may print
// (review 42 R42-2): test framing, populations, and the non-failing M2
// cleanup diagnostic. Every other line is an unexpected failure diagnostic.
var prefetchAllowedLines = []*regexp.Regexp{
	regexp.MustCompile(`^=== (RUN|PAUSE|CONT|NAME)\s`),
	regexp.MustCompile(`^--- (FAIL|PASS): \S+ \(`),
	regexp.MustCompile(`^(PASS|FAIL)$`),
	regexp.MustCompile(`^(ok|FAIL)\s+\S+\s+\S+`),
	regexp.MustCompile(`^\s+\S+:\d+: POP \S+ PF=\d+ FG=\d+ ROSTER=\d+$`),
	regexp.MustCompile(`^\s+\S+:\d+: CLEANUP: reader work still live \S+ after the result; closing the reader directly$`),
}

// validatePrefetchMutant gives credit only for exactly the intended failures:
// every declared RED is red, every declared green passes, no other assertion
// is red, and no other line (an unmarked t.Error, a failing CLEANUP) appears.
// It returns every assertion outcome it read.
func validatePrefetchMutant(m prefetchMutant, out []byte) (map[string]string, error) {
	text := testOutput(out)
	for _, invalid := range []string{"panic:", "[build failed]", "[setup failed]", "WARNING: DATA RACE", "SETUP:"} {
		if strings.Contains(text, invalid) {
			return nil, fmt.Errorf("the run shows %q, which is no behavioural red", invalid)
		}
	}
	got := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if match := prefetchAssertLine.FindStringSubmatch(line); match != nil {
			id := match[1]
			if match[2] == "PASS" {
				if got[id] == "" {
					got[id] = "PASS"
				}
				continue
			}
			if match[3] != id {
				return got, fmt.Errorf("assertion %s is marked RED for %s", id, match[3])
			}
			if !slices.Contains(m.red, id) {
				return got, fmt.Errorf("unexpected RED assertion %s: %q", id, strings.TrimSpace(line))
			}
			got[id] = "RED"
			continue
		}
		if !slices.ContainsFunc(prefetchAllowedLines, func(re *regexp.Regexp) bool { return re.MatchString(line) }) {
			return got, fmt.Errorf("unexpected diagnostic: %q", strings.TrimSpace(line))
		}
	}
	for _, id := range m.red {
		if got[id] != "RED" {
			return got, fmt.Errorf("assertion %s is %q, want RED", id, got[id])
		}
	}
	for _, id := range m.green {
		if got[id] != "PASS" {
			return got, fmt.Errorf("assertion %s is %q, want PASS", id, got[id])
		}
	}
	passed, bad := testOutcomes(out)
	wantBad := []string{sdkModule + "/audio (fail)"}
	for _, test := range m.tests {
		wantBad = append(wantBad, test+" (fail)")
	}
	slices.Sort(bad)
	slices.Sort(wantBad)
	if len(passed) != 0 || !slices.Equal(bad, wantBad) {
		return got, fmt.Errorf("failed %q, passed %q; want each of %q to fail", bad, passed, m.tests)
	}
	return got, nil
}

// prefetchValidatorControls are M1 runs with one extra failure injected into
// the same failing test (C10, through DDSONIC_PREFETCH_VALIDATOR_CONTROL): the
// validator must refuse each for that extra failure, and for nothing else.
var prefetchValidatorControls = []struct{ value, want string }{
	{"errorf", "CONTROL-EXTRA-FAILURE"},
	{"red", "unexpected RED assertion C10-XRED"},
}

func (r *runner) prefetchMutations(scratch, staged, baseDir string, replace map[string]string) error {
	target := filepath.Join(staged, "audio", "chunked-reader.go")
	src, err := os.ReadFile(target)
	if err != nil {
		return &stepError{12, err.Error(), ""}
	}
	for _, m := range prefetchMutants {
		var mutated []byte
		if m.old == "" {
			base, err := os.ReadFile(filepath.Join(baseDir, "audio", "chunked-reader.go"))
			if err != nil {
				return &stepError{12, err.Error(), ""}
			}
			if sum := sha256.Sum256(base); hex.EncodeToString(sum[:]) != prefetchM2BaseSHA {
				return &stepError{12, fmt.Sprintf("prefetch mutant %s: the base reader hashes to %x, want %s", m.name, sum, prefetchM2BaseSHA), ""}
			}
			mutated = base
		} else {
			if n := bytes.Count(src, []byte(m.old)); n != 1 {
				return &stepError{12, fmt.Sprintf("prefetch mutant %s: its anchor occurs %d times in audio/chunked-reader.go, want once", m.name, n), ""}
			}
			mutated = bytes.Replace(src, []byte(m.old), []byte(m.new), 1)
		}
		file := filepath.Join(r.dir, "prefetch-"+m.name+".go")
		if err := os.WriteFile(file, mutated, 0o644); err != nil {
			return &stepError{12, err.Error(), ""}
		}
		rep := maps.Clone(replace)
		rep[target] = file
		overlay, _ := json.Marshal(map[string]any{"Replace": rep})
		overlayPath := filepath.Join(r.dir, "overlay-prefetch-"+m.name+".json")
		if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
			return &stepError{12, err.Error(), ""}
		}
		run := func(label string, extra []string) ([]byte, error) {
			out, err := r.toolEnv(12, "go test under prefetch mutant "+label, scratch, 10*time.Minute, extra, "test", "-overlay="+overlayPath, "-race", "-count=1", "-json",
				"-run", "^("+strings.Join(m.tests, "|")+")$", sdkModule+"/audio")
			var se *stepError
			switch {
			case err == nil:
				return out, &stepError{12, fmt.Sprintf("prefetch mutant %s: its tests passed, so it is not caught", label), string(out)}
			case !errors.As(err, &se) || strings.Contains(se.what, "killed at its deadline"):
				return out, &stepError{12, fmt.Sprintf("prefetch mutant %s: the run did not end in a test failure: %v", label, err), ""}
			}
			return out, nil
		}
		out, err := run(m.name, nil)
		if err != nil {
			return err
		}
		got, verr := validatePrefetchMutant(m, out)
		if verr != nil {
			return &stepError{12, fmt.Sprintf("prefetch mutant %s: %v", m.name, verr), testOutput(out)}
		}
		ids := slices.Sorted(maps.Keys(got))
		var all []string
		for _, id := range ids {
			all = append(all, id+"="+got[id])
		}
		r.notes = append(r.notes, fmt.Sprintf("prefetch mutant %s: %s", m.name, strings.Join(all, " ")))
		if m.name != "M1" {
			continue
		}
		for _, ctl := range prefetchValidatorControls {
			label := m.name + " + validator control " + ctl.value
			out, err := run(label, []string{"DDSONIC_PREFETCH_VALIDATOR_CONTROL=" + ctl.value})
			if err != nil {
				return err
			}
			_, verr := validatePrefetchMutant(m, out)
			switch {
			case verr == nil:
				return &stepError{12, fmt.Sprintf("prefetch %s: the validator accepted a run with an extra failure", label), testOutput(out)}
			case !strings.Contains(verr.Error(), ctl.want):
				return &stepError{12, fmt.Sprintf("prefetch %s: refused for another reason than the extra failure (%q): %v", label, ctl.want, verr), testOutput(out)}
			}
			r.notes = append(r.notes, fmt.Sprintf("prefetch %s: refused: %v", label, verr))
		}
	}
	return nil
}

// testOutput is the concatenated Output of go test -json.
func testOutput(out []byte) string {
	var b strings.Builder
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var ev struct{ Output string }
		if err := dec.Decode(&ev); err != nil {
			break
		}
		b.WriteString(ev.Output)
	}
	return b.String()
}
