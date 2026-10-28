//go:build sdkcontract

package sdkcontract

// ddsonic: the composed stage (M3b2, plan v7 §9), steps 13 and 14 of the
// sequence. The composed tests run the real ddsonic Session and provider in
// external/spotify over the staged SDK, with only remote services and the
// access point replaced by loopback fixtures; their sources are overlays in
// testdata, so go test ./... never compiles them.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bypassSHA256 binds the guard bypass's overlay bytes (plan v7 §2.6 (a)).
const bypassSHA256 = "d35fbdf81f92b97824b89f0478f04bee7c7da6cd98693030acca90f5efb2f561"

// composedFiles maps each composed overlay source to where it is compiled:
// "sdk:" into the staged SDK, "app:" into external/spotify.
var composedFiles = map[string]string{
	"ap_fake_helper.go.txt":          "sdk:ap/zz_ddsonic_contract_fake.go",
	"ap_composed_observer.go.txt":    "sdk:ap/zz_ddsonic_composed_observer.go",
	"audio_composed_keyprobe.go.txt": "sdk:audio/zz_ddsonic_composed_keyprobe.go", // D7 (plan v9 §2.7)
	// RB-1's reconnect gate (M3B2-S4 bundle): active stage only, never in the stock O.
	"ap_composed_regate.go.txt":     "sdk:ap/zz_ddsonic_composed_regate.go",
	"composed_harness_test.go.txt":  "app:zz_ddsonic_composed_harness_test.go",
	"composed_controls_test.go.txt": "app:zz_ddsonic_composed_controls_test.go",
	// The step-2 rows; the file sorts before the controls, so ZZ stays last.
	"composed_cases_test.go.txt":  "app:zz_ddsonic_composed_cases_test.go",
	"composed_bypass_test.go.txt": "app:composed_bypass_ddsonic_test.go",
}

// composedParentTests is the exact required parent list (plan v9 §9): step
// 14's parent run must start each of these once and see it pass, and start
// no other top-level test but the conditional composedE1
// (checkComposedParents). 61 names.
var composedParentTests = []string{
	"TestDDSonicComposedSmokeP0",
	"TestDDSonicComposedFC1UnknownHost",
	"TestDDSonicComposedFC2UnknownPath",
	"TestDDSonicComposedFC3Redirect",
	"TestDDSonicComposedFC4Dial",
	"TestDDSonicComposedFC9DealerObserved",
	"TestDDSonicComposedFC10LateUse",
	"TestDDSonicComposedFC8BypassBinding",
	"TestDDSonicComposedFC13ReaderTail",
	"TestDDSonicComposedFC14CutBoundary",
	// The step-1 assurance tests (plan v9 §2.8, R31-5).
	"TestDDSonicComposedI26bFeasibility",
	"TestDDSonicComposedFixtureSharedHold",
	"TestDDSonicComposedD10DrainObserved",
	// The I-rows (steps 2, 4 and 5).
	"TestDDSonicComposedI1",
	"TestDDSonicComposedI2",
	"TestDDSonicComposedI3",
	"TestDDSonicComposedI4",
	"TestDDSonicComposedI5Direct",
	"TestDDSonicComposedI5Retired",
	"TestDDSonicComposedI6",
	"TestDDSonicComposedI6b",
	"TestDDSonicComposedI7",
	"TestDDSonicComposedI8",
	"TestDDSonicComposedI9",
	"TestDDSonicComposedI10Direct",
	"TestDDSonicComposedI10ProviderDeadline",
	"TestDDSonicComposedI10SDKWindow",
	"TestDDSonicComposedI13I14",
	"TestDDSonicComposedI13pI14p",
	"TestDDSonicComposedI15",
	"TestDDSonicComposedI16",
	"TestDDSonicComposedI17",
	"TestDDSonicComposedI18",
	"TestDDSonicComposedI19a",
	"TestDDSonicComposedI19b",
	"TestDDSonicComposedI20",
	"TestDDSonicComposedI21",
	"TestDDSonicComposedI22",
	"TestDDSonicComposedI23",
	"TestDDSonicComposedI24",
	"TestDDSonicComposedI25",
	"TestDDSonicComposedI26",
	"TestDDSonicComposedI27Audit",
	"TestDDSonicComposedI28",
	"TestDDSonicComposedI29",
	"TestDDSonicComposedI30",
	// The lifecycle and FLAC rows (steps 2 and 5).
	"TestDDSonicComposedL1a",
	"TestDDSonicComposedL1b",
	"TestDDSonicComposedL2",
	"TestDDSonicComposedL3",
	"TestDDSonicComposedL4",
	"TestDDSonicComposedL5",
	"TestDDSonicComposedL6Rebuild",
	"TestDDSonicComposedL6FailedAttempt",
	"TestDDSonicComposedL6RejectedBuild",
	"TestDDSonicComposedL6AbandonedBuild",
	"TestDDSonicComposedL6Close",
	"TestDDSonicComposedF1a",
	"TestDDSonicComposedF1b",
	"TestDDSonicComposedF1c",
	"TestDDSonicComposedZZNoLateUse",
}

