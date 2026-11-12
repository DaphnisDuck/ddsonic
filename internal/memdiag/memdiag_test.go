package memdiag

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Off, nothing is recorded, nothing is created and the context is untouched.
func TestOffDoesNothing(t *testing.T) {
	if On() {
		t.Fatal("diagnostics are on in a test that did not start them")
	}
	ctx := context.Background()
	Count("x", "")
	Add("x", "y", 3)
	Event(1, "x", "mp3", "MP3_320")
	if got := WithOpen(ctx); got != ctx {
		t.Error("WithOpen changed the context while off")
	}
	if OpenSeq(WithOpen(ctx)) != 0 || Counters() != nil || Dir() != "" {
		t.Error("off, a sequence number, counters or a directory were reported")
	}
	if err := Snapshot("x"); err != nil {
		t.Error(err)
	}
	stop, err := Start("")
	if err != nil || On() {
		t.Fatalf("Start(\"\") = %v, on %t; want off, no error", err, On())
	}
	stop()
}

func start(t *testing.T) (dir string, stop func()) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "diag")
	stop, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return dir, stop
}

func readLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("%s: %v in %q", filepath.Base(path), err, sc.Text())
		}
		out = append(out, m)
	}
	return out
}

func wantMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != want {
		t.Errorf("%s: mode %o, want %o", filepath.Base(path), fi.Mode().Perm(), want)
	}
}

// On, the run's directory and files are private, the events and counters
// are what was recorded, and a snapshot holds the memory classes asked for.
func TestRunRecordsPrivately(t *testing.T) {
	dir, stop := start(t)
	run := Dir()
	ctx := WithOpen(context.Background())
	seq := OpenSeq(ctx)
	if seq == 0 || OpenSeq(WithOpen(context.Background())) != seq+1 {
		t.Errorf("sequence numbers %d, then not %d", seq, seq+1)
	}
	Count("opens", "mp3")
	Count("opens", "mp3")
	Add("ring_open", "", 10)
	Add("ring_open", "", -4)
	Event(seq, "open", "mp3", "MP3_320")
	if c := Counters(); c["opens.mp3"] != 2 || c["ring_open"] != 6 {
		t.Errorf("counters = %v", c)
	}
	if err := Snapshot("test"); err != nil {
		t.Fatal(err)
	}
	stop()
	if On() {
		t.Fatal("still on after stop")
	}

	wantMode(t, dir, 0o700)
	wantMode(t, run, 0o700)
	ents, err := os.ReadDir(run)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range ents {
		names[e.Name()] = true
		wantMode(t, filepath.Join(run, e.Name()), 0o600)
	}
	for _, n := range []string{"events.jsonl", "snapshots.jsonl", "goroutines-001.txt", "heap-001.pb.gz", "goroutines-002.txt", "heap-002.pb.gz"} {
		if !names[n] {
			t.Errorf("no %s in %v", n, names)
		}
	}

	evs := readLines(t, filepath.Join(run, "events.jsonl"))
	if len(evs) != 1 || evs[0]["ev"] != "open" || evs[0]["seq"] != float64(seq) || evs[0]["codec"] != "mp3" || evs[0]["format"] != "MP3_320" {
		t.Errorf("events = %v", evs)
	}

	snaps := readLines(t, filepath.Join(run, "snapshots.jsonl"))
	if len(snaps) != 3 || snaps[0]["kind"] != "build" || snaps[1]["trigger"] != "test" || snaps[2]["trigger"] != "exit" {
		t.Fatalf("snapshots = %v", snaps)
	}
	for _, k := range []string{"go", "version", "os", "arch"} {
		if _, ok := snaps[0][k]; !ok {
			t.Errorf("build line has no %q: %v", k, snaps[0])
		}
	}
	s := snaps[1]
	if c, _ := s["counters"].(map[string]any); c["opens.mp3"] != float64(2) {
		t.Errorf("snapshot counters = %v", s["counters"])
	}
	g, _ := s["go"].(map[string]any)
	for _, k := range []string{"/gc/heap/live:bytes", "/memory/classes/heap/free:bytes", "/memory/classes/heap/released:bytes", "/memory/classes/total:bytes", "committed_bytes", "/gc/cycles/total:gc-cycles", "/sched/goroutines:goroutines", "/cgo/go-to-c-calls:calls", "cgo_calls"} {
		if _, ok := g[k]; !ok {
			t.Errorf("go has no %q", k)
		}
	}
	if runtime.GOOS != "linux" {
		return
	}
	st, _ := s["proc_status_kB"].(map[string]any)
	ro, _ := s["proc_smaps_rollup_kB"].(map[string]any)
	fds, _ := s["fds_by_type"].(map[string]any)
	for _, k := range []string{"VmRSS", "VmHWM", "RssAnon", "RssFile"} {
		if _, ok := st[k]; !ok {
			t.Errorf("status has no %s: %v", k, st)
		}
	}
	for _, k := range []string{"Rss", "Anonymous", "Pss_Anon", "Pss_File"} {
		if _, ok := ro[k]; !ok {
			t.Errorf("smaps_rollup has no %s: %v", k, ro)
		}
	}
	if n, _ := s["threads"].(float64); n < 1 {
		t.Errorf("threads = %v", s["threads"])
	}
	if len(fds) == 0 {
		t.Error("no descriptors counted")
	}
	for k := range fds {
		if strings.ContainsAny(k, "/[") {
			t.Errorf("a descriptor kind holds a path: %q", k)
		}
	}
	if s["errors"] != nil {
		t.Errorf("errors = %v", s["errors"])
	}
}

func TestStartRefusesAnUnsafeDirectory(t *testing.T) {
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"group-readable": open, "symlink": link, "file": file} {
		if stop, err := Start(dir); err == nil {
			stop()
			t.Errorf("%s: Start succeeded", name)
		}
		if On() {
			t.Fatalf("%s: on after a refused start", name)
		}
	}
}

// Events sent while stop runs are dropped, never sent on a closed channel.
func TestEventsRaceStop(t *testing.T) {
	_, stop := start(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 2000 {
				Event(uint64(i), "e", "", "")
				Add("n", "", 1)
			}
		}()
	}
	stop()
	wg.Wait()
}

func TestEnv(t *testing.T) {
	if Env != "DDSONIC_MEMDIAG" {
		t.Errorf("Env = %q, want DDSONIC_MEMDIAG", Env)
	}
}

func TestCodec(t *testing.T) {
	for format, want := range map[string]string{
		"OGG_VORBIS_96": "vorbis", "OGG_VORBIS_320": "vorbis", "MP3_160": "mp3", "MP3_320": "mp3",
		"FLAC_FLAC": "flac", "AAC_24": "aac", "MP4_128": "aac", "UNKNOWN": "other", "": "other",
	} {
		if got := Codec(format); got != want {
			t.Errorf("Codec(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestFdKindKeepsNoPath(t *testing.T) {
	for target, want := range map[string]string{
		"socket:[123]": "socket", "pipe:[9]": "pipe", "anon_inode:[eventpoll]": "anon_inode:eventpoll",
		"anon_inode:inotify": "anon_inode:inotify", "anon_inode:[a/b]": "anon_inode", "/memfd:x (deleted)": "memfd",
		"/dev/snd/pcmC0D0p": "dev", "/home/someone/Music/a song.mp3": "file", "net:[1]": "other",
	} {
		if got := fdKind(target); got != want {
			t.Errorf("fdKind(%q) = %q, want %q", target, got, want)
		}
	}
}
