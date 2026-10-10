package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
)

// RepoRoot is the repository the harness runs from: the nearest directory
// above the working directory that holds go.mod.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod above the working directory")
		}
		dir = parent
	}
}

// Track is the tags of a generated music file.
type Track struct {
	Title, Artist, Album, Genre string
	Number                      int
}

// WriteTrack writes a playable, tagged MP3 at path under the music folder:
// the repository's generated two-second tone, repeated to last about
// seconds, behind an ID3v2.4 tag. Nothing but the Go standard library
// touches the file, so no encoder's version can change it.
func (e *Env) WriteTrack(rel string, tr Track, seconds int) string {
	e.t.Helper()
	root, err := RepoRoot()
	if err != nil {
		e.t.Fatal(err)
	}
	tone, err := os.ReadFile(filepath.Join(root, "player", "testdata", "tone-440hz.mp3"))
	if err != nil {
		e.t.Fatal(err)
	}
	var frames bytes.Buffer
	for name, value := range map[string]string{
		"TIT2": tr.Title, "TPE1": tr.Artist, "TALB": tr.Album, "TCON": tr.Genre, "TRCK": strconv.Itoa(tr.Number),
	} {
		if value == "" || value == "0" {
			continue
		}
		body := append([]byte{3}, value...) // 3: UTF-8
		frames.WriteString(name)
		frames.Write(syncsafe(len(body)))
		frames.Write([]byte{0, 0})
		frames.Write(body)
	}
	var file bytes.Buffer
	file.WriteString("ID3")
	file.Write([]byte{4, 0, 0})
	file.Write(syncsafe(frames.Len()))
	file.Write(frames.Bytes())
	for range max(1, seconds/2) {
		file.Write(tone)
	}
	path := filepath.Join(e.Music, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// syncsafe encodes n as ID3v2's four 7-bit bytes.
func syncsafe(n int) []byte {
	return []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
}

// requestLog is the fake yt-dlp's log of every invocation.
const requestLog = "requests.jsonl"

// YTResponse is one invocation the fake yt-dlp answers: these arguments,
// exactly and in order.
type YTResponse struct {
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr,omitempty"`
	Exit   int      `json:"exit,omitempty"`
}

// YTRequest is one invocation the fake yt-dlp received. It was answered
// (Answered), or rejected as unscripted (neither field set), or the fake
// itself failed (Failure): it could not read its script.
type YTRequest struct {
	Args     []string `json:"args"`
	Answered bool     `json:"answered"`
	Failure  string   `json:"failure,omitempty"`
}

// URL is the address the invocation was for: its last argument.
func (r YTRequest) URL() string {
	if len(r.Args) == 0 {
		return ""
	}
	return r.Args[len(r.Args)-1]
}

// FakeYTDLP is the fake yt-dlp installed in an environment.
type FakeYTDLP struct {
	e   *Env
	dir string
	// sup hears of every invocation from the fake itself, over a channel
	// that is not a file (see supervisor).
	sup *supervisor
}

// supervisedEvent is what the fake reports: an invocation has begun, or
// ended, and how.
type supervisedEvent struct {
	Phase    string   `json:"phase"`
	Args     []string `json:"args"`
	Answered bool     `json:"answered"`
	Failure  string   `json:"failure"`
	Exit     int      `json:"exit"`
}

// supervisor listens for the fake's reports on an abstract Unix socket and
// keeps them in this process's memory.
//
// It is what makes the accounting independent of the fake's files. Those
// (its log, its attempt markers, its failure notes) live in directories
// that a fault, or a case, can make unwritable for a while; restored
// afterwards, they would show nothing of an invocation that failed in
// between. An abstract socket is not in any directory: nothing done to the
// file system keeps a report from arriving, and nothing done afterwards
// removes one that has arrived. The fake reports before it does anything
// and again before it answers, and answers nothing if it cannot.
//
// Abstract sockets are a Linux facility, scoped to the network namespace,
// which the fake shares with the case. The binary cases run on Linux only.
type supervisor struct {
	listener net.Listener
	done     chan struct{}

	mu     sync.Mutex
	events []supervisedEvent
	broken []string // what went wrong with the supervision itself
}

// supervisorAddress must be fakeytdlp.SupervisorAddress: the fake is a
// separate program and cannot share the function.
// TestFakeIsSupervisedWhateverBecomesOfItsFiles runs the real fake against
// this address.
func supervisorAddress(base string) string { return "@acceptance-fake-yt-dlp:" + base }

func startSupervisor(base string) (*supervisor, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("the fake yt-dlp is supervised over an abstract Unix socket, which %s does not have", runtime.GOOS)
	}
	listener, err := net.Listen("unix", supervisorAddress(base))
	if err != nil {
		return nil, fmt.Errorf("supervise the fake yt-dlp: %w", err)
	}
	s := &supervisor{listener: listener, done: make(chan struct{})}
	go s.serve()
	return s, nil
}

func (s *supervisor) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.note("the supervisor stopped listening: " + err.Error())
			}
			return
		}
		// One report at a time: the fake waits for the acknowledgement, so
		// what it does next happens after the report is kept.
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		var ev supervisedEvent
		if err != nil || json.Unmarshal(line, &ev) != nil || (ev.Phase != "begin" && ev.Phase != "end") {
			s.note(fmt.Sprintf("the supervisor received a report it could not read (%q, %v)", line, err))
			conn.Close()
			continue
		}
		s.mu.Lock()
		s.events = append(s.events, ev)
		s.mu.Unlock()
		if _, err := conn.Write([]byte("ok\n")); err != nil {
			s.note("the supervisor could not acknowledge a report: " + err.Error())
		}
		conn.Close()
	}
}

