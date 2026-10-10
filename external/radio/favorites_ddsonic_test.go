// ddsonic: the favorites store under contention. A toggle waits for the
// favorites file's lock and writes the file without holding the lock the
// readers take, since drawing reads the store every frame; toggles queue
// behind one another; the observer is told outside every lock.

package radio

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/fileutil"
)

// promptly fails the test unless fn returns within a second: a reader, or
// the UI, that had to wait for a save. fn returns what it found wrong, or "".
func promptly(t *testing.T, what string, fn func() string) {
	t.Helper()
	done := make(chan string, 1)
	go func() { done <- fn() }()
	select {
	case wrong := <-done:
		if wrong != "" {
			t.Error(what + ": " + wrong)
		}
	case <-time.After(time.Second):
		t.Fatalf("%s waited for a save in progress", what)
	}
}

// holdFavoritesLock takes the favorites file's lock, as another ddsonic
// saving its own toggle holds it.
func holdFavoritesLock(t *testing.T, f *Favorites) (release func()) {
	t.Helper()
	unlock, err := fileutil.LockFile(f.path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = unlock() }) }
	t.Cleanup(release)
	return release
}

func newTestFavorites(t *testing.T) *Favorites {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	f := LoadFavorites()
	if f.path == "" {
		t.Fatal("favorites have no path")
	}
	return f
}

// While a toggle waits for the file's lock, every read answers at once
// with the state before it, and the toggle lands when the lock is free.
func TestFavoritesReadsDoNotWaitForASave(t *testing.T) {
	f := newTestFavorites(t)
	kept, added := CatalogStation{Name: "Kept", URL: "https://kept.example/"}, CatalogStation{Name: "Added", URL: "https://added.example/"}
	if _, err := f.Toggle(kept); err != nil {
		t.Fatal(err)
	}
	revision := f.Revision()
	release := holdFavoritesLock(t, f)

	saved := make(chan error, 1)
	go func() { _, err := f.Toggle(added); saved <- err }()
	// Reads from now until the lock is released race the toggle's start;
	// before or after it, none may wait.
	for range 50 {
		promptly(t, "Contains", func() string {
			if !f.Contains(kept.URL) || f.Contains(added.URL) {
				return "membership changed before the save landed"
			}
			return ""
		})
		promptly(t, "Revision", func() string {
			if f.Revision() != revision {
				return "revision advanced before the save landed"
			}
			return ""
		})
		promptly(t, "Stations and Count", func() string {
			if got := f.Stations(); len(got) != 1 || f.Count() != 1 {
				return fmt.Sprintf("stations = %+v before the save landed", got)
			}
			return ""
		})
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-saved:
		t.Fatalf("the toggle did not wait for the file lock: %v", err)
	default:
	}

	release()
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	if !f.Contains(added.URL) || f.Revision() != revision+1 || f.Count() != 2 {
		t.Errorf("after the save: stations %+v, revision %d, want both and %d", f.Stations(), f.Revision(), revision+1)
	}
}

// Toggles made at once take their turns: each sees the one before it, so
// none is lost, a station toggled an even number of times ends as it began,
// and memory, the file and the revision agree.
func TestFavoritesConcurrentTogglesTakeTurns(t *testing.T) {
	f := newTestFavorites(t)
	const distinct, repeats = 12, 6
	flipped := CatalogStation{Name: "Flipped", URL: "https://flipped.example/"}
	release := holdFavoritesLock(t, f)
	var wg sync.WaitGroup
	toggle := func(s CatalogStation) {
		defer wg.Done()
		if _, err := f.Toggle(s); err != nil {
			t.Error(err)
		}
	}
	for i := range distinct {
		wg.Add(1)
		go toggle(CatalogStation{Name: fmt.Sprintf("Station %d", i), URL: fmt.Sprintf("https://radio.example/%d", i)})
	}
	for range repeats {
		wg.Add(1)
		go toggle(flipped)
	}
	// All of them are waiting before the first can save: a toggle that read
	// what to do before its turn would act on what the others have since
	// changed.
	time.Sleep(100 * time.Millisecond)
	release()
	wg.Wait()

	if f.Count() != distinct || f.Contains(flipped.URL) {
		t.Errorf("favorites = %d with flipped=%v, want %d without it", f.Count(), f.Contains(flipped.URL), distinct)
	}
	if got := f.Revision(); got != distinct+repeats {
		t.Errorf("revision = %d, want one per toggle: %d", got, distinct+repeats)
	}
	onDisk, err := loadFavoriteStations(f.path)
	if err != nil || !slices.Equal(onDisk, f.Stations()) {
		t.Errorf("file holds %+v (%v), memory %+v", onDisk, err, f.Stations())
	}
}

