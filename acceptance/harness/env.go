// Package harness runs a ddsonic executable in an isolated environment and
// drives it the way a person does: keys into a terminal (tmux), the screen
// read back, and the player's own remote interface for what the screen does
// not show. The acceptance cases in acceptance/binarycases are written on it.
//
// The executable is given, never built here: DDSONIC_ACCEPTANCE_BINARY names
// it, set by the acceptance runner to the binary extracted from a release
// archive (or, in a development run, to a local build).
package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/acceptance"
	"github.com/bjarneo/cliamp/internal/appdir"
)

// The environment variables the acceptance runner sets for the cases. Each
// starts with the product's name in capitals, as its config variable does.
var (
	envPrefix = strings.ToUpper(appdir.Name) + "_ACCEPTANCE_"
	// BinaryEnv names the executable under test.
	BinaryEnv = envPrefix + "BINARY"
	// ArtifactsEnv names a directory to keep each case's captures in. Unset,
	// they go to the test's temporary directory and are dropped with it.
	ArtifactsEnv = envPrefix + "ARTIFACTS"
	// VersionEnv and BinarySHA256Env say which artifact the runner
	// identified: the version it must report and its checksum.
	VersionEnv      = envPrefix + "VERSION"
	BinarySHA256Env = envPrefix + "BINARY_SHA256"
	// SourceSHAEnv is the commit a release binary must report; empty for a
	// development build, which reports none.
	SourceSHAEnv = envPrefix + "SOURCE_SHA"
)

const (
	// waitTimeout bounds every wait for a condition. Waits poll; none sleeps
	// for a fixed time and hopes.
	waitTimeout = 20 * time.Second
	pollEvery   = 50 * time.Millisecond

	// maxRuntimePath keeps every runtime path far below the Unix socket limit
	// (107 bytes on Linux), so that limit is never what a case trips on.
	maxRuntimePath = 60
)

// Env is one isolated ddsonic installation: its own home, config, data,
// cache and music folders under a short base directory, its own terminal,
// no session bus, no audible output and no yt-dlp but the fake one.
type Env struct {
	t         testing.TB
	Binary    string
	Base      string
	Home      string
	Config    string // ddsonic's config directory
	Data      string // ddsonic's data directory
	Music     string
	Bin       string // the only directory on the player's PATH
	Artifacts string

	tmuxSocket string
	run        int // how many times the player has been started
	nsChecked  int // the run whose player's network namespace was checked
	lastScreen string
	fake       *FakeYTDLP // when installed: its rejections fail the case
}

