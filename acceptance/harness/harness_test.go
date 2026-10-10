package harness

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

const libraryScreen = `╭──────────────────────────────────────────────────────────────────────────────╮
│  🦆 ddsonic dev                                                   [Library]  │
│  ♫ Test Artist - First Song · Test Album                                     │
│  00:01 / 00:02                                                    ▶ Playing  │
│  ●─────────────────────────────────────────────────────────────────────────  │
│  ── Music / Local / Albums · by title  ✓ synced just now  2/3 ─────────────  │
│    A note about this level.                                                  │
│    Keep Album                                                     Keeper ›   │
│  > Added Album                                                  Newcomer ›   │
│                                                                              │
│  ──────────────────────────────────────────────────────────────────────────  │
│   j k  Move  g G  Top, bottom  Enter l  Open  q  Quit                        │
╰──────────────────────────────────────────────────────────────────────────────╯
`

func TestParseView(t *testing.T) {
	v := ParseView(libraryScreen)
	if !v.At("Music / Local / Albums · by title") {
		t.Errorf("breadcrumb = %q", v.Breadcrumb)
	}
	if !strings.Contains(v.Heading, "✓ synced just now") || strings.Contains(v.Heading, "─") {
		t.Errorf("heading = %q, want the badge and no rule", v.Heading)
	}
	if want := []string{"A note about this level.", "Keep Album Keeper", "Added Album Newcomer"}; !slices.Equal(v.Rows, want) {
		t.Errorf("rows = %q, want %q", v.Rows, want)
	}
	if !v.HasRow("Added Album") || v.HasRow("Quit") {
		t.Errorf("HasRow reads outside the list: rows %q", v.Rows)
	}
	if v.NowPlaying != "♫ Test Artist - First Song · Test Album" || !strings.Contains(v.Transport, "Playing") {
		t.Errorf("now playing %q, transport %q", v.NowPlaying, v.Transport)
	}
	if len(v.Lines) != 3 || !strings.HasPrefix(v.Lines[2], "> ") {
		t.Errorf("lines = %q, want the cursor kept on the selected line", v.Lines)
	}
}

