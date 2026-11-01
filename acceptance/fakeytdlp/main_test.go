package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// heard stands in for the supervisor in the tests that are not about it.
func heard(Event) error { return nil }

// script writes dir's responses.json.
func script(t *testing.T, dir string, responses []Response) {
	t.Helper()
	data, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "responses.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func logged(t *testing.T, dir string) []Request {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "requests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var requests []Request
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var r Request
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, r)
	}
	return requests
}

// The fake answers an invocation only when its arguments are exactly the
// scripted ones, refuses everything else, and logs both.
func TestFakeFailsClosed(t *testing.T) {
	scripted := []string{"--flat-playlist", "-J", "--", "https://example.invalid/list"}
	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout string
		wantStderr string
		answered   bool
	}{
		{"the scripted invocation", scripted, 0, "{\"ok\":true}\n", "", true},
		{"a scripted failure", []string{"-j", "--", "https://example.invalid/down"}, 1, "", "ERROR: 503", true},
		{"one argument more", append([]string{"--verbose"}, scripted...), exitUnscripted, "", "unscripted invocation", false},
		{"one argument fewer", scripted[1:], exitUnscripted, "", "unscripted invocation", false},
		{"the same arguments in another order", []string{"-J", "--flat-playlist", "--", "https://example.invalid/list"}, exitUnscripted, "", "unscripted invocation", false},
		{"another address", []string{"--flat-playlist", "-J", "--", "https://example.invalid/other"}, exitUnscripted, "", "unscripted invocation", false},
		{"--version", []string{"--version"}, exitUnscripted, "", "unscripted invocation", false},
		{"no arguments", nil, exitUnscripted, "", "unscripted invocation", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			script(t, dir, []Response{
				{Args: scripted, Stdout: "{\"ok\":true}\n"},
				{Args: []string{"-j", "--", "https://example.invalid/down"}, Stderr: "ERROR: 503\n", Exit: 1},
			})
			var stdout, stderr bytes.Buffer
			exit := run(dir, tt.args, &stdout, &stderr, heard)
			if exit != tt.wantExit || stdout.String() != tt.wantStdout || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr containing %q",
					exit, stdout.String(), stderr.String(), tt.wantExit, tt.wantStdout, tt.wantStderr)
			}
			requests := logged(t, dir)
			if len(requests) != 1 || requests[0].Answered != tt.answered || len(requests[0].Args) != len(tt.args) {
				t.Errorf("request log = %+v, want this one invocation with answered=%v", requests, tt.answered)
			}
		})
	}
}

// Not scripted at all, or scripted with a file it cannot read, the fake
// answers nothing: it never guesses. And it says so in its log, as a failure
// of the harness and not as an answer or a rejection, so that the case can
// tell a broken fake from a provider that is down.
func TestFakeRecordsItsOwnFailures(t *testing.T) {
	missing := t.TempDir()
	malformed := t.TempDir()
	if err := os.WriteFile(filepath.Join(malformed, "responses.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(t.TempDir(), "removed", "ytdlp") // the fixture directory itself is gone
	for name, dir := range map[string]string{"no responses.json": missing, "a malformed responses.json": malformed, "no fixture directory": gone} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := run(dir, []string{"-J", "--", "https://example.invalid/liked"}, &stdout, &stderr, heard)
			if exit != exitNotConfigured || stdout.Len() != 0 || !strings.Contains(stderr.String(), "harness failure") {
				t.Errorf("exit %d, stdout %q, stderr %q; want %d, nothing, and a harness failure", exit, stdout.String(), stderr.String(), exitNotConfigured)
			}
			requests := logged(t, dir)
			if len(requests) != 1 || requests[0].Answered || requests[0].Failure == "" || len(requests[0].Args) != 3 {
				t.Errorf("request log = %+v, want this invocation recorded as a harness failure", requests)
			}
		})
	}
	// An empty script is a script: it answers nothing, and that is a
	// rejection, not a failure of the fake.
	dir := t.TempDir()
	script(t, dir, []Response{})
	var stdout, stderr bytes.Buffer
	if exit := run(dir, []string{"-J"}, &stdout, &stderr, heard); exit != exitUnscripted {
		t.Errorf("with an empty script: exit %d", exit)
	}
	if requests := logged(t, dir); len(requests) != 1 || requests[0].Answered || requests[0].Failure != "" {
		t.Errorf("request log = %+v, want a rejection", requests)
	}
	// A scripted failure is an answer.
	script(t, dir, []Response{{Args: []string{"-J"}, Exit: 1, Stderr: "ERROR: 503\n"}})
	if exit := run(dir, []string{"-J"}, &stdout, &stderr, heard); exit != 1 {
		t.Errorf("a scripted failure: exit %d", exit)
	}
	if requests := logged(t, dir); len(requests) != 2 || !requests[1].Answered || requests[1].Failure != "" {
		t.Errorf("request log = %+v, want the scripted failure recorded as answered", requests)
	}
}