// composedE1 is the one conditional parent (plan v9 §7, §9): absent, the
// runner records E1 NOT COVERED; present, it must pass like any other.
const composedE1 = "TestDDSonicComposedE1"

// checkComposedParents is step 14's exact parent set (plan v9 §8 I12, §9),
// over the parent run's go test -json events: the top-level tests started
// and ended. Every listed name and an admitted composedE1 must be started
// once and end once, with pass; a listed name that is absent, skipped,
// failed, incomplete (started, never ended) or duplicated fails, and so
// does any other started or ended top-level name (an unlisted one, such
// as the mutant-only TestDDSonicComposedI1Ordered). The whole stdout must
// be read as test2json events: a line that is not a JSON event with an
// Action, or a read that stops early (R76-3), fails rather than leaving an
// accepted prefix. It returns the step's note: the count, and E1 NOT
// COVERED when E1 is absent.
func checkComposedParents(out []byte) (string, error) {
	return checkComposedParentsMax(out, 64<<20)
}

// checkComposedParentsMax is checkComposedParents with the longest line it
// reads, maxLine (its unit makes the scanner stop early with a small one).
func checkComposedParentsMax(out []byte, maxLine int) (string, error) {
	started, ended := map[string]int{}, map[string][]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, min(1<<20, maxLine)), maxLine)
	line := 0
	for sc.Scan() {
		line++
		var ev struct{ Action, Test string }
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Action == "" {
			return "", fmt.Errorf("line %d of the parent run's output is not a test2json event (%v): %.80q", line, err, sc.Bytes())
		}
		if ev.Test == "" || strings.Contains(ev.Test, "/") {
			continue
		}
		switch ev.Action {
		case "run":
			started[ev.Test]++
		case "pass", "fail", "skip":
			ended[ev.Test] = append(ended[ev.Test], ev.Action)
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("the parent run's output was not read to its end (after line %d): %w", line, err)
	}
	listed := map[string]bool{composedE1: true}
	for _, name := range composedParentTests {
		listed[name] = true
	}
	seen := map[string]bool{}
	for name := range started {
		seen[name] = true
	}
	for name := range ended {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	var bad []string
	for _, name := range names {
		switch {
		case !listed[name]:
			bad = append(bad, name+": not a listed parent")
		case started[name] != 1 || len(ended[name]) != 1:
			bad = append(bad, fmt.Sprintf("%s: started %d times, ended %v", name, started[name], ended[name]))
		case ended[name][0] != "pass":
			bad = append(bad, name+": "+ended[name][0])
		}
	}
	for _, name := range composedParentTests {
		if !seen[name] {
			bad = append(bad, name+": absent")
		}
	}
	if len(bad) != 0 {
		return "", errors.New(strings.Join(bad, "; "))
	}
	e1 := "E1 NOT COVERED (episodes are not covered in composition; plan v9 §7, §9)"
	if seen[composedE1] {
		e1 = "E1 passed"
	}
	return fmt.Sprintf("exactly the %d listed parents passed; %s", len(composedParentTests), e1), nil
}

// childExpect is a child control's exact expected outcome (plan v7 §9).
//
// Every message line a test or subtest writes (a t.Log or t.Error line,
// "<file>.go:<line>: <message>") must begin with one of its allowed log
// prefixes or one of its expected failures; an expected failure must occur
// exactly as often as stated, and nothing else may. So a child that fails on
// purpose cannot hide another assertion failure (R29-1).
type childExpect struct {
	results  map[string]string         // test or subtest -> pass / fail
	logs     map[string][]string       // test or subtest -> allowed log prefixes
	failures map[string]map[string]int // test or subtest -> expected failure prefix -> exact count
	markers  map[string][]string       // test or subtest -> what its output must hold
	forbid   []string                  // what no output may hold
	check    func(out string) error
}

var composedChildren = map[string]childExpect{
	"TestDDSonicChildFC5": {
		results: map[string]string{"TestDDSonicChildFC5": "pass"},
		logs:    map[string][]string{"TestDDSonicChildFC5": {"FC5-FOLLOWUP-ISSUED", "FC5-ORDER "}},
		markers: map[string][]string{"TestDDSonicChildFC5": {"FC5-FOLLOWUP-ISSUED", "dest=router"}},
		check: func(out string) error {
			return eventOrder(out, "FC5-ORDER", func(restore, release, followup int) bool { return followup > 0 && followup < restore })
		},
	},
	"TestDDSonicChildFC6Wake": {
		results: map[string]string{"TestDDSonicChildFC6Wake": "fail", "TestDDSonicChildFC6Wake/holder": "fail", "TestDDSonicChildFC6Wake/waiter": "pass"},
		logs: map[string][]string{
			"TestDDSonicChildFC6Wake/holder": {"FC6-WAITER-STATE select", "FC6-WAITER-BLOCKED-BEFORE-POISON"},
			"TestDDSonicChildFC6Wake/waiter": {"FC6-WAKE-OK "},
		},
		failures: map[string]map[string]int{"TestDDSonicChildFC6Wake/holder": {"composed cleanup failed at T5e (router in-flight)": 1}},
		markers: map[string][]string{
			"TestDDSonicChildFC6Wake/holder": {"FC6-WAITER-STATE select", "FC6-WAITER-BLOCKED-BEFORE-POISON"},
			"TestDDSonicChildFC6Wake/waiter": {"FC6-WAKE-OK"},
		},
	},
	"TestDDSonicChildFC6Arrival": {
		results:  map[string]string{"TestDDSonicChildFC6Arrival": "fail", "TestDDSonicChildFC6Arrival/holder": "fail", "TestDDSonicChildFC6Arrival/arrival": "pass"},
		logs:     map[string][]string{"TestDDSonicChildFC6Arrival/arrival": {"FC6-ARRIVAL-OK "}},
		failures: map[string]map[string]int{"TestDDSonicChildFC6Arrival/holder": {"composed cleanup failed at T5e (router in-flight)": 1}},
		markers:  map[string][]string{"TestDDSonicChildFC6Arrival/arrival": {"FC6-ARRIVAL-OK"}},
	},
	"TestDDSonicChildFC7": {
		results: map[string]string{"TestDDSonicChildFC7": "pass"},
		logs:    map[string][]string{"TestDDSonicChildFC7": {"FC7-REPANIC-RECOVERED ", "FC7-FOLLOWUP-ISSUED", "FC7-ORDER "}},
		markers: map[string][]string{"TestDDSonicChildFC7": {"FC7-REPANIC-RECOVERED", "FC7-FOLLOWUP-ISSUED", "dest=router"}},
	},
	"TestDDSonicChildFC11": {
		results:  map[string]string{"TestDDSonicChildFC11": "fail"},
		failures: map[string]map[string]int{"TestDDSonicChildFC11": {"ZZ: router TestDDSonicChildFC11: an undeclared late use": 1}},
	},
	"TestDDSonicChildFC12": {
		results: map[string]string{"TestDDSonicChildFC12": "fail", "TestDDSonicChildFC12/holder": "fail", "TestDDSonicChildFC12/after": "pass"},
		logs: map[string][]string{
			"TestDDSonicChildFC12/holder": {"P0-COUNTS-OBSERVED ", "FC12-PRE-OK", "FC12-C1-OK", "FC12-CHECKPOINT-OBSERVED ", "FC12-CHECKPOINT-OK"}, // MUTATION-POINT R30-1
			"TestDDSonicChildFC12/after":  {"FC12-REFUSED-OK", "FC12-WORKER-ENDED ", "FC12-RETAINED-OK"},
		},
		failures: map[string]map[string]int{"TestDDSonicChildFC12/holder": {"composed cleanup failed at T5b (builds)": 1}},
		markers: map[string][]string{
			"TestDDSonicChildFC12/holder": {"FC12-PRE-OK", "FC12-C1-OK", "FC12-CHECKPOINT-OK"},
			"TestDDSonicChildFC12/after":  {"FC12-REFUSED-OK", "FC12-WORKER-ENDED", "FC12-RETAINED-OK"},
		},
	},
}

var reOrder = regexp.MustCompile(`restore=(\d+) release\(hb\)=(\d+) followup-issued=(\d+)`)

func eventOrder(out, marker string, ok func(restore, release, followup int) bool) error {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		m := reOrder.FindStringSubmatch(line)
		if m == nil {
			return fmt.Errorf("%s without an order: %s", marker, line)
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		if !ok(a, b, c) {
			return fmt.Errorf("%s: the order is not the variant's: %s", marker, line)
		}
		return nil
	}
	return fmt.Errorf("no %s marker", marker)
}

// composedOverlay builds the composed stage's overlay over the staged SDK
// and external/spotify, from the runner's own copies of testdata.
func (r *runner) composedOverlay(step int, staged string) (string, error) {
	replace := map[string]string{}
	for name, target := range composedFiles {
		local := filepath.Join(r.dir, "contract", name)
		if _, err := os.Stat(local); err != nil {
			return "", &stepError{step, "composed overlay source missing: " + err.Error(), ""}
		}
		where, rel, _ := strings.Cut(target, ":")
		switch where {
		case "sdk":
			replace[filepath.Join(staged, filepath.FromSlash(rel))] = local
		case "app":
			replace[filepath.Join(r.repo, "external", "spotify", rel)] = local
		}
	}
	data, _ := json.Marshal(map[string]any{"Replace": replace})
	path := filepath.Join(r.dir, "composed-overlay.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", &stepError{step, err.Error(), ""}
	}
	return path, nil
}

// goTestJSON runs go test -json in the repository with the scratch modfile
// and the composed overlay, bounded, and returns its output and exit status.
// A non-zero exit is not an error here: a child control fails on purpose.
func (r *runner) goTestJSON(scratch, overlay string, limit time.Duration, run string, extraEnv []string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.goBin, "test", "-modfile="+filepath.Join(scratch, "go.mod"), "-overlay="+overlay,
		"-race", "-count=1", "-json", "-timeout", (limit - time.Minute).String(), "-run", run, "./external/spotify")
	cmd.Dir, cmd.Env = r.repo, append(slices.Clone(r.env), extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return stdout.Bytes(), -1, fmt.Errorf("killed at its deadline: stderr:\n%s", stderr.String())
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errorsAs(err, &ee) {
			return stdout.Bytes(), -1, fmt.Errorf("%v: stderr:\n%s", err, stderr.String())
		}
		code = ee.ExitCode()
	}
	if stderr.Len() != 0 && code != 0 && !bytes.Contains(stdout.Bytes(), []byte(`"Action"`)) {
		return stdout.Bytes(), code, fmt.Errorf("no test output: stderr:\n%s", stderr.String())
	}
	return stdout.Bytes(), code, nil
}