func (s *supervisor) note(problem string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broken = append(s.broken, problem)
}

// stop ends the supervision; reports already received are kept.
func (s *supervisor) stop() {
	_ = s.listener.Close()
	<-s.done
}

// problems is what the supervisor heard that makes the case untrustworthy:
// a failure of the supervision itself, an invocation that began and never
// reported its end, and an invocation that ended in a failure of the fake's
// own. logged is how many invocations the request log holds: the supervisor
// having heard of more is a problem too, whatever the log says.
func (s *supervisor) problems(logged int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	problems := slices.Clone(s.broken)
	begun, ended := 0, 0
	for _, ev := range s.events {
		switch {
		case ev.Phase == "begin":
			begun++
		case ev.Failure != "":
			ended++
			problems = append(problems, fmt.Sprintf("the fake yt-dlp told its supervisor that it failed (%s, exit %d) on %q: a harness failure, not an answer from the provider", ev.Failure, ev.Exit, ev.Args))
		default:
			ended++
		}
	}
	if begun != ended {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp began %d invocation(s) and reported the end of %d: what became of the rest is unknown", begun, ended))
	}
	if ended > logged {
		problems = append(problems, fmt.Sprintf("the supervisor heard of %d invocation(s) and the request log holds %d: the log is not the whole story", ended, logged))
	}
	return problems
}

var (
	buildFake   sync.Once
	fakeBinary  string
	errFakeMake error
)

// builtFake builds the fake yt-dlp once and returns the executable's bytes.
func builtFake() ([]byte, error) {
	buildFake.Do(func() {
		var root, dir string
		if root, errFakeMake = RepoRoot(); errFakeMake != nil {
			return
		}
		if dir, errFakeMake = os.MkdirTemp("", "fakeytdlp"); errFakeMake != nil {
			return
		}
		fakeBinary = filepath.Join(dir, "yt-dlp")
		cmd := exec.Command("go", "build", "-o", fakeBinary, "./acceptance/fakeytdlp")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			errFakeMake = fmt.Errorf("build the fake yt-dlp: %w: %s", err, out)
		}
	})
	if errFakeMake != nil {
		return nil, errFakeMake
	}
	return os.ReadFile(fakeBinary)
}

// InstallFakeYTDLP puts the fail-closed fake on the player's PATH as
// "yt-dlp". It is the only yt-dlp the player can find: the real one is not
// on that PATH. Until Script is called it answers nothing.
func (e *Env) InstallFakeYTDLP() *FakeYTDLP {
	e.t.Helper()
	data, err := builtFake()
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.Bin, "yt-dlp"), data, 0o700); err != nil {
		e.t.Fatal(err)
	}
	f := &FakeYTDLP{e: e, dir: filepath.Join(e.Base, "ytdlp")}
	// Supervised from before it can be invoked until the case ends.
	sup, err := startSupervisor(e.Base)
	if err != nil {
		e.t.Fatal(err)
	}
	f.sup = sup
	e.fake = f // its dealings are accounted for when the case ends (Env.cleanup)
	if err := os.MkdirAll(f.dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	f.Script(nil)
	return f
}

