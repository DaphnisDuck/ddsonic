package model

// ddsonic: Local on a fresh home with no ~/Music (#82), from the real index
// through the library's screen.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/catalogsync/localsrc"
	"github.com/bjarneo/cliamp/library"
)

// indexedLocalModel indexes src's folder, dir, once into a new catalog and
// shows its Local menu with the stored status, as a startup does.
func indexedLocalModel(t *testing.T, dir string, src func(string, localsrc.Index) *localsrc.Source) Model {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	_ = catalogsync.New(store, nil, src(dir, store)).Sync(ctx, catalog.Local)
	stored, err := store.SyncStatus(ctx, catalog.Local)
	if err != nil || len(stored) != 1 {
		t.Fatalf("status = %+v, %v", stored, err)
	}
	m := newLibraryModel(library.LocalCatalog(store, nil, dir))
	m.SetCatalogSync(map[string]CatalogStatus{catalog.Local: {
		LastSuccess: stored[0].LastSuccess, LastError: stored[0].LastError}}, func(string) {})
	return sized(m, 100, 30)
}

const indexingRow = "Indexing your music… (press r to retry if this persists)"

func TestLocalWithoutDefaultMusicFolder(t *testing.T) {
	// A long home, as macOS's temporary folders are, must pass too.
	long := filepath.Join(t.TempDir(), strings.Repeat("h", 100))
	for _, dir := range []string{filepath.Join(t.TempDir(), "Music"), filepath.Join(long, "Music")} {
		wantView(t, "Local's menu", indexedLocalModel(t, dir, localsrc.NewDefault), []string{"Albums", "✓ synced"}, "sync failed", "cached")
		for _, list := range []string{"Albums", "Artists", "Genres"} {
			in := openRow(t, indexedLocalModel(t, dir, localsrc.NewDefault), list)
			// The screen cuts a long row to its width, so it is checked by
			// its start; the row itself must name the folder in full.
			wantView(t, list, in, []string{"No music found in", "✓ synced"}, "sync failed", indexingRow, "cached")
			if rows := in.libTop().entries; len(rows) != 1 || rows[0].Title != "No music found in "+dir {
				t.Errorf("%s (%d-character folder): rows = %+v, want one row naming the folder", list, len(dir), rows)
			}
		}
	}
}

// A folder the user configured that does not exist still says so.
func TestLocalConfiguredMusicFolderMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Music")
	m := openRow(t, indexedLocalModel(t, dir, localsrc.New), "Albums")
	wantView(t, "Albums", m, []string{"sync failed"}, "No music found", "✓ synced")
}