func errorsAs(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

// testRecord is what go test -json said about each test and subtest.
type testRecord struct {
	results map[string]string
	output  map[string]*strings.Builder
	all     strings.Builder
}

func readTestJSON(out []byte) *testRecord {
	rec := &testRecord{results: map[string]string{}, output: map[string]*strings.Builder{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct{ Action, Test, Output string }
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		rec.all.WriteString(ev.Output)
		if ev.Test == "" {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			rec.results[ev.Test] = ev.Action
		case "output":
			b := rec.output[ev.Test]
			if b == nil {
				b = &strings.Builder{}
				rec.output[ev.Test] = b
			}
			b.WriteString(ev.Output)
		}
	}
	return rec
}

var composedForbidden = []string{"WARNING: DATA RACE", "[build failed]", "[setup failed]", "panic: "}

// composed runs steps 13 and 14.
func (r *runner) composed(scratch, staged string) error {
	// 13. The bypass binding: the overlay's bytes are the bound ones, and the
	// binding check (FC8) passes against the shipped source.
	data, err := os.ReadFile(filepath.Join(r.dir, "contract", "composed_bypass_test.go.txt"))
	if err != nil {
		return &stepError{13, err.Error(), ""}
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != bypassSHA256 {
		return &stepError{13, fmt.Sprintf("the bypass overlay's SHA-256 is %x, the bound one is %s", sum, bypassSHA256), ""}
	}
	overlay, err := r.composedOverlay(13, staged)
	if err != nil {
		return err
	}
	out, code, err := r.goTestJSON(scratch, overlay, 10*time.Minute, "^TestDDSonicComposedFC8BypassBinding$", nil)
	rec := readTestJSON(out)
	if err != nil || code != 0 || rec.results["TestDDSonicComposedFC8BypassBinding"] != "pass" ||
		!strings.Contains(rec.output["TestDDSonicComposedFC8BypassBinding"].String(), "FC8-BINDING-OK") {
		return &stepError{13, fmt.Sprintf("the bypass binding did not pass (exit %d, %v)", code, err), rec.all.String()}
	}
	r.notes = append(r.notes, "step 13: the bypass overlay is bound ("+bypassSHA256[:12]+"…) and FC8 passed")

	// 14. The composed stage: the parent run, then each child control.
	out, code, err = r.goTestJSON(scratch, overlay, 45*time.Minute, "^TestDDSonicComposed", nil)
	rec = readTestJSON(out)
	if err != nil || code != 0 {
		return &stepError{14, fmt.Sprintf("the composed stage failed (exit %d, %v)", code, err), rec.all.String()}
	}
	for _, f := range composedForbidden {
		if strings.Contains(rec.all.String(), f) {
			return &stepError{14, "the composed stage's output holds " + f, rec.all.String()}
		}
	}
	for name, res := range rec.results {
		if res != "pass" {
			return &stepError{14, name + " did not pass: " + res, rec.all.String()}
		}
	}
	for _, want := range composedParentTests {
		if rec.results[want] != "pass" {
			return &stepError{14, want + " was not shown to pass", rec.all.String()}
		}
	}
	parents, err := checkComposedParents(out)
	if err != nil {
		return &stepError{14, "the composed stage's exact parent set: " + err.Error(), rec.all.String()}
	}
	r.notes = append(r.notes, "step 14: "+parents)
	// R30-1: no allowed child log can admit an assertion failure.
	sources := map[string][]byte{}
	for _, name := range []string{"composed_harness_test.go.txt", "composed_controls_test.go.txt"} {
		data, err := os.ReadFile(filepath.Join(r.dir, "contract", name))
		if err != nil {
			return &stepError{14, err.Error(), ""}
		}
		sources[name] = data
	}
	note, err := composedAllowedLogsDisjoint(composedChildren, sources)
	if err != nil {
		return &stepError{14, "an allowed child log aliases an assertion: " + err.Error(), ""}
	}
	r.notes = append(r.notes, "step 14: the allowed child logs are markers disjoint from every assertion ("+note+")")
	children := slices.Sorted(func(yield func(string) bool) {
		for k := range composedChildren {
			if !yield(k) {
				return
			}
		}
	})
	for _, name := range children {
		if err := r.composedChild(scratch, overlay, name, composedChildren[name]); err != nil {
			return err
		}
	}
	r.notes = append(r.notes, fmt.Sprintf("step 14: %d composed tests passed; %d child controls had their exact outcomes", len(composedParentTests), len(composedChildren)))
	return nil
}

func (r *runner) composedChild(scratch, overlay, name string, want childExpect) error {
	out, code, err := r.goTestJSON(scratch, overlay, 5*time.Minute, "^"+name+"$", nil)
	if err != nil {
		return &stepError{14, fmt.Sprintf("child %s: the child run did not complete (exit %d, %v)", name, code, err), string(out)}
	}
	if cerr := checkChild(want, out, code); cerr != nil {
		return &stepError{14, fmt.Sprintf("child %s: %v (exit %d)", name, cerr, code), readTestJSON(out).all.String()}
	}
	return nil
}

// reMessage is a test's message line: "<file>.go:<line>: <message>".
var reMessage = regexp.MustCompile(`^\s+[^\s:]+\.go:\d+: (.*)$`)

// checkChild is the exact outcome check (plan v7 §9; R29-1).
func checkChild(want childExpect, out []byte, code int) error {
	rec := readTestJSON(out)
	fail := func(what string) error { return errors.New(what) }
	all := rec.all.String()
	for _, f := range append(slices.Clone(composedForbidden), want.forbid...) {
		if strings.Contains(all, f) {
			return fail("its output holds " + f)
		}
	}
	for test, res := range want.results {
		if rec.results[test] != res {
			return fail(fmt.Sprintf("%s ended %q, want %q", test, rec.results[test], res))
		}
	}
	for test := range rec.results {
		if _, ok := want.results[test]; !ok {
			return fail("an unexpected test ran: " + test)
		}
	}
	// Every message line is an allowed log or an expected failure; expected
	// failures occur exactly as often as stated (R29-1).
	for test := range want.results {
		counts := map[string]int{}
		var text string
		if b := rec.output[test]; b != nil {
			text = b.String()
		}
		for _, line := range strings.Split(text, "\n") {
			m := reMessage.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			msg, matched := m[1], false
			for prefix := range want.failures[test] {
				if strings.HasPrefix(msg, prefix) {
					counts[prefix]++
					matched = true
					break
				}
			}
			for _, prefix := range want.logs[test] {
				if !matched && strings.HasPrefix(msg, prefix) {
					matched = true
				}
			}
			if !matched { // MUTATION-POINT R29-1
				return fail(fmt.Sprintf("%s wrote a message outside its exact set (an extra assertion or log): %q", test, msg))
			}
		}
		for prefix, n := range want.failures[test] {
			if counts[prefix] != n {
				return fail(fmt.Sprintf("%s: expected failure %q occurred %d time(s), want exactly %d", test, prefix, counts[prefix], n))
			}
		}
	}
	for test, markers := range want.markers {
		b := rec.output[test]
		for _, m := range markers {
			if b == nil || !strings.Contains(b.String(), m) {
				return fail(fmt.Sprintf("%s's output lacks %q", test, m))
			}
		}
	}
	if want.check != nil {
		if cerr := want.check(all); cerr != nil {
			return fail(cerr.Error())
		}
	}
	wantCode := 0
	for _, res := range want.results {
		if res == "fail" {
			wantCode = 1
		}
	}
	if code != wantCode {
		return fail(fmt.Sprintf("exit %d, want %d", code, wantCode))
	}
	return nil
}

// reMarker is an allowed child log's form: an upper-case, hyphenated marker
// token, alone or followed by a space (R30-1).
var reMarker = regexp.MustCompile(`^[A-Z0-9]+(-[A-Z0-9]+)+( |$)`)

// leadingLiteral is the string literal a message argument begins with, and
// whether that literal is the whole message.
func leadingLiteral(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			if s, err := strconv.Unquote(x.Value); err == nil {
				return s, true
			}
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			s, _ := leadingLiteral(x.X)
			return s, false
		}
	}
	return "", false
}

// composedAllowedLogsDisjoint is R30-1's fail-closed check: every allowed
// child log is a marker, and no assertion in the composed sources (a call of
// Error, Errorf, Fatal or Fatalf whose message begins with a literal) can
// begin with one. So an allowed log can never admit a real assertion
// failure. A message with no leading literal (an error value) is not a
// marker by construction, and is reported in the note.
func composedAllowedLogsDisjoint(children map[string]childExpect, sources map[string][]byte) (note string, err error) {
	type assertion struct {
		where    string
		prefix   string // the literal up to its first verb
		complete bool   // the literal is the whole message
	}
	var asserts []assertion
	dynamic := 0
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range sources {
			if !yield(k) {
				return
			}
		}
	}) {
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, name, sources[name], 0)
		if perr != nil {
			return "", fmt.Errorf("%s: %v", name, perr)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			formatted := false
			switch sel.Sel.Name {
			case "Error", "Fatal":
			case "Errorf", "Fatalf":
				formatted = true
			default:
				return true
			}
			lit, whole := leadingLiteral(call.Args[0])
			if lit == "" {
				dynamic++
				return true
			}
			a := assertion{where: fset.Position(call.Pos()).String(), prefix: lit, complete: whole && len(call.Args) == 1}
			if formatted {
				if i := strings.IndexByte(lit, '%'); i >= 0 {
					a.prefix, a.complete = lit[:i], false
				} else {
					a.complete = whole
				}
			}
			asserts = append(asserts, a)
			return true
		})
	}
	for child, want := range children {
		for test, logs := range want.logs {
			for _, l := range logs {
				if !reMarker.MatchString(l) {
					return "", fmt.Errorf("%s: %s's allowed log %q is not a marker", child, test, l)
				}
				for _, a := range asserts {
					if strings.HasPrefix(a.prefix, l) || (!a.complete && a.prefix != "" && strings.HasPrefix(l, a.prefix)) {
						return "", fmt.Errorf("%s: %s's allowed log %q can begin the assertion at %s (%q)", child, test, l, a.where, a.prefix)
					}
				}
			}
		}
	}
	return fmt.Sprintf("%d literal assertions checked against the allowed logs; %d with no leading literal", len(asserts), dynamic), nil
}

