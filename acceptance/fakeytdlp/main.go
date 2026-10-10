// Command fakeytdlp stands in for yt-dlp in the deterministic acceptance
// cases. The harness installs it as "yt-dlp", the only one on the player's
// PATH.
//
// It fails closed. It answers an invocation only when its arguments are,
// exactly and in order, ones the case scripted; anything else exits with an
// error and is recorded as rejected, so a case cannot pass because the fake
// quietly accepted something new. Every invocation is logged, answered or
// not. It never runs the real yt-dlp and never touches the network.
//
// What it does not show: that the real yt-dlp, YouTube or a signed-in
// account behave as the fixtures say. Live acceptance covers that.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Response is one scripted answer.
type Response struct {
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr,omitempty"`
	Exit   int      `json:"exit,omitempty"`
}

// Request is one logged invocation. Exactly one of three things happened to
// it: it was answered (Answered), it was rejected as unscripted (neither
// field set), or the fake itself could not work (Failure says why).
type Request struct {
	Time     string   `json:"time"`
	Args     []string `json:"args"`
	Answered bool     `json:"answered"`
	Failure  string   `json:"failure,omitempty"`
}

const (
	exitUnscripted    = 97
	exitNotConfigured = 96
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake yt-dlp:", err)
		os.Exit(exitNotConfigured)
	}
	// The harness lays the fixture directory beside the bin directory, and
	// supervises the fake under the name of the directory that holds both.
	base := filepath.Dir(filepath.Dir(exe))
	os.Exit(run(filepath.Join(base, "ytdlp"), os.Args[1:], os.Stdout, os.Stderr, supervisor(base)))
}

// Event is what the fake tells its supervisor about an invocation: that it
// has begun, and how it ended.
type Event struct {
	Phase    string   `json:"phase"` // "begin" or "end"
	Args     []string `json:"args"`
	Answered bool     `json:"answered,omitempty"`
	Failure  string   `json:"failure,omitempty"`
	Exit     int      `json:"exit,omitempty"`
}

// SupervisorAddress is where the harness listens for the fake installed
// under base: an abstract Unix socket. It is not a file. No directory's
// permissions, no full disk and no removed fixture can keep the fake from
// reaching it, which is the point: the files the fake keeps (its log, its
// attempt markers, its failure notes) are all in places a case, or a fault,
// can make unwritable, and a failure to write them must still be heard.
func SupervisorAddress(base string) string { return "@acceptance-fake-yt-dlp:" + base }

// supervisor returns the function that reports an event to the harness and
// waits until the harness has taken it.
func supervisor(base string) func(Event) error {
	return func(ev Event) error {
		conn, err := net.DialTimeout("unix", SupervisorAddress(base), 5*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := conn.Write(append(line, '\n')); err != nil {
			return err
		}
		ack := make([]byte, 3)
		if _, err := io.ReadFull(conn, ack); err != nil || string(ack) != "ok\n" {
			return fmt.Errorf("the supervisor did not acknowledge: %q, %v", ack, err)
		}
		return nil
	}
}

// run answers one invocation from the fixture directory dir and returns the
// exit status. Whatever happens, the invocation is accounted for: a fake
// that cannot read its script, or cannot write its log, must not look, to
// the player and to the case, like a provider that is down.
//
// The harness hears of every invocation directly: report tells it that the
// invocation has begun before anything else is done, and how it ended
// before anything is answered. If the harness cannot be told, the fake
// answers nothing. That channel does not depend on any file.
//
// The files are kept as well, as evidence. An attempt marker is left in
// dir/attempts and removed only once the invocation is in the log, and
// every failure of the fake's own is noted in a file nothing rewrites
// (noteFailure).
func run(dir string, args []string, stdout, stderr io.Writer, report func(Event) error) int {
	unheard := func(err error) int {
		noteFailure(dir, fmt.Sprintf("cannot reach the supervisor: %v (invocation %q)", err, args))
		fmt.Fprintf(stderr, "fake yt-dlp: harness failure: cannot reach the supervisor: %v\n", err)
		return exitNotConfigured
	}
	if err := report(Event{Phase: "begin", Args: args}); err != nil {
		return unheard(err)
	}
	answer, failure := serve(dir, args)
	end := Event{Phase: "end", Args: args, Answered: answer != nil, Failure: failure, Exit: exitUnscripted}
	switch {
	case failure != "":
		end.Exit = exitNotConfigured
	case answer != nil:
		end.Exit = answer.Exit
	}
	if err := report(end); err != nil {
		return unheard(err)
	}
	switch {
	case failure != "":
		fmt.Fprintln(stderr, "fake yt-dlp: harness failure:", failure)
	case answer == nil:
		fmt.Fprintf(stderr, "ERROR: fake yt-dlp: unscripted invocation %q\n", args)
	default:
		fmt.Fprint(stdout, answer.Stdout)
		fmt.Fprint(stderr, answer.Stderr)
	}
	return end.Exit
}

// serve finds the scripted answer to args and records the invocation in the
// files. It returns the answer, nil for an unscripted invocation, or what
// went wrong with the fake itself.
func serve(dir string, args []string) (answer *Response, failure string) {
	fail := func(what string, err error) (*Response, string) {
		text := fmt.Sprintf("%s: %v", what, err)
		noteFailure(dir, fmt.Sprintf("%s (invocation %q)", text, args))
		return nil, text
	}
	attempt, err := beginAttempt(dir)
	if err != nil {
		return fail("cannot mark the attempt", err)
	}
	request := Request{Time: time.Now().UTC().Format(time.RFC3339Nano), Args: args}
	responses, loadErr := load(dir)
	if loadErr != nil {
		request.Failure = loadErr.Error()
	} else {
		for i := range responses {
			if slices.Equal(responses[i].Args, args) {
				answer = &responses[i]
				break
			}
		}
		request.Answered = answer != nil
	}
	if err := record(dir, request); err != nil {
		return fail("cannot record the request", err) // the attempt marker stays
	}
	_ = os.Remove(attempt)
	if loadErr != nil {
		return nil, loadErr.Error()
	}
	return answer, ""
}

// beginAttempt leaves a marker saying an invocation has started.
func beginAttempt(dir string) (string, error) {
	attempts := filepath.Join(dir, "attempts")
	if err := os.MkdirAll(attempts, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(attempts, "invocation-")
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// noteFailure appends a failure of the fake's own to a file that is never
// rewritten: harness-failures.log in the fixture directory, or, if that
// cannot be written either, beside the executable.
func noteFailure(dir, text string) {
	for _, path := range []string{
		filepath.Join(dir, "harness-failures.log"),
		filepath.Join(filepath.Dir(dir), "bin", "harness-failures.log"),
	} {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			continue
		}
		_, werr := fmt.Fprintln(f, time.Now().UTC().Format(time.RFC3339Nano), text)
		if cerr := f.Close(); werr == nil && cerr == nil {
			return
		}
	}
}

// load reads the scripted responses.
func load(dir string) ([]Response, error) {
	data, err := os.ReadFile(filepath.Join(dir, "responses.json"))
	if err != nil {
		return nil, fmt.Errorf("no scripted responses: %w", err)
	}
	var responses []Response
	if err := json.Unmarshal(data, &responses); err != nil {
		return nil, fmt.Errorf("responses.json is malformed: %w", err)
	}
	return responses, nil
}

// record appends one line to the request log. O_APPEND keeps concurrent
// invocations' lines whole.
func record(dir string, r Request) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	// The directory is made if it is gone, so that even then there is a
	// record of what was asked.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