// A screen that is not the library has no breadcrumb, so no expectation
// about a level can match it by accident.
func TestParseViewOfAnotherScreen(t *testing.T) {
	for _, screen := range []string{"", "bash: ddsonic: command not found\n$ ", "ddsonic cannot use its control socket: the path is 108 bytes\n"} {
		if v := ParseView(screen); v.Breadcrumb != "" || len(v.Rows) != 0 {
			t.Errorf("ParseView(%q) = breadcrumb %q, rows %q", screen, v.Breadcrumb, v.Rows)
		}
	}
	// Without the frame, as on a short terminal.
	v := ParseView("♫ No track loaded\n00:00 / 00:00   ■ Stopped\n── Music  1/4 ───\n> All Music ›\n  Local ›\n──────\n q  Quit\n")
	if !v.At("Music") || !slices.Equal(v.Rows, []string{"All Music", "Local"}) {
		t.Errorf("frameless: breadcrumb %q, rows %q", v.Breadcrumb, v.Rows)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "'plain'",
		"two words":  "'two words'",
		"it's":       `'it'\''s'`,
		"$HOME;rm x": "'$HOME;rm x'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSyncsafe(t *testing.T) {
	for n, want := range map[int][]byte{0: {0, 0, 0, 0}, 127: {0, 0, 0, 127}, 128: {0, 0, 1, 0}, 16384: {0, 1, 0, 0}} {
		if got := syncsafe(n); !slices.Equal(got, want) {
			t.Errorf("syncsafe(%d) = %v, want %v", n, got, want)
		}
	}
}

// The scripted account answers exactly the invocations a cookie-mode sync
// makes, and nothing about what the account holds that is not synced.
func TestYTAccountResponses(t *testing.T) {
	account := YTAccount{
		Browser:     "b",
		Liked:       []YTVideo{{ID: "vid1", Title: "T", Track: "T", Album: "Album One", Artists: []string{"A"}}},
		SavedAlbums: []string{"Album One", "Only Saved"},
	}
	responses := account.Responses()
	var urls []string
	for _, r := range responses {
		if got := r.Args[:7]; !slices.Equal(got, []string{"--ignore-config", "--no-mark-watched", "--socket-timeout", "15", "--no-warnings", "--cookies-from-browser", "b"}) {
			t.Errorf("invocation starts %q", got)
		}
		if r.Args[len(r.Args)-2] != "--" {
			t.Errorf("the address is not the last argument after --: %q", r.Args)
		}
		urls = append(urls, r.Args[len(r.Args)-1])
		if strings.Contains(r.Stdout, "Only Saved") {
			t.Errorf("a response mentions an album that is only saved: %s", r.Stdout)
		}
	}
	slices.Sort(urls)
	if want := []string{YTWatchURL("vid1"), YTPlaylistFeed, YTLikedMusic}; !slices.Equal(urls, sorted(want)) {
		t.Errorf("answered addresses = %q, want %q", urls, sorted(want))
	}
	account.FailLiked = true
	for _, r := range account.Responses() {
		if r.Args[len(r.Args)-1] == YTLikedMusic && (r.Exit == 0 || !strings.HasPrefix(r.Stderr, "ERROR:")) {
			t.Errorf("the failing Liked Music read is %+v", r)
		}
	}
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return s
}

// M9: when a case ends, the fake's dealings are accounted for from its own
// log, for every case that installed it, and anything untrustworthy is a
// problem. A scripted failure was answered: it is none.
func TestFakeAccountingFailsClosed(t *testing.T) {
	const answered = `{"time":"t1","args":["-J","--","https://example.invalid/liked"],"answered":true}` + "\n"
	const outage = `{"time":"t2","args":["-J","--","https://example.invalid/down"],"answered":true}` + "\n" // scripted Exit 1
	const rejected = `{"time":"t3","args":["--review-unscripted"],"answered":false}` + "\n"
	const failure = `{"time":"t4","args":["-J","--","https://example.invalid/liked"],"answered":false,"failure":"no scripted responses: open responses.json: no such file or directory"}` + "\n"
	tests := []struct {
		name   string
		log    string // "" for no log at all
		script string // "" for no script at all
		want   []string
	}{
		{"nothing was asked", "", "[]", nil},
		{"answers only, a scripted outage among them", answered + outage, "[]", nil},
		{"an unscripted invocation", answered + rejected, "[]", []string{"rejected an unscripted invocation"}},
		{"the fake could not read its script", answered + failure + answered, "[]", []string{"itself failed", "harness failure"}},
		{"both", rejected + failure, "[]", []string{"rejected an unscripted invocation", "itself failed"}},
		{"a log that is not JSON lines", answered + "garbage\n", "[]", []string{"request log is malformed at line 2"}},
		{"the script is gone at the end", answered, "", []string{"script is gone at the end"}},
		{"the script is malformed at the end", answered, "{not json", []string{"script is malformed at the end"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.log != "" {
				os.WriteFile(filepath.Join(dir, requestLog), []byte(tt.log), 0o600)
			}
			if tt.script != "" {
				os.WriteFile(filepath.Join(dir, "responses.json"), []byte(tt.script), 0o600)
			}
			problems := (&FakeYTDLP{e: &Env{t: t}, dir: dir}).accounting()
			text := strings.Join(problems, "\n")
			if len(tt.want) == 0 && len(problems) != 0 {
				t.Fatalf("problems = %v, want none", problems)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("problems = %v, want one containing %q", problems, want)
				}
			}
		})
	}
	// Round 4: what the log does not show. An invocation that began and was
	// never recorded, and a failure the fake noted, each fail the case
	// although the log and the script read perfectly well at the end.
	for name, leave := range map[string]func(dir string){
		"an attempt that was never recorded": func(dir string) {
			os.MkdirAll(filepath.Join(dir, "attempts"), 0o700)
			os.WriteFile(filepath.Join(dir, "attempts", "invocation-1"), nil, 0o600)
		},
		"a failure noted in the fixture directory": func(dir string) {
			os.WriteFile(filepath.Join(dir, "harness-failures.log"), []byte("t cannot record the request: no space left on device\n"), 0o600)
		},
		"a failure noted beside the executable": func(dir string) {
			os.MkdirAll(filepath.Join(filepath.Dir(dir), "bin"), 0o700)
			os.WriteFile(filepath.Join(filepath.Dir(dir), "bin", "harness-failures.log"), []byte("t cannot mark the attempt\n"), 0o600)
		},
	} {
		base := t.TempDir()
		dir := filepath.Join(base, "ytdlp")
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, requestLog), []byte(answered+outage), 0o600)
		os.WriteFile(filepath.Join(dir, "responses.json"), []byte("[]"), 0o600)
		if problems := (&FakeYTDLP{e: &Env{t: t}, dir: dir}).accounting(); len(problems) != 0 {
			t.Fatalf("%s: the control is not clean: %v", name, problems)
		}
		leave(dir)
		if problems := (&FakeYTDLP{e: &Env{t: t}, dir: dir}).accounting(); len(problems) == 0 {
			t.Errorf("%s: accounted as clean", name)
		}
	}
	// A log that cannot be read at all is a problem too.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "responses.json"), []byte("[]"), 0o600)
	os.Mkdir(filepath.Join(dir, requestLog), 0o700)
	if problems := (&FakeYTDLP{e: &Env{t: t}, dir: dir}).accounting(); len(problems) == 0 {
		t.Error("an unreadable request log was accounted as clean")
	}
}