// childJSON builds go test -json output for checkChild's own control.
func childJSON(events ...[3]string) []byte {
	var b bytes.Buffer
	for _, e := range events {
		data, _ := json.Marshal(map[string]string{"Action": e[0], "Test": e[1], "Output": e[2]})
		b.Write(append(data, '\n'))
	}
	return b.Bytes()
}

// TestComposedChildOutcomeIsExact is checkChild's fail-closed control
// (R29-1): FC11's real expected failure passes; the same failure with one
// more, independent assertion failure is refused, as are a missing and a
// doubled expected failure.
func TestComposedChildOutcomeIsExact(t *testing.T) {
	const name = "TestDDSonicChildFC11"
	want := composedChildren[name]
	expected := "    zz_ddsonic_composed_controls_test.go:717: ZZ: router TestDDSonicChildFC11: an undeclared late use: #1 GET control.composed.invalid/followup/undeclared\n"
	base := [][3]string{{"run", name, ""}, {"output", name, "=== RUN   " + name + "\n"}, {"output", name, expected}}
	end := [][3]string{{"output", name, "--- FAIL: " + name + " (0.03s)\n"}, {"fail", name, ""}}
	run := func(middle ...[3]string) []byte {
		var ev [][3]string
		ev = append(append(append(ev, base...), middle...), end...)
		return childJSON(ev...)
	}
	if err := checkChild(want, run(), 1); err != nil {
		t.Fatalf("the expected outcome was refused: %v", err)
	}
	extra := [3]string{"output", name, "    zz_ddsonic_composed_controls_test.go:719: unexpected independent assertion failure\n"}
	if err := checkChild(want, run(extra), 1); err == nil {
		t.Error("an extra, independent assertion failure inside the expected-FAIL child was accepted")
	}
	if err := checkChild(want, run([3]string{"output", name, expected}), 1); err == nil {
		t.Error("a doubled expected failure was accepted")
	}
	missing := childJSON([3]string{"run", name, ""}, [3]string{"output", name, "=== RUN   " + name + "\n"}, [3]string{"fail", name, ""})
	if err := checkChild(want, missing, 1); err == nil {
		t.Error("a child without its expected failure was accepted")
	}
}

