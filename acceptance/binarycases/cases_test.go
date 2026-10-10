// Package binarycases holds the deterministic acceptance cases that drive a
// ddsonic executable through a terminal. Each test is one case of the
// catalog (docs/ddsonic/acceptance/cases.yaml), which names it as its runner.
//
// They run through `make acceptance`, which names the executable under test.
// Under a plain `go test ./...` there is none and they skip.
package binarycases

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/acceptance"
	"github.com/bjarneo/cliamp/acceptance/harness"
	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/internal/appdir"
)

const (
	// Wide enough that a heading shows every source's sync badge whole.
	cols, rows = 150, 30

	levelMusic       = "Music"
	levelLocal       = "Music / Local"
	levelLocalAlbums = "Music / Local / Albums · by title"
)

// at waits for the library level with this breadcrumb, loaded: a level
// shows its heading at once and its rows a moment later, and a row read in
// between is the "Loading…" line, not the level's content.
func at(e *harness.Env, breadcrumb string) harness.View {
	return e.WaitView("the level "+breadcrumb+", loaded", func(v harness.View) bool {
		return v.At(breadcrumb) && !v.HasRow("Loading")
	})
}

// open moves the cursor to the row holding text and opens it, then waits
// for the level it leads to.
func open(t *testing.T, e *harness.Env, text, leadsTo string) harness.View {
	t.Helper()
	e.Keys("g")
	for i := 0; ; i++ {
		v := e.View()
		// The list wraps, so every row comes under the cursor within one
		// pass; three allow for a screen read just before it was redrawn.
		if i > 3*(len(v.Lines)+1) {
			t.Fatalf("no row %q to open at %q; rows: %q", text, v.Breadcrumb, v.Rows)
		}
		selected := -1
		for n, line := range v.Lines {
			if strings.HasPrefix(line, "> ") {
				selected = n
			}
		}
		if selected >= 0 && strings.Contains(v.Lines[selected], text) {
			break
		}
		e.Keys("j")
		e.WaitView("the cursor to move", func(next harness.View) bool { return !slices.Equal(next.Lines, v.Lines) })
	}
	e.Keys("Enter")
	return at(e, leadsTo)
}

// localAlbums starts the player and opens Local → Albums.
func localAlbums(t *testing.T, e *harness.Env) harness.View {
	t.Helper()
	e.Start(cols, rows)
	at(e, levelMusic)
	open(t, e, "Local", levelLocal)
	return open(t, e, "Albums", levelLocalAlbums)
}

// albumIDs maps the titles of provider's catalog albums to their catalog
// IDs, and fails on a title held twice: a duplicate is never expected here.
func albumIDs(t *testing.T, e *harness.Env, provider string) map[string]int64 {
	t.Helper()
	albums, err := e.Catalog().Albums(context.Background(), provider, catalog.ByTitle)
	if err != nil {
		t.Fatalf("catalog albums of %s: %v", provider, err)
	}
	ids := map[string]int64{}
	for _, a := range albums {
		if _, twice := ids[a.Title]; twice {
			t.Fatalf("the catalog holds the %s album %q twice", provider, a.Title)
		}
		ids[a.Title] = a.ID
	}
	return ids
}