// Round 4, M9: when the fake cannot record an invocation, that is itself
// recorded somewhere else, to stay: a request log that works again later
// does not erase it, and neither does a later invocation that goes well.
func TestFakeFailureToRecordIsSticky(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full to make a write fail with")
	}
	args := []string{"-J", "--", "https://example.invalid/liked"}
	for name, breakLog := range map[string]func(log string) error{
		"the log cannot be written (no space)": func(log string) error { return os.Symlink("/dev/full", log) },
		"the log cannot be opened":             func(log string) error { return os.Mkdir(log, 0o700) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			script(t, dir, []Response{{Args: args, Stdout: "{}\n"}})
			log := filepath.Join(dir, "requests.jsonl")
			var stdout, stderr bytes.Buffer
			if exit := run(dir, args, &stdout, &stderr, heard); exit != 0 {
				t.Fatalf("a first, good invocation: exit %d, %s", exit, stderr.String())
			}
			saved, _ := os.ReadFile(log)

			// The log breaks, an invocation happens, the log is put back.
			os.Remove(log)
			if err := breakLog(log); err != nil {
				t.Fatal(err)
			}
			stdout.Reset()
			if exit := run(dir, args, &stdout, &stderr, heard); exit != exitNotConfigured || stdout.Len() != 0 {
				t.Errorf("with the log broken: exit %d, stdout %q; want %d and no answer", exit, stdout.String(), exitNotConfigured)
			}
			os.RemoveAll(log)
			os.WriteFile(log, saved, 0o600)
			if exit := run(dir, args, &stdout, &stderr, heard); exit != 0 {
				t.Fatalf("after the log is restored: exit %d", exit)
			}

			// The log now looks clean: two answered invocations.
			if requests := logged(t, dir); len(requests) != 2 || !requests[0].Answered || !requests[1].Answered {
				t.Fatalf("request log = %+v", requests)
			}
			// What happened in between is still on record, elsewhere.
			if left := unaccounted(dir); len(left) == 0 {
				t.Error("nothing records the invocation that could not be logged")
			}
		})
	}
}

// unaccounted is everything the fake left to say an invocation was not
// accounted for: attempts with no record, and its sticky failure notes.
func unaccounted(dir string) []string {
	var left []string
	for _, pattern := range []string{filepath.Join(dir, "attempts", "*"), filepath.Join(dir, "harness-failures*"), filepath.Join(filepath.Dir(dir), "bin", "*harness-failures*")} {
		found, _ := filepath.Glob(pattern)
		left = append(left, found...)
	}
	return left
}