// TestComposedChildFC12RefusesP0Assertion is R30-1's regression control: a
// real-shaped FC12 run is accepted; the same run with the real P0 count
// assertion's failure is refused, never admitted as an allowed log.
func TestComposedChildFC12RefusesP0Assertion(t *testing.T) {
	const (
		name   = "TestDDSonicChildFC12"
		holder = name + "/holder"
		after  = name + "/after"
	)
	want := composedChildren[name]
	line := func(test, msg string) [3]string {
		return [3]string{"output", test, "    zz_ddsonic_composed_controls_test.go:1: " + msg + "\n"}
	}
	run := func(extra ...[3]string) []byte {
		ev := [][3]string{
			{"run", name, ""}, {"run", holder, ""},
			line(holder, "P0-COUNTS-OBSERVED A1=1 A2=3 A3=1 A7=1 A8=0 D1=1 D5=0 accepted=1 logins=1"),
		}
		ev = append(ev, extra...)
		ev = append(ev,
			line(holder, "FC12-PRE-OK"), line(holder, "FC12-C1-OK"),
			line(holder, "FC12-CHECKPOINT-OBSERVED key=fixture lock=held transport=router router=poisoned"),
			line(holder, "FC12-CHECKPOINT-OK"),
			line(holder, "composed cleanup failed at T5b (builds); branch F: router poisoned, fixtures stopped, nothing restored, slot kept"),
			[3]string{"fail", holder, ""},
			[3]string{"run", after, ""},
			line(after, "FC12-REFUSED-OK"), line(after, "FC12-WORKER-ENDED context canceled"), line(after, "FC12-RETAINED-OK"),
			[3]string{"pass", after, ""}, [3]string{"fail", name, ""})
		return childJSON(ev...)
	}
	if err := checkChild(want, run(), 1); err != nil {
		t.Fatalf("the real-shaped FC12 outcome was refused: %v", err)
	}
	// The real assertion (composed_controls_test.go.txt's p0), as it fails.
	p0Failure := line(holder, "P0 counts: A1=2 A2=3 A3=1 A7=1 A8=0, want A1=1 A2=3 A3=1 A7=1 A8=0 (plan v7 §3.2)")
	if err := checkChild(want, run(p0Failure), 1); err == nil {
		t.Error("a real P0 count assertion failure inside FC12's holder was accepted as an allowed log")
	}
}

