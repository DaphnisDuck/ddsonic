package localsrc

// ddsonic: the implicit default music folder (#82).

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/playlist"
)

// reopen replaces l's store, source and engine with fresh ones on the same
// database and folder, as a restart does. implicit picks NewDefault.
func (l *library) reopen(dbPath string, implicit bool) {
	l.t.Helper()
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { store.Close() })
	l.store = store
	if implicit {
		l.src = NewDefault(l.dir, store)
	} else {
		l.src = New(l.dir, store)
	}
	l.src.readTags = func(paths []string) []playlist.Track {
		l.mu.Lock()
		defer l.mu.Unlock()
		out := make([]playlist.Track, len(paths))
		for i, p := range paths {
			rel, _ := filepath.Rel(l.dir, p)
			l.reads = append(l.reads, rel)
			out[i] = l.tags[rel]
		}
		return out
	}
	l.eng = catalogsync.New(store, nil, l.src)
}

// newFolderLibrary indexes dir, which need not exist, into a new database.
func newFolderLibrary(t *testing.T, dir string, implicit bool) (*library, string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "library.db")
	l := &library{t: t, dir: dir, tags: map[string]playlist.Track{}}
	l.reopen(db, implicit)
	return l, db
}

func (l *library) status() catalog.CollectionStatus {
	l.t.Helper()
	st, err := l.store.SyncStatus(context.Background(), catalog.Local)
	if err != nil || len(st) != 1 {
		l.t.Fatalf("status = %+v, %v; want one collection", st, err)
	}
	return st[0]
}

func wantAbsent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s: stat = %v, want it still absent", dir, err)
	}
}

// A fresh home with no ~/Music: the default folder is an empty library, not
// a failed sync, at every startup, and it is not created.
func TestDefaultFolderNeverCreatedIsEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Music")
	l, db := newFolderLibrary(t, dir, true)
	for run := 1; run <= 3; run++ {
		if err := l.sync(); err != nil {
			t.Fatalf("run %d: sync = %v, want an empty library", run, err)
		}
		if st := l.status(); st.LastError != "" || st.LastSuccess.IsZero() {
			t.Fatalf("run %d: status = %+v, want a success", run, st)
		}
		if got := l.albums(); len(got) != 0 {
			t.Errorf("run %d: albums = %v, want none", run, got)
		}
		wantAbsent(t, dir)
		l.store.Close()
		l.reopen(db, true) // a restart
	}
}

// Once the default folder exists, it indexes as any folder does.
func TestDefaultFolderIndexesOnceCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Music")
	l, _ := newFolderLibrary(t, dir, true)
	if err := l.sync(); err != nil {
		t.Fatal(err)
	}
	l.write("a/1.mp3", "1", playlist.Track{Title: "1", Artist: "A", Album: "One"})
	if err := l.sync(); err != nil {
		t.Fatal(err)
	}
	if got, want := l.albums(), map[string][]string{"One": {"1"}}; !equalAlbums(got, want) {
		t.Errorf("albums = %v, want %v", got, want)
	}
	if st := l.status(); st.LastError != "" {
		t.Errorf("status = %+v, want a success", st)
	}
}

// What stays a failure: a folder the user configured, a default folder
// that held an indexed library (an unmounted drive), one that cannot be
// read, and one that is not a directory.
func TestDefaultFolderFailuresStay(t *testing.T) {
	t.Run("configured folder missing", func(t *testing.T) {
		l, _ := newFolderLibrary(t, filepath.Join(t.TempDir(), "Music"), false)
		if err := l.sync(); err == nil {
			t.Fatal("indexing a missing configured folder succeeded")
		}
		if st := l.status(); st.LastError == "" || !st.LastSuccess.IsZero() {
			t.Errorf("status = %+v, want the failure recorded", st)
		}
	})
	t.Run("default folder gone after indexing", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "Music")
		l, _ := newFolderLibrary(t, dir, true)
		l.write("a/1.mp3", "1", playlist.Track{Title: "1", Artist: "A", Album: "One"})
		if err := l.sync(); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		// The folder's own error, not the empty-folder guard's.
		if err := l.sync(); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("indexing a vanished default folder over a full index = %v, want its not-exist error", err)
		}
		if got, want := l.albums(), map[string][]string{"One": {"1"}}; !equalAlbums(got, want) {
			t.Errorf("albums = %v, want %v kept", got, want)
		}
		if st := l.status(); st.LastError == "" {
			t.Errorf("status = %+v, want the failure recorded", st)
		}
	})
	t.Run("default folder unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads every folder")
		}
		parent := t.TempDir()
		if err := os.Chmod(parent, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(parent, 0o755) })
		l, _ := newFolderLibrary(t, filepath.Join(parent, "Music"), true)
		if err := l.sync(); err == nil {
			t.Error("indexing an unreadable default folder succeeded")
		}
	})
	t.Run("default folder is a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "Music")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		l, _ := newFolderLibrary(t, path, true)
		if err := l.sync(); err == nil {
			t.Error("indexing a default folder that is a file succeeded")
		}
	})
}