func titles(ids map[string]int64) []string {
	names := make([]string, 0, len(ids))
	for name := range ids {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func localConfig(e *harness.Env) string {
	return "initial_directory = \"" + e.Music + "\"\n"
}

// The executable in the case environment is the one the runner identified:
// the same bytes, reporting the version the candidate must report.
func TestBinaryUnderTestIsTheIdentifiedArtifact(t *testing.T) {
	e := harness.New(t)
	wantVersion, wantSum := os.Getenv(harness.VersionEnv), os.Getenv(harness.BinarySHA256Env)
	if wantVersion == "" || wantSum == "" {
		t.Skip("the runner did not say which artifact is under test")
	}
	sum, err := acceptance.FileSHA256(e.Binary)
	if err != nil {
		t.Fatal(err)
	}
	if sum != wantSum {
		t.Errorf("the executable's SHA-256 is %s, want the identified artifact's %s", sum, wantSum)
	}
	// A release binary names the commit it was built from as well; a
	// development build names none.
	want := appdir.Name + " version " + wantVersion
	if commit := os.Getenv(harness.SourceSHAEnv); commit != "" {
		want += " (commit " + commit + ")"
	}
	stdout, stderr, exit := e.Client("--version")
	if exit != 0 || strings.TrimSpace(stdout) != want {
		t.Errorf("--version = exit %d, %q (stderr %q); want %q", exit, stdout, stderr, want)
	}
}

// A first start with no config: no account providers, an empty library, a
// clean quit, and the same again on a second start.
func TestFreshStartWithNoProviders(t *testing.T) {
	e := harness.New(t)
	for start := 1; start <= 2; start++ {
		e.Start(cols, rows)
		v := at(e, levelMusic)
		if want := []string{"All Music", "Local", "Radio", "Search"}; !slices.Equal(v.Rows, want) {
			t.Fatalf("start %d: the Music level lists %q, want %q", start, v.Rows, want)
		}
		if v.NowPlaying != "♫ No track loaded" {
			t.Errorf("start %d: now playing reads %q", start, v.NowPlaying)
		}
		state := e.RemoteState()
		if state.State != "stopped" || state.Total != 0 || state.Track != nil {
			t.Errorf("start %d: remote state is %+v, want stopped with an empty queue", start, state)
		}
		if _, err := os.Stat(e.SocketPath()); err != nil {
			t.Errorf("start %d: no IPC socket while running: %v", start, err)
		}
		e.Quit()
		if _, err := os.Stat(e.SocketPath()); !os.IsNotExist(err) {
			t.Errorf("start %d: the IPC socket is still there after quitting (stat: %v)", start, err)
		}
	}
	if stderr := strings.TrimSpace(e.Stderr()); stderr != "" {
		t.Errorf("two clean runs wrote to standard error:\n%s", stderr)
	}
}

// An invalid startup option is refused by name, before anything starts.
func TestInvalidStartupOptionIsRefused(t *testing.T) {
	e := harness.New(t)
	for _, tt := range []struct {
		args []string
		want []string
	}{
		{[]string{"--provider", "plex"}, []string{"plex", "radio", "spotify", "ytmusic"}},
		{[]string{"--repeat", "sideways"}, []string{"sideways"}},
		{[]string{"--no-such-option"}, []string{"no-such-option"}},
	} {
		_, stderr, exit := e.Client(tt.args...)
		if exit == 0 {
			t.Errorf("%v: exit 0, want a refusal; stderr %q", tt.args, stderr)
		}
		for _, want := range tt.want {
			if !strings.Contains(stderr, want) {
				t.Errorf("%v: the message does not mention %q:\n%s", tt.args, want, stderr)
			}
		}
		if strings.Contains(stderr, "TTY") {
			t.Errorf("%v: the player went on to start its screen:\n%s", tt.args, stderr)
		}
	}
	for _, left := range []string{e.SocketPath(), filepath.Join(e.Data, "library.db")} {
		if _, err := os.Stat(left); !os.IsNotExist(err) {
			t.Errorf("a refused start left %s behind (stat: %v)", left, err)
		}
	}
}

// A config.toml that is not valid TOML stops the start: a non-zero exit, the
// file and the parser's error with its place, the file left as it was, and
// nothing started in its place on defaults. A valid config starts as ever,
// and so does one holding the output-device line older versions wrote bare,
// which is the one tolerated exception.
func TestMalformedConfigStopsTheStart(t *testing.T) {
	e := harness.New(t)
	configFile := filepath.Join(e.Config, "config.toml")

	for _, tt := range []struct {
		name string
		toml string
		at   string // file:line, as the diagnostic gives it
		says string // a fragment of the parser's own message
	}{
		{"a string left open", "[spotify]\nclient_id = \"oops\nbitrate = 320\n", ":2:", "strings cannot contain newlines"},
		{"a line that is no setting", "volume = 0\nthis is not toml\n", ":2:", "expected '.' or '='"},
		{"a value that is no TOML value", "volume = 0\ntheme = Tokyo Night\n", ":2:", "expected value"},
		{"a key given twice", "volume = 1\nvolume = 2\n", ":2:", "already been defined"},
		{"a section given twice", "[spotify]\nbitrate = 320\n[spotify]\nbitrate = 160\n", ":3:", "already been defined"},
		{"an invalid number", "volume = 01\n", ":1:", "Invalid integer"},
		{"an array left open", "eq = [0, 0, 1\n", ":1:", ""},
		// The tolerated device line excuses nothing beside it, and is not
		// tolerated in any shape but the one older versions wrote.
		{"an error beside the legacy device line", "audio_device = default\ntheme = Tokyo Night\n", ":2:", "expected value"},
		{"a device line with a string left open", "audio_device = \"default\n", ":1:", "strings cannot contain newlines"},
		{"a bare device line inside a section", "[spotify]\naudio_device = default\n", ":2:", "expected value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e.WriteConfig(tt.toml)
			_, stderr, exit := e.Client() // no arguments: start the player
			if exit == 0 {
				t.Errorf("exit 0, want the start refused; stderr %q", stderr)
			}
			for _, want := range []string{configFile + tt.at, tt.says, "did not start", "was not changed"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("the diagnostic lacks %q:\n%s", want, stderr)
				}
			}
			// Without a terminal the player's screen cannot open; reaching
			// that point would mean it went on to start, on defaults.
			if strings.Contains(stderr, "TTY") {
				t.Errorf("the player went on to start:\n%s", stderr)
			}
			if data, err := os.ReadFile(configFile); err != nil || string(data) != tt.toml {
				t.Errorf("the malformed file was changed (err %v): %q", err, data)
			}
			entries, _ := os.ReadDir(e.Config)
			if len(entries) != 1 {
				t.Errorf("the refused start left %d entries in the config directory, want config.toml alone", len(entries))
			}
			if _, err := os.Stat(filepath.Join(e.Data, "library.db")); !os.IsNotExist(err) {
				t.Errorf("the refused start created a catalog (stat: %v)", err)
			}
		})
	}

	// The other direction: these start. Unknown keys and sections are not a
	// syntax matter, and the second file is what choosing an output device
	// used to write.
	for _, tt := range []struct{ name, toml, keeps string }{
		{"a valid config with keys nobody knows", "# mine\nvolume = -3\nflux_capacitor = true\n\n[nobody.knows]\nx = [1, 2]\n", "flux_capacitor = true\n"},
		{"the device line older versions wrote", "volume = -3\naudio_device = default\n", "audio_device = default\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e.WriteConfig(tt.toml)
			e.Start(cols, rows)
			at(e, levelMusic)
			if state := e.RemoteState(); state.State != "stopped" {
				t.Errorf("remote state is %+v after starting", state)
			}
			e.Quit()
			// Starting and quitting did not rewrite the line: a file is not
			// changed merely by being read.
			if data, _ := os.ReadFile(configFile); !strings.Contains(string(data), tt.keeps) {
				t.Errorf("the config no longer holds %q:\n%s", tt.keeps, data)
			}
		})
	}
}