// TestComposedAllowedLogsAreNotAssertions is R30-1's static control: the
// real allowed logs are markers disjoint from every real assertion; a
// non-marker allowed log, and a marker that begins an assertion, are both
// refused.
func TestComposedAllowedLogsAreNotAssertions(t *testing.T) {
	sources := map[string][]byte{}
	for _, name := range []string{"composed_harness_test.go.txt", "composed_controls_test.go.txt"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		sources[name] = data
	}
	note, err := composedAllowedLogsDisjoint(composedChildren, sources)
	if err != nil {
		t.Fatalf("the real allowed logs alias an assertion: %v", err)
	}
	t.Logf("R30-1-DISJOINT %s", note)
	src := map[string][]byte{"x.go": []byte("package x\nfunc f(t T) { t.Errorf(\"P0 counts: %s, want %s\", a, b); t.Errorf(\"FC99-OK but wrong: %d\", n) }\n")}
	alias := map[string]childExpect{"C": {logs: map[string][]string{"C": {"P0 counts: "}}}}
	if _, err := composedAllowedLogsDisjoint(alias, src); err == nil {
		t.Error("a non-marker allowed log that begins an assertion was accepted")
	}
	marker := map[string]childExpect{"C": {logs: map[string][]string{"C": {"FC99-OK"}}}}
	if _, err := composedAllowedLogsDisjoint(marker, src); err == nil {
		t.Error("an allowed marker that begins an assertion was accepted")
	}
}