// Two ddsonics share the file. Each toggle applies what its own screen
// showed to the file as it is now: neither loses the other's station, and
// starring what the other already starred is done once.
func TestFavoritesMergeAcrossProcesses(t *testing.T) {
	a := newTestFavorites(t)
	b := &Favorites{path: a.path, byURL: map[string]struct{}{}}
	x, y := CatalogStation{Name: "X", URL: "https://x.example/"}, CatalogStation{Name: "Y", URL: "https://y.example/"}
	if _, err := a.Toggle(x); err != nil {
		t.Fatal(err)
	}
	if added, err := b.Toggle(y); err != nil || !added {
		t.Fatalf("b stars Y: %v, %v", added, err)
	}
	if got := b.Stations(); !slices.Equal(got, []CatalogStation{x, y}) {
		t.Errorf("b after starring Y = %+v, want a's X kept", got)
	}
	// a still shows only X; b shows X starred already and stars nothing new.
	revision := b.Revision()
	c := &Favorites{path: a.path, byURL: map[string]struct{}{}}
	if added, err := c.Toggle(x); err != nil || !added {
		t.Fatalf("c stars X, already starred by a: %v, %v", added, err)
	}
	if onDisk, _ := loadFavoriteStations(a.path); !slices.Equal(onDisk, []CatalogStation{x, y}) {
		t.Errorf("file = %+v, want X once and Y", onDisk)
	}
	if c.Revision() != 1 || b.Revision() != revision {
		t.Errorf("revisions: c %d, b %d; want c's own toggle counted and b untouched", c.Revision(), b.Revision())
	}
	// a unstars X by what it shows; Y, which it never loaded, stays.
	if added, err := a.Toggle(x); err != nil || added {
		t.Fatalf("a unstars X: %v, %v", added, err)
	}
	if got := a.Stations(); !slices.Equal(got, []CatalogStation{y}) {
		t.Errorf("a after unstarring X = %+v, want Y alone", got)
	}
}