// Script replaces what the fake answers. It can be called while the player
// runs: the account "changes" between two syncs.
func (f *FakeYTDLP) Script(responses []YTResponse) {
	f.e.t.Helper()
	if responses == nil {
		responses = []YTResponse{}
	}
	data, err := json.MarshalIndent(responses, "", " ")
	if err != nil {
		f.e.t.Fatal(err)
	}
	// Renamed into place, so the fake never reads half a file.
	tmp := filepath.Join(f.dir, "responses.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		f.e.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(f.dir, "responses.json")); err != nil {
		f.e.t.Fatal(err)
	}
}

// Requests is every invocation so far, in order.
func (f *FakeYTDLP) Requests() []YTRequest {
	f.e.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, requestLog))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.e.t.Fatal(err)
	}
	var requests []YTRequest
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var r YTRequest
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			f.e.t.Fatalf("fake yt-dlp request log: %v", err)
		}
		requests = append(requests, r)
	}
	return requests
}

// Count is how many invocations so far were for url.
func (f *FakeYTDLP) Count(url string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.URL() == url {
			n++
		}
	}
	return n
}

// Rejected is every invocation so far that the fake refused: one the case
// did not script. A scripted failure was answered and is not among them.
func (f *FakeYTDLP) Rejected() []YTRequest {
	f.e.t.Helper()
	var rejected []YTRequest
	for _, r := range f.Requests() {
		if !r.Answered && r.Failure == "" {
			rejected = append(rejected, r)
		}
	}
	return rejected
}