// New prepares an isolated environment for the executable under test. With
// no executable named, or no tmux, the case cannot run here: it is skipped,
// which the acceptance runner reports as blocked, never as passed.
func New(t testing.TB) *Env {
	t.Helper()
	binary := os.Getenv(BinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set: no executable under test (run through `make acceptance`)", BinaryEnv)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("executable under test: %v", err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed: the binary cases need a terminal to drive")
	}
	// Every binary case is offline and fake-backed, and runs only where the
	// kernel isolates it from the network. The runner starts the test
	// process inside a new network namespace; this checks, from the inside,
	// that it really is one. The player and everything it starts are
	// children of this process and inherit the namespace (checked again once
	// the player is up). Without verified isolation the case is skipped,
	// which is reported as blocked: it is not run and called passed.
	if err := acceptance.VerifyThisProcessIsIsolated(os.Getenv(acceptance.HostNetNSEnv)); err != nil {
		t.Skipf("kernel network isolation is not active for this case: %v", err)
	}

	// Runtime files are short-lived and need short paths, so they go under
	// /tmp. Evidence does not live here: see acceptance.EvidenceRoot.
	base, err := os.MkdirTemp("/tmp", "dda")
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{
		t: t, Binary: binary, Base: base,
		Home:       filepath.Join(base, "h"),
		Config:     filepath.Join(base, "h", ".config", appdir.Name),
		Data:       filepath.Join(base, "h", ".local", "share", appdir.Name),
		Music:      filepath.Join(base, "m"),
		Bin:        filepath.Join(base, "bin"),
		tmuxSocket: filepath.Join(base, "tmux"),
	}
	if n := len(e.SocketPath()); n > maxRuntimePath {
		t.Fatalf("runtime socket path is %d bytes (%s): the harness needs a short base directory", n, e.SocketPath())
	}
	for _, dir := range []string{e.Config, e.Music, e.Bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// ALSA's null device: everything plays, nothing is heard.
	e.write(filepath.Join(e.Home, ".asoundrc"), "pcm.!default { type null }\nctl.!default { type null }\n")

	e.Artifacts = t.TempDir()
	if root := os.Getenv(ArtifactsEnv); root != "" {
		e.Artifacts = filepath.Join(root, strings.ReplaceAll(t.Name(), "/", "_"))
		if err := os.MkdirAll(e.Artifacts, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(e.cleanup)
	return e
}

// SocketPath is the player's IPC socket in this environment.
func (e *Env) SocketPath() string { return filepath.Join(e.Config, appdir.Name+".sock") }

// environ is the whole environment the player runs in. Nothing is inherited:
// no real home, no session bus (so the desktop's media keys and MPRIS name
// are untouched), no proxy that works, and a PATH holding only Bin.
func (e *Env) environ() []string {
	dead := "http://127.0.0.1:9" // the discard port: any HTTP request fails fast
	return []string{
		"HOME=" + e.Home,
		"PATH=" + e.Bin,
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"XDG_CONFIG_HOME=" + filepath.Join(e.Home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(e.Home, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(e.Home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(e.Home, ".local", "state"),
		"XDG_MUSIC_DIR=" + e.Music,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=" + filepath.Join(e.Base, "no-bus"),
		"http_proxy=" + dead, "https_proxy=" + dead, "HTTP_PROXY=" + dead, "HTTPS_PROXY=" + dead,
		"all_proxy=" + dead, "ALL_PROXY=" + dead, "no_proxy=", "NO_PROXY=",
	}
}

// WriteConfig writes config.toml. Without it the player starts on defaults.
func (e *Env) WriteConfig(toml string) {
	e.t.Helper()
	e.write(filepath.Join(e.Config, "config.toml"), toml)
}

func (e *Env) write(path, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// Start runs the player in a terminal of the given size. It returns once the
// terminal exists; wait for a screen before pressing keys.
func (e *Env) Start(cols, rows int, args ...string) {
	e.t.Helper()
	if e.Alive() {
		e.t.Fatal("the player is already running in this environment")
	}
	e.run++
	_ = os.Remove(e.exitFile())
	quoted := make([]string, 0, len(args)+1)
	for _, a := range append([]string{e.Binary}, args...) {
		quoted = append(quoted, shellQuote(a))
	}
	var env []string
	for _, kv := range e.environ() {
		env = append(env, shellQuote(kv))
	}
	script := fmt.Sprintf("#!/bin/sh\nenv -i %s %s 2>>%s\necho $? > %s\n",
		strings.Join(env, " "), strings.Join(quoted, " "), shellQuote(e.stderrFile()), shellQuote(e.exitFile()))
	runner := filepath.Join(e.Base, "run.sh")
	e.write(runner, script)
	out, err := exec.Command("tmux", "-S", e.tmuxSocket, "-f", "/dev/null", "new-session", "-d",
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "/bin/sh "+shellQuote(runner)).CombinedOutput()
	if err != nil {
		e.t.Fatalf("tmux: %v: %s", err, out)
	}
}

func (e *Env) exitFile() string   { return filepath.Join(e.Base, "exit") }
func (e *Env) stderrFile() string { return filepath.Join(e.Base, "stderr.txt") }

// LogPath is the player's log file.
func (e *Env) LogPath() string { return filepath.Join(e.Config, appdir.Name+".log") }

// Alive reports whether a started player is still running.
func (e *Env) Alive() bool {
	if e.run == 0 {
		return false
	}
	_, err := os.Stat(e.exitFile())
	return os.IsNotExist(err)
}

// ExitCode is the exit status of the player's last run. Call it after
// WaitExit.
func (e *Env) ExitCode() int {
	e.t.Helper()
	data, err := os.ReadFile(e.exitFile())
	if err != nil {
		e.t.Fatalf("the player has not exited: %v", err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		e.t.Fatalf("exit status %q: %v", data, err)
	}
	return code
}

// Stderr is everything the player's runs wrote to standard error.
func (e *Env) Stderr() string {
	data, _ := os.ReadFile(e.stderrFile())
	return string(data)
}

// Keys presses keys, in tmux's names ("Enter", "Escape", "Tab", "C-g", or a
// literal character). The player must be running.
func (e *Env) Keys(keys ...string) {
	e.t.Helper()
	for _, key := range keys {
		if !e.Alive() {
			e.fail("the player exited before key %q could be pressed", key)
		}
		if out, err := exec.Command("tmux", "-S", e.tmuxSocket, "send-keys", key).CombinedOutput(); err != nil {
			e.fail("send key %q: %v: %s", key, err, out)
		}
	}
}

// Screen is the terminal's current text. After the player exits it is the
// last screen that was read.
func (e *Env) Screen() string {
	out, err := exec.Command("tmux", "-S", e.tmuxSocket, "capture-pane", "-p").Output()
	if err == nil {
		e.lastScreen = string(out)
	}
	return e.lastScreen
}

// View reads the screen as a library view.
func (e *Env) View() View { return ParseView(e.Screen()) }

// WaitView polls until the library view satisfies ok. The player dying
// fails the wait at once; running out of time fails it with the screen, the
// log and the rest of the diagnostics saved.
func (e *Env) WaitView(what string, ok func(View) bool) View {
	e.t.Helper()
	var v View
	e.WaitFor(what, func() bool {
		v = e.View()
		return ok(v)
	})
	e.requirePlayerIsolated()
	// Each screen a case waited for and got is part of its evidence.
	e.record("screens.txt", "=== "+what+"\n"+v.Raw+"\n")
	return v
}

// record appends to one of the case's evidence files.
func (e *Env) record(name, text string) {
	f, err := os.OpenFile(filepath.Join(e.Artifacts, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(text)
}

// requirePlayerIsolated checks, once per start, that the running player is
// in this process's network namespace, which New verified to be isolated.
// The player says which process it is in its IPC pid file.
func (e *Env) requirePlayerIsolated() {
	e.t.Helper()
	if e.nsChecked == e.run || !e.Alive() {
		return
	}
	data, err := os.ReadFile(e.SocketPath() + ".pid")
	if err != nil {
		return // not serving IPC yet; the next wait checks again
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		e.fail("the player's pid file holds %q", data)
	}
	if err := acceptance.SameNetworkNamespace(pid); err != nil {
		e.fail("the player is not inside the network isolation: %v", err)
	}
	e.nsChecked = e.run
	e.record("network-isolation.txt", fmt.Sprintf("run %d: the player (pid %d) is in this case's isolated network namespace\n", e.run, pid))
}

// WaitFor polls until ok, with the same liveness check and time limit as
// WaitView.
func (e *Env) WaitFor(what string, ok func() bool) {
	e.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		if ok() {
			return
		}
		if e.run > 0 && !e.Alive() {
			e.fail("the player exited (status %d) while waiting for: %s", e.ExitCode(), what)
		}
		if time.Now().After(deadline) {
			e.fail("timed out after %s waiting for: %s", waitTimeout, what)
		}
		time.Sleep(pollEvery)
	}
}

// WaitExit waits for the player to exit and returns its status.
func (e *Env) WaitExit() int {
	e.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for e.Alive() {
		if time.Now().After(deadline) {
			e.fail("the player did not exit within %s", waitTimeout)
		}
		time.Sleep(pollEvery)
	}
	return e.ExitCode()
}

// Quit presses q until the player exits (q also steps back out of a level),
// and requires a clean exit.
func (e *Env) Quit() {
	e.t.Helper()
	e.Screen() // keep the last screen for the record
	deadline := time.Now().Add(waitTimeout)
	for e.Alive() {
		if time.Now().After(deadline) {
			e.fail("the player did not quit within %s", waitTimeout)
		}
		_ = exec.Command("tmux", "-S", e.tmuxSocket, "send-keys", "q").Run()
		time.Sleep(4 * pollEvery)
	}
	if code := e.ExitCode(); code != 0 {
		e.fail("the player quit with status %d", code)
	}
}

// Client runs the executable as a one-shot command in the same environment:
// the remote-control commands, or anything else that needs no terminal.
func (e *Env) Client(args ...string) (stdout, stderr string, exit int) {
	e.t.Helper()
	cmd := exec.Command(e.Binary, args...)
	cmd.Env = e.environ()
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			e.t.Fatalf("%s %v: %v", filepath.Base(e.Binary), args, err)
		}
		exit = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), exit
}

// State is the part of the player's remote state the cases read.
type State struct {
	State string `json:"state"`
	Track *struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
		Album  string `json:"album"`
		Path   string `json:"path"`
	} `json:"track"`
	Index       int     `json:"index"`
	Total       int     `json:"total"`
	Position    float64 `json:"position"`
	StreamError string  `json:"stream_error"`
}

// RemoteState asks the running player for its state over IPC.
func (e *Env) RemoteState() State {
	e.t.Helper()
	out, errOut, exit := e.Client("remote", "state")
	if exit != 0 {
		e.fail("remote state: exit %d: %s", exit, errOut)
	}
	var reply struct {
		OK       bool  `json:"ok"`
		Snapshot State `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil || !reply.OK {
		e.fail("remote state: %v: %s", err, out)
	}
	e.record("remote-state.jsonl", strings.TrimSpace(out)+"\n")
	return reply.Snapshot
}

// fail saves the diagnostics and fails the case.
func (e *Env) fail(format string, args ...any) {
	e.t.Helper()
	e.saveDiagnostics()
	e.t.Fatalf(format+"\n--- screen ---\n%s", append(args, e.lastScreen)...)
}

// saveDiagnostics keeps what explains a failure: the screen, standard
// error, the log, the remote state and the fake yt-dlp's request log.
func (e *Env) saveDiagnostics() {
	e.Screen()
	keep := func(name, content string) {
		_ = os.WriteFile(filepath.Join(e.Artifacts, name), []byte(content), 0o600)
	}
	keep("screen.txt", e.lastScreen)
	keep("stderr.txt", e.Stderr())
	if data, err := os.ReadFile(e.LogPath()); err == nil {
		keep("player.log", string(data))
	}
	if data, err := os.ReadFile(filepath.Join(e.Base, "ytdlp", requestLog)); err == nil {
		keep("yt-dlp-requests.jsonl", string(data))
	}
	for _, notes := range []string{filepath.Join(e.Base, "ytdlp", "harness-failures.log"), filepath.Join(e.Bin, "harness-failures.log")} {
		if data, err := os.ReadFile(notes); err == nil {
			keep("yt-dlp-harness-failures.log", string(data))
		}
	}
	if e.Alive() {
		cmd := exec.Command(e.Binary, "remote", "state")
		cmd.Env = e.environ()
		if out, err := cmd.Output(); err == nil {
			keep("remote-state.json", string(out))
		}
	}
}

func (e *Env) cleanup() {
	// A case's captures are kept whether it passed or failed: a pass is
	// evidence too.
	e.saveDiagnostics()
	_ = exec.Command("tmux", "-S", e.tmuxSocket, "kill-server").Run()
	// With the player stopped, nothing more can reach the fake: account for
	// everything it was asked (FakeYTDLP.accounting). This runs for every
	// case that installed it, whatever the case itself checked, so a
	// request the case did not script, or a fake that could not read its own
	// script, fails the case even when its screen looked right. A scripted
	// failure was answered, and is neither.
	if e.fake != nil {
		e.fake.sup.stop() // nothing can invoke the fake any more
		for _, problem := range e.fake.accounting() {
			e.t.Error(problem)
		}
	}
	_ = os.RemoveAll(e.Base)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// View is the library panel as the screen shows it.
type View struct {
	// Breadcrumb is the panel's heading: where you are ("Music / Local /
	// Albums · by title"), without the sync badge and the position.
	Breadcrumb string
	// Heading is the whole heading line, badge included.
	Heading string
	// Rows are the list's rows, without the cursor and the open marker.
	Rows []string
	// Lines are the panel's lines as shown, cursor and markers included: a
	// level's note is among them.
	Lines []string
	// NowPlaying and Transport are the two lines above the panel.
	NowPlaying string
	Transport  string
	Raw        string
}

// ParseView reads a captured screen. It finds the panel by its heading, so
// a screen that is not the library gives an empty Breadcrumb, which no
// expectation matches.
func ParseView(screen string) View {
	v := View{Raw: screen}
	inPanel := false
	for raw := range strings.SplitSeq(screen, "\n") {
		line := strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "│"))
		switch {
		case strings.HasPrefix(line, "♫"):
			v.NowPlaying = line
		case v.Transport == "" && v.NowPlaying != "" && !inPanel && line != "":
			v.Transport = line
		}
		if !inPanel {
			if rest, ok := strings.CutPrefix(line, "── "); ok {
				inPanel = true
				v.Heading = strings.TrimSpace(strings.TrimRight(rest, "─ "))
				v.Breadcrumb, _, _ = strings.Cut(v.Heading, "  ")
			}
			continue
		}
		if line == "" {
			continue
		}
		if strings.Trim(line, "─") == "" { // the rule under the list
			break
		}
		v.Lines = append(v.Lines, line)
		row := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "> "), "›"))
		v.Rows = append(v.Rows, strings.Join(strings.Fields(row), " "))
	}
	return v
}

// HasRow reports whether a row contains text.
func (v View) HasRow(text string) bool {
	for _, row := range v.Rows {
		if strings.Contains(row, text) {
			return true
		}
	}
	return false
}

// At reports whether the view is the level with exactly this breadcrumb.
func (v View) At(breadcrumb string) bool { return v.Breadcrumb == breadcrumb }