// A save that fails changes nothing: not memory, not the revision, not the
// file, and the observer hears nothing. The next toggle works.
func TestFavoritesFailedSaveChangesNothing(t *testing.T) {
	f := newTestFavorites(t)
	p := New(Options{Favorites: f})
	told := 0
	p.OnFavoritesToggled(func() { told++ })
	kept, refused := CatalogStation{Name: "Kept", URL: "https://kept.example/"}, CatalogStation{Name: "Refused", URL: "https://refused.example/"}
	if _, err := f.Toggle(kept); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	// The file moves behind a link into a directory nothing can be written
	// in: it still reads, and its replacement cannot be created.
	revision, dir := f.Revision(), t.TempDir()
	target := filepath.Join(dir, favoritesFile)
	if err := os.Rename(f.path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}

	for _, s := range []CatalogStation{refused, kept} { // an add and a remove
		if _, err := f.Toggle(s); err == nil {
			t.Fatalf("toggle of %s into a read-only directory succeeded", s.Name)
		}
	}
	after, err := os.ReadFile(f.path)
	if err != nil || string(after) != string(before) {
		t.Errorf("failed saves changed the file (%v):\n%s", err, after)
	}
	if got := f.Stations(); !slices.Equal(got, []CatalogStation{kept}) || f.Revision() != revision || told != 1 {
		t.Errorf("after failed saves: stations %+v, revision %d (was %d), observer told %d times (want 1)", got, f.Revision(), revision, told)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if added, err := f.Toggle(refused); err != nil || !added || f.Revision() != revision+1 || told != 2 {
		t.Errorf("toggle after the failure: %v, %v, revision %d, observer told %d times", added, err, f.Revision(), told)
	}
}

// The observer runs with no lock held, whichever way the toggle came: it
// may read the store and the provider, as the catalog's resync does.
func TestFavoritesObserverRunsOutsideEveryLock(t *testing.T) {
	f := newTestFavorites(t)
	p := New(Options{Favorites: f})
	p.AppendCatalog([]CatalogStation{{Name: "WBGO", URL: "https://wbgo"}})
	var seen []int
	p.OnFavoritesToggled(func() {
		// Each of these takes a lock a toggle takes.
		_ = f.Contains("https://wbgo")
		_ = f.Revision()
		_ = p.LocalStationTracks()
		seen = append(seen, len(p.FavoriteTracks()))
	})
	promptly(t, "a toggle through the store", func() string {
		if _, err := f.Toggle(CatalogStation{Name: "Direct", URL: "https://direct.example/"}); err != nil {
			return err.Error()
		}
		return ""
	})
	promptly(t, "a toggle through the provider", func() string {
		if _, _, err := p.ToggleFavorite("c:0"); err != nil {
			return err.Error()
		}
		return ""
	})
	promptly(t, "a failed toggle through the provider", func() string {
		if _, _, err := p.ToggleFavorite("c:99"); err == nil {
			return "toggle of a station that is not there succeeded"
		}
		return ""
	})
	// Told after each change landed, and not for the one that failed.
	if !slices.Equal(seen, []int{1, 2}) {
		t.Errorf("observer saw %v favorites, want [1 2]", seen)
	}
}

// The provider's lock is taken before the store's, never after: toggles
// through both, readers of both and an observer that reads both run
// together without a deadlock. Run with -race.
func TestFavoritesAndProviderLocksDoNotDeadlock(t *testing.T) {
	f := newTestFavorites(t)
	p := New(Options{Favorites: f})
	p.AppendCatalog([]CatalogStation{{Name: "WBGO", URL: "https://wbgo"}, {Name: "KEXP", URL: "https://kexp"}})
	p.OnFavoritesToggled(func() { _, _ = p.FavoriteTracks(), p.LocalStationTracks() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		run := func(fn func(i int)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 25 {
					fn(i)
				}
			}()
		}
		run(func(i int) { _, _, _ = p.ToggleFavorite(fmt.Sprintf("c:%d", i%2)) })
		run(func(i int) {
			_, _ = f.Toggle(CatalogStation{Name: "Direct", URL: fmt.Sprintf("https://direct.example/%d", i%3)})
		})
		run(func(int) { _, _ = p.Playlists() })
		run(func(int) { _, _ = p.FavoriteTracks(), p.LocalStationTracks() })
		run(func(int) { _, _, _ = f.Contains("https://wbgo"), f.Revision(), f.Count() })
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("toggles and readers of the provider and the store deadlocked")
	}
	onDisk, err := loadFavoriteStations(f.path)
	if err != nil || !slices.Equal(onDisk, f.Stations()) {
		t.Errorf("file holds %+v (%v), memory %+v", onDisk, err, f.Stations())
	}
}

// A toggle through the provider (the remote's provider.favorite) waits for
// the favorites file without the provider's lock, which the UI takes to
// list the pane's rows whenever a track starts: every other provider call
// answers while the save waits.
func TestProviderIsFreeWhileItsFavoriteIsSaved(t *testing.T) {
	f := newTestFavorites(t)
	p := New(Options{Favorites: f})
	p.AppendCatalog([]CatalogStation{{Name: "WBGO", URL: "https://wbgo"}, {Name: "KEXP", URL: "https://kexp"}})
	if _, _, err := p.ToggleFavorite("c:1"); err != nil {
		t.Fatal(err)
	}
	release := holdFavoritesLock(t, f)

	type result struct {
		added bool
		name  string
		err   error
	}
	saved := make(chan result, 2)
	go func() { added, name, err := p.ToggleFavorite("c:0"); saved <- result{added, name, err} }()            // by catalog row
	go func() { added, name, err := p.ToggleFavorite("f:https://kexp"); saved <- result{added, name, err} }() // by favorite row
	for range 50 {
		promptly(t, "Playlists", func() string {
			if _, err := p.Playlists(); err != nil {
				return err.Error()
			}
			return ""
		})
		promptly(t, "the provider's other reads", func() string {
			_, _ = p.LocalStationTracks(), p.FavoriteTracks()
			if _, err := p.Tracks("c:0"); err != nil {
				return err.Error()
			}
			return ""
		})
		time.Sleep(time.Millisecond)
	}
	select {
	case r := <-saved:
		t.Fatalf("a toggle did not wait for the file lock: %+v", r)
	default:
	}

	release()
	got := map[string]bool{}
	for range 2 {
		r := <-saved
		if r.err != nil {
			t.Fatal(r.err)
		}
		got[r.name] = r.added
	}
	// Each toggled the station it named when asked, whatever landed first.
	if want := map[string]bool{"WBGO": true, "KEXP": false}; !maps.Equal(got, want) {
		t.Errorf("toggles = %v, want %v", got, want)
	}
	if got := f.Stations(); len(got) != 1 || got[0].Name != "WBGO" {
		t.Errorf("favorites = %+v, want WBGO alone", got)
	}
}