// accounting is the fake's reckoning when a case ends, with the player
// stopped: everything that makes the case's dealings with the fake
// untrustworthy. It fails closed. An invocation the case did not script, an
// invocation the fake could not serve because its own script was missing or
// malformed, a request log that cannot be read, and a script that is not
// readable at the end are each a problem, and each fails the case: a broken
// fake is never taken for the provider failure a case scripted. A scripted
// failure was answered, and is none of these. Nor does a clean log at the
// end clear an earlier failure to write it: the fake's attempt markers and
// failure notes are read as well, and above all what the fake reported to
// its supervisor, which does not pass through any file.
func (f *FakeYTDLP) accounting() []string {
	var problems []string
	logged := 0 // invocations the request log holds
	data, err := os.ReadFile(filepath.Join(f.dir, requestLog))
	if err != nil && !os.IsNotExist(err) {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp's request log cannot be read, so what it was asked is unknown: %v", err))
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for line := 1; sc.Scan(); line++ {
		var r YTRequest
		logged++
		switch err := json.Unmarshal(sc.Bytes(), &r); {
		case err != nil:
			problems = append(problems, fmt.Sprintf("the fake yt-dlp's request log is malformed at line %d, so what it was asked is unknown: %v", line, err))
		case r.Failure != "":
			problems = append(problems, fmt.Sprintf("the fake yt-dlp itself failed (%s) on %q: a harness failure, not an answer from the provider", r.Failure, r.Args))
		case !r.Answered:
			problems = append(problems, fmt.Sprintf("the fake yt-dlp rejected an unscripted invocation: %q", r.Args))
		}
	}
	if err := sc.Err(); err != nil {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp's request log cannot be read to its end: %v", err))
	}
	// What the fake could not put in its log, it left elsewhere, to stay: a
	// marker for every invocation that began and was never recorded, and a
	// note of every failure of its own. A log that reads well now does not
	// answer for an invocation it never received.
	if left, _ := filepath.Glob(filepath.Join(f.dir, "attempts", "*")); len(left) > 0 {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp was invoked %d time(s) without being able to account for it: the request log is not the whole story", len(left)))
	}
	for _, notes := range []string{filepath.Join(f.dir, "harness-failures.log"), filepath.Join(filepath.Dir(f.dir), "bin", "harness-failures.log")} {
		switch noted, err := os.ReadFile(notes); {
		case err == nil && len(bytes.TrimSpace(noted)) > 0:
			problems = append(problems, "the fake yt-dlp itself failed: "+strings.TrimSpace(string(noted)))
		case err != nil && !os.IsNotExist(err):
			problems = append(problems, fmt.Sprintf("the fake yt-dlp's failure notes cannot be read: %v", err))
		}
	}
	// And what the fake told its supervisor directly, which no file's state
	// can add to or take away from.
	if f.sup != nil {
		problems = append(problems, f.sup.problems(logged)...)
	}
	script, err := os.ReadFile(filepath.Join(f.dir, "responses.json"))
	var responses []YTResponse
	if err != nil {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp's script is gone at the end of the case: %v", err))
	} else if err := json.Unmarshal(script, &responses); err != nil {
		problems = append(problems, fmt.Sprintf("the fake yt-dlp's script is malformed at the end of the case: %v", err))
	}
	return problems
}

// RequireNoRejections fails the case now if the fake has refused any
// invocation so far. Every case that installs the fake gets this check
// automatically when it ends (Env.cleanup); calling it mid-case only fails
// sooner, with the screen of that moment.
func (f *FakeYTDLP) RequireNoRejections() {
	f.e.t.Helper()
	for _, r := range f.Rejected() {
		f.e.fail("the fake yt-dlp rejected an unscripted invocation: %q", r.Args)
	}
}

// YTVideo is one video of a scripted YouTube Music account.
type YTVideo struct {
	ID        string
	Title     string   // the video's title, as a flat playlist lists it
	Channel   string   // the uploading channel
	ChannelID string   //
	Track     string   // the music details a full read gives: track, artists, album
	Artists   []string //
	Album     string   //
	Year      int      //
}

// YTAccount is a scripted YouTube Music account, read with a browser's
// cookies. Only what ddsonic syncs has an answer: Liked Music and the
// playlist feed. SavedAlbums and FollowedArtists are what the account holds
// that ddsonic does not sync; they are part of the fixture so a case can say
// what must not appear, and nothing answers a request for them.
type YTAccount struct {
	Browser         string
	Liked           []YTVideo
	SavedAlbums     []string
	FollowedArtists []string
	// FailLiked makes the Liked Music read fail, as an outage would.
	FailLiked bool
}

// YouTube addresses, as ddsonic asks yt-dlp for them.
const (
	YTPlaylistFeed = "https://www.youtube.com/feed/playlists"
	YTLikedMusic   = "https://www.youtube.com/playlist?list=LM"
)

// YTWatchURL is a video's address.
func YTWatchURL(id string) string { return "https://music.youtube.com/watch?v=" + id }

// Responses scripts the account: the exact invocations ddsonic makes for a
// cookie-mode sync and for reading each liked video's details. Each starts
// with --ignore-config and --no-mark-watched, spelled out here rather than
// taken from the player's code: the fake answers nothing else, so a sync
// that could be steered by a yt-dlp.conf, or mark a video watched, fails.
func (a YTAccount) Responses() []YTResponse {
	base := []string{"--ignore-config", "--no-mark-watched", "--socket-timeout", "15", "--no-warnings", "--cookies-from-browser", a.Browser}
	invocation := func(url string, args ...string) []string {
		return append(append(append([]string{}, base...), args...), "--", url)
	}
	// The playlist feed lists nothing: this account has no playlists.
	responses := []YTResponse{{Args: invocation(YTPlaylistFeed, "--flat-playlist", "-j")}}

	type entry struct {
		ID        string  `json:"id"`
		Title     string  `json:"title"`
		Channel   string  `json:"channel"`
		ChannelID string  `json:"channel_id"`
		Duration  float64 `json:"duration"`
	}
	liked := struct {
		Count   int     `json:"playlist_count"`
		Entries []entry `json:"entries"`
	}{Count: len(a.Liked), Entries: []entry{}}
	for _, v := range a.Liked {
		liked.Entries = append(liked.Entries, entry{ID: v.ID, Title: v.Title, Channel: v.Channel, ChannelID: v.ChannelID, Duration: 180})
		details, _ := json.Marshal(map[string]any{
			"id": v.ID, "track": v.Track, "artists": v.Artists, "album": v.Album,
			"release_year": v.Year, "categories": []string{"Music"},
		})
		responses = append(responses, YTResponse{Args: invocation(YTWatchURL(v.ID), "-j", "--skip-download"), Stdout: string(details) + "\n"})
	}
	likedJSON, _ := json.Marshal(liked)
	likedRead := YTResponse{Args: invocation(YTLikedMusic, "--flat-playlist", "-J"), Stdout: string(likedJSON) + "\n"}
	if a.FailLiked {
		likedRead = YTResponse{Args: likedRead.Args, Exit: 1, Stderr: "ERROR: Unable to download API page: HTTP Error 503: Service Unavailable\n"}
	}
	return append(responses, likedRead)
}

// Config is the config.toml section that signs ddsonic in to the account.
func (a YTAccount) Config() string {
	return "[ytmusic]\ncookies_from = " + strconv.Quote(a.Browser) + "\n"
}

// Lines returns s's non-empty lines, trimmed.
func Lines(s string) []string {
	var lines []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// Catalog opens a copy of the player's catalog as it is now, for reading: a
// second source for what the screen shows, and the only one for identity
// (catalog IDs) and for when each collection last synced. It is a copy, so
// reading it cannot disturb the running player.
func (e *Env) Catalog() *sqlite.Store {
	e.t.Helper()
	var store *sqlite.Store
	var lastErr error
	// The files are copied while the player may be writing them; a torn
	// copy fails to open and is taken again.
	for range 20 {
		dir, err := os.MkdirTemp(e.Base, "cat")
		if err != nil {
			e.t.Fatal(err)
		}
		for _, suffix := range []string{"", "-wal"} {
			data, err := os.ReadFile(filepath.Join(e.Data, "library.db"+suffix))
			if err != nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, "library.db"+suffix), data, 0o600); err != nil {
				e.t.Fatal(err)
			}
		}
		if store, lastErr = sqlite.Open(context.Background(), filepath.Join(dir, "library.db")); lastErr == nil {
			e.t.Cleanup(func() { _ = store.Close() })
			return store
		}
		time.Sleep(pollEvery)
	}
	e.fail("cannot read the player's catalog: %v", lastErr)
	return nil
}