// Local is authoritative for the files in the music folder: a file added
// appears after a refresh and not before, survives a restart, and a file
// removed is gone after a refresh, while an unrelated album keeps its place
// and its identity throughout.
func TestLocalAddRefreshRestartRemove(t *testing.T) {
	e := harness.New(t)
	e.WriteConfig(localConfig(e))
	e.WriteTrack("Keeper/Keep Album/01.mp3", harness.Track{Title: "Kept Song", Artist: "Keeper", Album: "Keep Album", Genre: "Rock", Number: 1}, 2)

	// Quiescent baseline: the first index is finished before anything is
	// asserted or changed.
	v := localAlbums(t, e)
	synced := e.WaitSynced(catalog.Local, time.Time{})
	v = e.WaitView("the kept album alone", func(v harness.View) bool { return len(v.Rows) == 1 && v.HasRow("Keep Album") })
	baseline := albumIDs(t, e, catalog.Local)
	if !slices.Equal(titles(baseline), []string{"Keep Album"}) {
		t.Fatalf("baseline catalog albums = %q, want Keep Album alone (screen rows %q)", titles(baseline), v.Rows)
	}

	// Mutate the source. Nothing changes by itself: the list, reopened from
	// the catalog, still shows the baseline, and no sync has run.
	added := e.WriteTrack("Newcomer/Added Album/01.mp3", harness.Track{Title: "New Song", Artist: "Newcomer", Album: "Added Album", Genre: "Jazz", Number: 1}, 2)
	e.Keys("h")
	at(e, levelLocal)
	v = open(t, e, "Albums", levelLocalAlbums)
	if len(v.Rows) != 1 || !v.HasRow("Keep Album") {
		t.Fatalf("before any refresh the list changed to %q", v.Rows)
	}
	if again := e.SyncedAt(catalog.Local); !again.Equal(synced) {
		t.Fatalf("Local synced by itself (at %s, baseline %s)", again, synced)
	}

	// Refresh: the added album appears, and the kept one is the same entity.
	e.Keys("r")
	synced = e.WaitSynced(catalog.Local, synced)
	e.WaitView("the added album", func(v harness.View) bool {
		return len(v.Rows) == 2 && v.HasRow("Added Album") && v.HasRow("Keep Album")
	})
	afterAdd := albumIDs(t, e, catalog.Local)
	if !slices.Equal(titles(afterAdd), []string{"Added Album", "Keep Album"}) || afterAdd["Keep Album"] != baseline["Keep Album"] {
		t.Fatalf("after the add the catalog holds %v, want both albums with Keep Album still ID %d", afterAdd, baseline["Keep Album"])
	}

	// Idempotence: a second refresh with nothing changed adds nothing.
	e.Keys("r")
	synced = e.WaitSynced(catalog.Local, synced)
	if again := albumIDs(t, e, catalog.Local); !mapsEqual(again, afterAdd) {
		t.Fatalf("a second refresh changed the catalog from %v to %v", afterAdd, again)
	}

	// Restart: both albums are there again, with the same identities.
	e.Quit()
	v = localAlbums(t, e)
	e.WaitView("both albums after the restart", func(v harness.View) bool {
		return len(v.Rows) == 2 && v.HasRow("Added Album") && v.HasRow("Keep Album")
	})
	synced = e.WaitSynced(catalog.Local, synced) // the startup index, so the next refresh is told apart from it
	if restarted := albumIDs(t, e, catalog.Local); !mapsEqual(restarted, afterAdd) {
		t.Fatalf("after the restart the catalog holds %v, want %v", restarted, afterAdd)
	}

	// Reverse the mutation: gone after a refresh, the kept album untouched.
	if err := os.RemoveAll(filepath.Dir(filepath.Dir(added))); err != nil {
		t.Fatal(err)
	}
	e.Keys("r")
	e.WaitSynced(catalog.Local, synced)
	e.WaitView("the added album gone", func(v harness.View) bool { return len(v.Rows) == 1 && v.HasRow("Keep Album") })
	if final := albumIDs(t, e, catalog.Local); !mapsEqual(final, baseline) {
		t.Fatalf("after the removal the catalog holds %v, want the baseline %v", final, baseline)
	}
	e.Quit()
}