// Round 5, M9: the supervisor hears of every invocation, how it began and
// how it ended, whatever becomes of the files; and a fake that cannot tell
// its supervisor answers nothing.
func TestFakeReportsEveryInvocationToItsSupervisor(t *testing.T) {
	scripted := []string{"-J", "--", "https://example.invalid/liked"}
	outage := []string{"-J", "--", "https://example.invalid/down"}
	dir := t.TempDir()
	script(t, dir, []Response{{Args: scripted, Stdout: "{}\n"}, {Args: outage, Exit: 1, Stderr: "ERROR: 503\n"}})
	var events []Event
	listen := func(ev Event) error { events = append(events, ev); return nil }
	var stdout, stderr bytes.Buffer

	run(dir, scripted, &stdout, &stderr, listen)
	run(dir, outage, &stdout, &stderr, listen)
	run(dir, []string{"--unscripted"}, &stdout, &stderr, listen)
	os.Remove(filepath.Join(dir, "responses.json"))
	run(dir, scripted, &stdout, &stderr, listen)
	want := []Event{
		{Phase: "begin"}, {Phase: "end", Answered: true, Exit: 0},
		{Phase: "begin"}, {Phase: "end", Answered: true, Exit: 1}, // a scripted outage is an answer
		{Phase: "begin"}, {Phase: "end", Exit: exitUnscripted},
		{Phase: "begin"}, {Phase: "end", Exit: exitNotConfigured},
	}
	if len(events) != len(want) {
		t.Fatalf("%d events, want %d: %+v", len(events), len(want), events)
	}
	for i, w := range want {
		got := events[i]
		if got.Phase != w.Phase || got.Answered != w.Answered || got.Exit != w.Exit || len(got.Args) == 0 {
			t.Errorf("event %d = %+v, want phase %q, answered %v, exit %d", i, got, w.Phase, w.Answered, w.Exit)
		}
	}
	if events[7].Failure == "" || events[5].Failure != "" || events[3].Failure != "" {
		t.Errorf("failures are not told apart from rejections and answers: %+v", events)
	}

	// A fake that cannot reach its supervisor answers nothing, even to an
	// invocation it has a perfectly good answer for.
	script(t, dir, []Response{{Args: scripted, Stdout: "{}\n"}})
	for name, deaf := range map[string]func(Event) error{
		"cannot report the beginning": func(Event) error { return os.ErrDeadlineExceeded },
		"cannot report the end": func(ev Event) error {
			if ev.Phase == "end" {
				return os.ErrDeadlineExceeded
			}
			return nil
		},
	} {
		stdout.Reset()
		if exit := run(dir, scripted, &stdout, &stderr, deaf); exit != exitNotConfigured || stdout.Len() != 0 {
			t.Errorf("%s: exit %d, stdout %q; want %d and no answer", name, exit, stdout.String(), exitNotConfigured)
		}
	}
}

// When neither the fixture directory nor the directory beside the
// executable can be written, no marker and no note can be left. The
// supervisor is told all the same.
func TestFakeTellsItsSupervisorWhenItCanWriteNothing(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	base := t.TempDir()
	dir, bin := filepath.Join(base, "ytdlp"), filepath.Join(base, "bin")
	for _, d := range []string{filepath.Join(dir, "attempts"), bin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-J", "--", "https://example.invalid/liked"}
	script(t, dir, []Response{{Args: args, Stdout: "{}\n"}})
	locked := []string{filepath.Join(dir, "attempts"), dir, bin}
	for _, d := range locked {
		os.Chmod(d, 0o500)
	}
	t.Cleanup(func() {
		for _, d := range locked {
			os.Chmod(d, 0o700)
		}
	})
	var events []Event
	var stdout, stderr bytes.Buffer
	exit := run(dir, args, &stdout, &stderr, func(ev Event) error { events = append(events, ev); return nil })
	for _, d := range locked {
		os.Chmod(d, 0o700)
	}
	if exit != exitNotConfigured || stdout.Len() != 0 {
		t.Errorf("exit %d, stdout %q; want %d and no answer", exit, stdout.String(), exitNotConfigured)
	}
	// Nothing could be left on disk: that is the situation.
	if left := unaccounted(dir); len(left) != 0 {
		t.Logf("something was written after all: %v", left)
	}
	if len(events) != 2 || events[1].Phase != "end" || events[1].Failure == "" || events[1].Answered {
		t.Errorf("the supervisor heard %+v, want the invocation's beginning and its failure", events)
	}
}