// SyncedAt is when every one of provider's collections last synced
// successfully (the oldest of them), or the zero time when one never has or
// the last attempt of one failed.
func (e *Env) SyncedAt(provider string) time.Time {
	e.t.Helper()
	status, err := e.Catalog().SyncStatus(context.Background(), provider)
	if err != nil || len(status) == 0 {
		return time.Time{}
	}
	oldest := status[0].LastSuccess
	for _, c := range status {
		if c.LastError != "" || c.LastSuccess.IsZero() {
			return time.Time{}
		}
		if c.LastSuccess.Before(oldest) {
			oldest = c.LastSuccess
		}
	}
	return oldest
}

// Status is provider's sync status per collection, from the catalog.
func (e *Env) Status(provider string) []catalog.CollectionStatus {
	e.t.Helper()
	status, err := e.Catalog().SyncStatus(context.Background(), provider)
	if err != nil {
		e.fail("sync status of %s: %v", provider, err)
	}
	return status
}

// WaitSynced waits until every collection of provider has synced
// successfully after the given time, and returns the new time. It is the
// completion point a negative assertion needs: "the row is absent" means
// something only once the sync that could have added it is known to be done.
func (e *Env) WaitSynced(provider string, after time.Time) time.Time {
	e.t.Helper()
	var at time.Time
	e.WaitFor(provider+" to finish a sync", func() bool {
		at = e.SyncedAt(provider)
		return at.After(after)
	})
	return at
}

// WaitSyncFailed waits until a collection of provider records a failed
// attempt made after the given time, and returns the error it stored.
func (e *Env) WaitSyncFailed(provider string, after time.Time) string {
	e.t.Helper()
	var msg string
	e.WaitFor(provider+" to record a failed sync", func() bool {
		for _, c := range e.Status(provider) {
			if c.LastError != "" && c.LastAttempt.After(after) {
				msg = c.LastError
				return true
			}
		}
		return false
	})
	return msg
}