func mapsEqual(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if got, ok := b[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// A local file plays through the null audio device, a second is queued and
// playback stops, each step read back from the player's remote state.
func TestLocalPlayQueueStop(t *testing.T) {
	e := harness.New(t)
	e.WriteConfig(localConfig(e))
	first := e.WriteTrack("Player/Play Album/01.mp3", harness.Track{Title: "First Song", Artist: "Player", Album: "Play Album", Number: 1}, 60)
	second := e.WriteTrack("Other/Other Album/01.mp3", harness.Track{Title: "Second Song", Artist: "Other", Album: "Other Album", Number: 1}, 60)

	localAlbums(t, e)
	e.WaitSynced(catalog.Local, time.Time{})
	open(t, e, "Play Album", "Music / Local / Albums / Play Album")
	e.Keys("Enter")

	var state harness.State
	e.WaitFor("the first song to play", func() bool {
		state = e.RemoteState()
		return state.State == "playing" && state.Track != nil && state.Track.Title == "First Song"
	})
	if state.Track.Path != first || state.Total != 1 || state.StreamError != "" {
		t.Fatalf("playing state is %+v (track %+v), want %s alone in the queue", state, state.Track, first)
	}
	e.WaitFor("playback to advance", func() bool { return e.RemoteState().Position > 0 })
	e.WaitView("the screen to show the first song playing", func(v harness.View) bool {
		return strings.Contains(v.NowPlaying, "First Song") && strings.Contains(v.Transport, "Playing")
	})

	// Queue a second track from outside, as a script would.
	if _, stderr, exit := e.Client("queue", second); exit != 0 {
		t.Fatalf("queue: exit %d: %s", exit, stderr)
	}
	e.WaitFor("the queue to hold two tracks", func() bool { return e.RemoteState().Total == 2 })
	if state = e.RemoteState(); state.State != "playing" || state.Track == nil || state.Track.Title != "First Song" {
		t.Fatalf("queueing changed what plays: %+v (track %+v)", state, state.Track)
	}

	e.Keys("s")
	e.WaitFor("playback to stop", func() bool { return e.RemoteState().State == "stopped" })
	if state = e.RemoteState(); state.Total != 2 {
		t.Errorf("stopping changed the queue to %d tracks, want 2", state.Total)
	}
	e.WaitView("the screen to show playback stopped", func(v harness.View) bool { return strings.Contains(v.Transport, "Stopped") })
	e.Quit()
}

// The YouTube Music account of the derived-list cases. Two liked tracks
// represent two albums and two artists; a third album and a third artist
// are saved and followed in the account but represented by no synced track.
var derivedAccount = harness.YTAccount{
	Browser: "acceptance-browser",
	Liked: []harness.YTVideo{
		{ID: "vidRepSaved01", Title: "Saved Song", Channel: "Alpha Artist - Topic", ChannelID: "UCalpha0000000000000001",
			Track: "Saved Song", Artists: []string{"Alpha Artist"}, Album: "Represented Saved Album", Year: 2001},
		{ID: "vidRepUnsav02", Title: "Unsaved Song", Channel: "Beta Artist - Topic", ChannelID: "UCbeta00000000000000002",
			Track: "Unsaved Song", Artists: []string{"Beta Artist"}, Album: "Represented Unsaved Album", Year: 2002},
	},
	// In the account's library, and not something ddsonic syncs.
	SavedAlbums:     []string{"Represented Saved Album", "Unrepresented Saved Album"},
	FollowedArtists: []string{"Alpha Artist", "Gamma Followed Artist"},
}

const (
	levelYouTube        = "Music / YouTube Music"
	levelYouTubeAlbums  = "Music / YouTube Music / Albums from tracks · by title"
	levelYouTubeArtists = "Music / YouTube Music / Artists from tracks"
)

// syncedYouTube starts the player signed in to the account and waits until
// the sync and the reading of every liked track's details are done: the
// completion point every assertion about the derived lists rests on.
func syncedYouTube(t *testing.T, e *harness.Env, account harness.YTAccount) *harness.FakeYTDLP {
	t.Helper()
	fake := e.InstallFakeYTDLP()
	fake.Script(account.Responses())
	e.WriteConfig(account.Config())
	e.Start(cols, rows)
	at(e, levelMusic)
	e.WaitSynced(catalog.YouTube, time.Time{})
	waitDetailsRead(e, fake, account)
	return fake
}

func waitDetailsRead(e *harness.Env, fake *harness.FakeYTDLP, account harness.YTAccount) {
	e.WaitFor("every liked track's details to be read", func() bool {
		for _, v := range account.Liked {
			if fake.Count(harness.YTWatchURL(v.ID)) == 0 {
				return false
			}
		}
		albums, err := e.Catalog().Albums(context.Background(), catalog.YouTube, catalog.ByTitle)
		return err == nil && len(albums) == len(account.Liked)
	})
}

// YouTube Music's "Albums from tracks" is derived from synced tracks, and
// says so. An album saved in the account that no synced track represents
// does not appear; an album a synced track represents appears whether it is
// saved or not.
func TestYouTubeAlbumsFromTracksAreDerived(t *testing.T) {
	e := harness.New(t)
	fake := syncedYouTube(t, e, derivedAccount)

	menu := open(t, e, "YouTube Music", levelYouTube)
	if !menu.HasRow("Albums from tracks") || slices.Contains(menu.Rows, "Albums") {
		t.Fatalf("the YouTube Music menu lists %q: its album list must be named as derived", menu.Rows)
	}
	v := open(t, e, "Albums from tracks", levelYouTubeAlbums)
	v = e.WaitView("the represented albums", func(v harness.View) bool { return v.HasRow("Represented Saved Album") })

	// Non-vacuous: the list is populated, with exactly what the tracks give.
	for _, want := range []string{"Represented Saved Album", "Represented Unsaved Album"} {
		if !v.HasRow(want) {
			t.Errorf("%q, represented by a synced track, is not listed: %q", want, v.Rows)
		}
	}
	if v.HasRow("Unrepresented Saved Album") {
		t.Errorf("an album saved in the account but represented by no synced track is listed: %q", v.Rows)
	}
	inCatalog := titles(albumIDs(t, e, catalog.YouTube))
	if want := []string{"Represented Saved Album", "Represented Unsaved Album"}; !slices.Equal(inCatalog, want) {
		t.Errorf("the catalog's YouTube albums are %q, want %q", inCatalog, want)
	}
	// The level says what it is, where the user reads it.
	if text := strings.Join(v.Lines, " "); !strings.Contains(text, "not synchronized") || !strings.Contains(text, "Liked Music and synced playlists") {
		t.Errorf("the list does not explain that it is derived:\n%s", strings.Join(v.Lines, "\n"))
	}

	// Nothing was asked about the account's saved albums, and nothing the
	// case did not script was asked at all.
	fake.RequireNoRejections()
	for _, r := range fake.Requests() {
		if u := r.URL(); u != harness.YTPlaylistFeed && u != harness.YTLikedMusic && !strings.HasPrefix(u, harness.YTWatchURL("")) {
			t.Errorf("an unexpected request reached yt-dlp: %q", r.Args)
		}
	}
	e.Quit()
}

// The same for "Artists from tracks": following an artist no synced track
// credits changes nothing here, and a credited artist appears followed or not.
func TestYouTubeArtistsFromTracksAreDerived(t *testing.T) {
	e := harness.New(t)
	fake := syncedYouTube(t, e, derivedAccount)

	menu := open(t, e, "YouTube Music", levelYouTube)
	if !menu.HasRow("Artists from tracks") || slices.Contains(menu.Rows, "Artists") {
		t.Fatalf("the YouTube Music menu lists %q: its artist list must be named as derived", menu.Rows)
	}
	open(t, e, "Artists from tracks", levelYouTubeArtists)
	v := e.WaitView("the credited artists", func(v harness.View) bool { return v.HasRow("Alpha Artist") })
	for _, want := range []string{"Alpha Artist", "Beta Artist"} {
		if !v.HasRow(want) {
			t.Errorf("%q, credited on a synced track, is not listed: %q", want, v.Rows)
		}
	}
	if v.HasRow("Gamma Followed Artist") {
		t.Errorf("an artist followed in the account but credited on no synced track is listed: %q", v.Rows)
	}
	artists, err := e.Catalog().Artists(context.Background(), catalog.YouTube)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range artists {
		names = append(names, a.Name)
	}
	if want := []string{"Alpha Artist", "Beta Artist"}; !slices.Equal(names, want) {
		t.Errorf("the catalog's YouTube artists are %q, want %q", names, want)
	}
	fake.RequireNoRejections()
	e.Quit()
}

// After a restart the cached library is shown before the source is read
// again: the fake yt-dlp answers nothing on the second run, and receives
// nothing.
func TestCachedLibraryShownBeforeAnyRefetch(t *testing.T) {
	e := harness.New(t)
	fake := syncedYouTube(t, e, derivedAccount)
	before := albumIDs(t, e, catalog.YouTube)
	synced := e.SyncedAt(catalog.YouTube)
	e.Quit()

	// The source is now unavailable: any read at all would be rejected and
	// recorded. The default refresh window (2h) has not passed.
	fake.Script(nil)
	asked := len(fake.Requests())

	e.Start(cols, rows)
	at(e, levelMusic)
	open(t, e, "YouTube Music", levelYouTube)
	open(t, e, "Albums from tracks", levelYouTubeAlbums)
	v := e.WaitView("the cached albums", func(v harness.View) bool {
		return v.HasRow("Represented Saved Album") && v.HasRow("Represented Unsaved Album")
	})
	if !strings.Contains(v.Heading, "synced") {
		t.Errorf("the heading %q does not say the list is a synced copy", v.Heading)
	}
	if after := albumIDs(t, e, catalog.YouTube); !mapsEqual(after, before) {
		t.Errorf("the catalog after the restart is %v, want %v unchanged", after, before)
	}
	if again := e.SyncedAt(catalog.YouTube); !again.Equal(synced) {
		t.Errorf("YouTube Music synced again at %s; the cached copy of %s should have been shown as it was", again, synced)
	}
	if now := fake.Requests(); len(now) != asked {
		t.Errorf("the source was read %d times before the cached library was shown: %v", len(now)-asked, now[asked:])
	}
	e.Quit()
}

// A sync that fails keeps what was cached and says it failed; the next
// sync, once the source is back, succeeds without a restart.
func TestFailedSyncKeepsTheCacheAndRecovers(t *testing.T) {
	e := harness.New(t)
	fake := syncedYouTube(t, e, derivedAccount)
	before := albumIDs(t, e, catalog.YouTube)
	synced := e.SyncedAt(catalog.YouTube)

	open(t, e, "YouTube Music", levelYouTube)
	open(t, e, "Albums from tracks", levelYouTubeAlbums)

	// The source fails.
	down := derivedAccount
	down.FailLiked = true
	fake.Script(down.Responses())
	failedAfter := time.Now().Add(-time.Second)
	e.Keys("r")
	if msg := e.WaitSyncFailed(catalog.YouTube, failedAfter); msg == "" {
		t.Fatal("the failed sync stored no error")
	}
	v := e.WaitView("the failure to be shown", func(v harness.View) bool { return strings.Contains(v.Heading, "sync failed") })
	if !strings.Contains(v.Heading, "cached") {
		t.Errorf("the heading %q does not say the cached library is showing", v.Heading)
	}
	if !v.HasRow("Represented Saved Album") || !v.HasRow("Represented Unsaved Album") {
		t.Errorf("the failed sync dropped rows: %q", v.Rows)
	}
	if kept := albumIDs(t, e, catalog.YouTube); !mapsEqual(kept, before) {
		t.Errorf("the failed sync changed the catalog from %v to %v", before, kept)
	}

	// The source is back, with one more liked track. No restart.
	back := derivedAccount
	back.Liked = append(slices.Clone(back.Liked), harness.YTVideo{
		ID: "vidRecovery03", Title: "Recovery Song", Channel: "Delta Artist - Topic", ChannelID: "UCdelta0000000000000003",
		Track: "Recovery Song", Artists: []string{"Delta Artist"}, Album: "Recovery Album", Year: 2003})
	fake.Script(back.Responses())
	e.Keys("r")
	e.WaitSynced(catalog.YouTube, synced)
	waitDetailsRead(e, fake, back)
	v = e.WaitView("the recovered list", func(v harness.View) bool {
		return v.HasRow("Recovery Album") && !strings.Contains(v.Heading, "sync failed")
	})
	recovered := albumIDs(t, e, catalog.YouTube)
	for title, id := range before {
		if recovered[title] != id {
			t.Errorf("%q changed identity across the failure and recovery: %d, was %d", title, recovered[title], id)
		}
	}
	e.Quit()
}

// A level's note wraps inside an 80-column screen, the key bar wraps with
// it, and on a short terminal the note gives its rows to the list.
func TestNotesAndKeyBarWrap(t *testing.T) {
	const note = "Saved YouTube Music Library Albums are not synchronized."
	for _, size := range []struct {
		name       string
		cols, rows int
		wantNote   bool
	}{
		{"80 columns", 80, 30, true},
		{"a short terminal", 80, 14, false},
	} {
		t.Run(size.name, func(t *testing.T) {
			e := harness.New(t)
			fake := e.InstallFakeYTDLP()
			fake.Script(derivedAccount.Responses())
			e.WriteConfig(derivedAccount.Config())
			e.Start(size.cols, size.rows)
			at(e, levelMusic)
			e.WaitSynced(catalog.YouTube, time.Time{})
			waitDetailsRead(e, fake, derivedAccount)
			open(t, e, "YouTube Music", levelYouTube)
			open(t, e, "Albums from tracks", levelYouTubeAlbums)
			v := e.WaitView("the album rows", func(v harness.View) bool { return v.HasRow("Represented Saved Album") })

			lines := harness.Lines(v.Raw)
			for _, line := range lines {
				if n := len([]rune(line)); n > size.cols {
					t.Errorf("a line is %d columns wide on a %d-column screen: %q", n, size.cols, line)
				}
				// The frame is drawn at this height; a short terminal drops it.
				if size.wantNote && !strings.HasPrefix(line, "│") && !strings.HasPrefix(line, "╭") && !strings.HasPrefix(line, "╰") {
					t.Errorf("a line is outside the frame: %q", line)
				}
			}
			if got := strings.Contains(strings.Join(v.Lines, " "), note); got != size.wantNote {
				t.Errorf("note shown = %v, want %v, on %dx%d:\n%s", got, size.wantNote, size.cols, size.rows, v.Raw)
			}
			// Whatever the size, the rows the level exists for are there.
			if !v.HasRow("Represented Unsaved Album") {
				t.Errorf("the list lost a row on %dx%d: %q", size.cols, size.rows, v.Rows)
			}
			if size.wantNote && !strings.Contains(v.Raw, "Quit") {
				t.Errorf("the key bar lost its keys on %dx%d:\n%s", size.cols, size.rows, v.Raw)
			}
			e.Quit()
		})
	}
}