func TestFakeRejectionsAreCounted(t *testing.T) {
	dir := t.TempDir()
	log := `{"time":"t1","args":["-J","--","https://example.invalid/liked"],"answered":true}
{"time":"t3","args":["--review-unscripted"],"answered":false}
{"time":"t4","args":["-j"],"answered":false,"failure":"no scripted responses"}
`
	if err := os.WriteFile(filepath.Join(dir, requestLog), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &FakeYTDLP{e: &Env{t: t}, dir: dir}
	if rejected := fake.Rejected(); len(rejected) != 1 || rejected[0].Args[0] != "--review-unscripted" {
		t.Errorf("rejected = %+v, want the one unscripted invocation (a harness failure is counted separately)", rejected)
	}
}

// Round 5, M9: the real fake, run as a program against the real supervisor.
// With its fixture directory, its attempts directory and the directory
// beside its executable all read-only for a while, it can leave no marker
// and no note, and afterwards its files look untouched. The supervisor was
// told, and the accounting fails the case. The scripted outage, which is an
// answer, stays clean.
func TestFakeIsSupervisedWhateverBecomesOfItsFiles(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the supervisor's abstract socket is a Linux facility")
	}
	if os.Getuid() == 0 {
		t.Skip("root writes to read-only directories")
	}
	binary, err := builtFake()
	if err != nil {
		t.Fatal(err)
	}
	liked := []string{"-J", "--", "https://example.invalid/liked"}
	outage := []string{"-J", "--", "https://example.invalid/down"}
	install := func(t *testing.T) (*FakeYTDLP, func(args ...string) (int, string)) {
		t.Helper()
		base, err := os.MkdirTemp("/tmp", "ddf")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(base) })
		e := &Env{t: t, Base: base, Bin: filepath.Join(base, "bin")}
		f := &FakeYTDLP{e: e, dir: filepath.Join(base, "ytdlp")}
		for _, dir := range []string{e.Bin, filepath.Join(f.dir, "attempts")} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(e.Bin, "yt-dlp"), binary, 0o700); err != nil {
			t.Fatal(err)
		}
		f.Script([]YTResponse{{Args: liked, Stdout: "{}\n"}, {Args: outage, Exit: 1, Stderr: "ERROR: HTTP Error 503\n"}})
		if f.sup, err = startSupervisor(base); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(f.sup.stop)
		return f, func(args ...string) (int, string) {
			cmd := exec.Command(filepath.Join(e.Bin, "yt-dlp"), args...)
			out, err := cmd.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); ok {
				return exit.ExitCode(), string(out)
			}
			if err != nil {
				t.Fatalf("run the fake: %v", err)
			}
			return 0, string(out)
		}
	}

	t.Run("answers and a scripted outage are clean", func(t *testing.T) {
		f, invoke := install(t)
		if exit, out := invoke(liked...); exit != 0 {
			t.Fatalf("the scripted answer: exit %d, %s", exit, out)
		}
		if exit, out := invoke(outage...); exit != 1 || !strings.Contains(out, "503") {
			t.Fatalf("the scripted outage: exit %d, %s", exit, out)
		}
		if problems := f.accounting(); len(problems) != 0 {
			t.Errorf("problems = %v, want none", problems)
		}
	})

	t.Run("an unscripted invocation", func(t *testing.T) {
		f, invoke := install(t)
		if exit, _ := invoke("--unscripted"); exit != 97 {
			t.Fatalf("exit %d, want 97", exit)
		}
		if problems := strings.Join(f.accounting(), "\n"); !strings.Contains(problems, "rejected an unscripted invocation") {
			t.Errorf("problems = %q", problems)
		}
	})

	// The reviewer's case, without the player around it.
	t.Run("nothing can be written, then everything is restored", func(t *testing.T) {
		f, invoke := install(t)
		if exit, _ := invoke(liked...); exit != 0 {
			t.Fatal("the baseline invocation failed")
		}
		before, _ := os.ReadFile(filepath.Join(f.dir, requestLog))
		locked := []string{filepath.Join(f.dir, "attempts"), f.dir, f.e.Bin}
		for _, dir := range locked {
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
		}
		exit, out := invoke(liked...)
		for _, dir := range locked {
			os.Chmod(dir, 0o700)
		}
		if exit != 96 {
			t.Fatalf("with everything read-only the fake exited %d, want 96: %s", exit, out)
		}
		// Recovery: a good invocation, as SYNC-002 goes on to make.
		if exit, _ := invoke(liked...); exit != 0 {
			t.Fatal("the invocation after the restore failed")
		}
		// The files are as if nothing had happened in between.
		after, _ := os.ReadFile(filepath.Join(f.dir, requestLog))
		if strings.Count(string(after), "\n") != strings.Count(string(before), "\n")+1 {
			t.Fatalf("the log gained other than the one later invocation:\n%s", after)
		}
		left, _ := filepath.Glob(filepath.Join(f.dir, "attempts", "*"))
		notes, _ := filepath.Glob(filepath.Join(f.e.Base, "*", "harness-failures.log"))
		if len(left)+len(notes) != 0 {
			t.Logf("the fake managed to leave something after all: %v %v", left, notes)
		}
		problems := strings.Join(f.accounting(), "\n")
		if !strings.Contains(problems, "told its supervisor that it failed") || !strings.Contains(problems, "the log is not the whole story") {
			t.Errorf("the accounting does not show the invocation that could not be recorded:\n%s", problems)
		}
	})

	t.Run("an invocation that began and never ended", func(t *testing.T) {
		f, _ := install(t)
		conn, err := net.Dial("unix", supervisorAddress(f.e.Base))
		if err != nil {
			t.Fatal(err)
		}
		conn.Write([]byte(`{"phase":"begin","args":["-J"]}` + "\n"))
		ack := make([]byte, 3)
		conn.Read(ack)
		conn.Close()
		if problems := strings.Join(f.accounting(), "\n"); !strings.Contains(problems, "began 1 invocation(s) and reported the end of 0") {
			t.Errorf("problems = %q", problems)
		}
	})

	// With no supervisor listening, the fake answers nothing at all.
	t.Run("an unsupervised fake answers nothing", func(t *testing.T) {
		f, invoke := install(t)
		f.sup.stop()
		if exit, out := invoke(liked...); exit != 96 || strings.Contains(out, "{}") {
			t.Errorf("exit %d, output %q; want 96 and no answer", exit, out)
		}
	})
}