// TestComposedParentSetIsExact is checkComposedParents' fail-closed control
// (plan v9 §8 I12, §9; review 75 §1): the 61 listed parents, each started
// once and passed, are accepted with E1 NOT COVERED, and with an admitted
// E1 that passed; a missing, extra (the mutant-only I1Ordered, an
// unlisted name), skipped, failed, incomplete or duplicated parent, and a
// failed E1, are each refused.
func TestComposedParentSetIsExact(t *testing.T) {
	const ordered = "TestDDSonicComposedI1Ordered"
	run := func(drop string, extra ...[3]string) []byte {
		var ev [][3]string
		for _, name := range composedParentTests {
			if name != drop {
				ev = append(ev, [3]string{"run", name, ""}, [3]string{"pass", name, ""})
			}
		}
		return childJSON(append(ev, extra...)...)
	}
	if len(composedParentTests) != 61 {
		t.Fatalf("the parent list has %d names, plan v9 §9 has 61", len(composedParentTests))
	}
	note, err := checkComposedParents(run(""))
	if err != nil || !strings.Contains(note, "E1 NOT COVERED") {
		t.Fatalf("the exact passing set was refused or E1 not reported NOT COVERED: %q, %v", note, err)
	}
	note, err = checkComposedParents(run("", [3]string{"run", composedE1, ""}, [3]string{"pass", composedE1, ""}))
	if err != nil || !strings.Contains(note, "E1 passed") {
		t.Fatalf("the exact passing set with an admitted, passing E1 was refused: %q, %v", note, err)
	}
	last := composedParentTests[len(composedParentTests)-1]
	refused := map[string][]byte{
		"a missing parent":              run("TestDDSonicComposedF1c"),
		"the mutant-only I1Ordered":     run("", [3]string{"run", ordered, ""}, [3]string{"pass", ordered, ""}),
		"an unlisted parent, never run": run("", [3]string{"pass", "TestDDSonicComposedX", ""}),
		"a skipped parent":              run("TestDDSonicComposedI22", [3]string{"run", "TestDDSonicComposedI22", ""}, [3]string{"skip", "TestDDSonicComposedI22", ""}),
		"a failed parent":               run("TestDDSonicComposedI27Audit", [3]string{"run", "TestDDSonicComposedI27Audit", ""}, [3]string{"fail", "TestDDSonicComposedI27Audit", ""}),
		"an incomplete parent":          run("TestDDSonicComposedI30", [3]string{"run", "TestDDSonicComposedI30", ""}),
		"a parent started twice":        run("", [3]string{"run", last, ""}),
		"a parent ended twice":          run("", [3]string{"pass", last, ""}),
		"a failed E1":                   run("", [3]string{"run", composedE1, ""}, [3]string{"fail", composedE1, ""}),
	}
	for what, out := range refused {
		if _, err := checkComposedParents(out); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	// R76-3: the whole output is read as events. The exact passing set
	// followed by a malformed, truncated or Action-less line is refused.
	whole := run("")
	malformed := map[string][]byte{
		"a non-JSON line after the set":   append(slices.Clone(whole), "not a test2json event\n"...),
		"a truncated event after the set": append(slices.Clone(whole), `{"Action":"pa`...),
		"an Action-less JSON line":        append(slices.Clone(whole), "{}\n"...),
	}
	for what, out := range malformed {
		if _, err := checkComposedParents(out); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
	// A line longer than the reader may take stops it early: the suffix is
	// never read, and the run is refused rather than accepted on its prefix;
	// the same set under the same small limit, with no such line, passes.
	long := append(slices.Clone(whole), `{"Action":"output","Output":"`+strings.Repeat("x", 4096)+`"}`+"\n"...)
	if _, err := checkComposedParentsMax(long, 1024); err == nil {
		t.Error("an unread suffix (a line over the reader's limit) was accepted")
	}
	if _, err := checkComposedParentsMax(whole, 1024); err != nil {
		t.Errorf("the exact passing set was refused under the small line limit: %v", err)
	}
}
