package localsrc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
)

// TestIndexRealLibrary times a first index of a real music folder into an
// empty catalog, reading tags and writing apart. It runs only when
// DDSONIC_BENCH_MUSIC names the folder:
//
//	DDSONIC_BENCH_MUSIC=/path/to/music go test ./catalogsync/localsrc -run RealLibrary -v
func TestIndexRealLibrary(t *testing.T) {
	dir := os.Getenv("DDSONIC_BENCH_MUSIC")
	if dir == "" {
		t.Skip("DDSONIC_BENCH_MUSIC not set")
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := New(dir, store)
	start := time.Now()
	snap, err := src.Fetch(ctx, Files, catalogsync.Known{})
	if err != nil {
		t.Fatal(err)
	}
	read := time.Since(start)
	start = time.Now()
	if err := store.ApplySnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d files: read %v, write %v", len(snap.Tracks), read.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
}
