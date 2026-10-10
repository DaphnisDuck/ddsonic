package acceptance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// TestOutcome is what "go test" reported for one test.
type TestOutcome struct {
	Status  string // "pass", "fail", "skip", or "" when the test never reported
	Output  string
	Elapsed float64
}

// TestRun is one "go test" run of one test, in a process of its own.
type TestRun struct {
	Test    string
	Outcome TestOutcome
	// ProcessFailed is set when the test's package or process failed: the
	// test failed, or TestMain did, or it crashed, or it did not build.
	ProcessFailed bool
	// Raw is the run's whole output, for the evidence bundle.
	Raw []byte
}

// RunGoTest runs one test of the package in dir (relative to repo), alone,
// in its own "go test" process, and returns what happened. env is added to
// the environment. With iso, the run happens inside the network isolation,
// and the isolation is verified for this very process tree before the
// result is returned; a run whose isolation cannot be verified is an error.
//
// One process per test is what makes the result trustworthy. The process's
// exit is then a signal about this test and nothing else: a pass it reports
// while the process fails means something around it went wrong (a TestMain,
// a crash, a harness that exits), and no other test's failure can be taken
// for the explanation, or hide one.
func RunGoTest(repo, dir, test string, iso *Isolation, env ...string) (TestRun, error) {
	argv := []string{"go", "test", "-json", "-count=1", "-run", "^" + regexp.QuoteMeta(test) + "$", "./" + dir}
	cmd := exec.Command(argv[0], argv[1:]...)
	proof := ""
	if iso != nil {
		var err error
		if cmd, proof, err = iso.Command(argv...); err != nil {
			return TestRun{Test: test}, err
		}
		defer os.Remove(proof)
		env = append(env, iso.Env()...)
	}
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()

	run := TestRun{Test: test, ProcessFailed: runErr != nil, Raw: append(out, stderr.Bytes()...)}
	if iso != nil {
		if err := iso.Verify(proof); err != nil {
			return run, fmt.Errorf("network isolation of %s in ./%s: %w", test, dir, err)
		}
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Action  string
			Test    string
			Output  string
			Elapsed float64
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Test == "" {
			if ev.Action == "fail" {
				run.ProcessFailed = true
			}
			continue
		}
		// A subtest's output belongs to its top-level test's story.
		top, _, isSub := strings.Cut(ev.Test, "/")
		if top != test {
			continue
		}
		switch ev.Action {
		case "output":
			run.Outcome.Output += ev.Output
		case "pass", "fail", "skip":
			if !isSub {
				run.Outcome.Status, run.Outcome.Elapsed = ev.Action, ev.Elapsed
			}
		}
	}
	return run, nil
}

// Result turns the run into a case's result.
//
//   - A reported failure is a failure, whatever else happened.
//   - A reported pass is a pass only if the process ended successfully too.
//     If it did not, the pass is not relied on: the case is blocked, and
//     what the test reported is kept as the detail.
//   - A skipped test could not reach a verdict in this environment: blocked.
//   - A test that reported nothing was not run, or, in a process that
//     failed, was prevented from running: blocked.
func (run TestRun) Result(caseID, executedBy string, at time.Time) Result {
	r := Result{CaseID: caseID, ExecutedBy: executedBy, ExecutedAt: at.UTC().Format(time.RFC3339), DurationSeconds: run.Outcome.Elapsed}
	around := "the test's process failed for a reason outside the test: " + lastLines(packageOutput(run.Raw), 6)
	switch run.Outcome.Status {
	case "fail":
		r.Execution, r.Detail = Fail, lastLines(run.Outcome.Output, 12)
	case "pass":
		r.Execution = Pass
		if run.ProcessFailed {
			r.Execution, r.Detail, r.EnvironmentLimitation = Blocked, "the test itself reported a pass", around
		}
	case "skip":
		r.Execution, r.EnvironmentLimitation = Blocked, lastLines(run.Outcome.Output, 4)
	default:
		r.Execution, r.Detail = NotRun, "the test did not report a result"
		if run.ProcessFailed {
			r.Execution, r.EnvironmentLimitation = Blocked, around
		}
	}
	return r
}

// packageOutput is the output "go test -json" attributes to the package
// itself and not to a test, plus anything that was not JSON at all.
func packageOutput(raw []byte) string {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct{ Action, Test, Output string }
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			b.WriteString(sc.Text() + "\n")
		} else if ev.Test == "" && ev.Action == "output" {
			b.WriteString(ev.Output)
		}
	}
	return b.String()
}

// lastLines is the last n non-empty lines of s, joined.
func lastLines(s string, n int) string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
