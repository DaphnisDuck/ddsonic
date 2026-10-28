package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/bjarneo/cliamp/catalog"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "sub", "library.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func appliedVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestOpenMigratesAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := loadMigrations(migrationFS)
	if got := appliedVersion(t, s.db); got != len(want) {
		t.Fatalf("schema version = %d, want %d", got, len(want))
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", mode, err)
	}
	s.Close()

	// Reopening applies nothing twice.
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	var rows int
	s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&rows)
	if rows != len(want) {
		t.Errorf("schema_migrations rows = %d, want %d", rows, len(want))
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (999, 0)`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("Open() error = %v, want ErrSchemaTooNew", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fsys := fstest.MapFS{
		"migrations/001_ok.sql":     {Data: []byte(`CREATE TABLE a (x INTEGER);`)},
		"migrations/002_broken.sql": {Data: []byte(`CREATE TABLE b (y INTEGER); THIS IS NOT SQL;`)},
	}
	if err := migrate(context.Background(), db, fsys); err == nil {
		t.Fatal("migrate() error = nil, want the broken migration to fail")
	}
	if got := appliedVersion(t, db); got != 1 {
		t.Errorf("schema version = %d, want 1", got)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'b'`).Scan(&n)
	if n != 0 {
		t.Error("table b from the failed migration survived its rollback")
	}
}

func TestLoadMigrationsRejectsGaps(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"gap":      {"migrations/001_a.sql": {}, "migrations/003_c.sql": {}},
		"bad name": {"migrations/first.sql": {}},
	}
	for name, fsys := range tests {
		if _, err := loadMigrations(fsys); err == nil {
			t.Errorf("%s: loadMigrations() error = nil", name)
		}
	}
}

// M3's unified search needs FTS5 compiled into the driver.
func TestFTS5Available(t *testing.T) {
	s := openTemp(t)
	if _, err := s.db.Exec(`CREATE VIRTUAL TABLE fts_probe USING fts5(title)`); err != nil {
		t.Fatalf("FTS5 unavailable: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO fts_probe (title) VALUES ('Mahler Symphony No. 5')`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM fts_probe WHERE fts_probe MATCH 'mahler'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("MATCH count = %d (%v), want 1", n, err)
	}
}

func TestConstraints(t *testing.T) {
	s := openTemp(t)
	exec := func(q string, args ...any) error { _, err := s.db.Exec(q, args...); return err }
	if err := exec(`INSERT INTO artists (provider, provider_id, name, sort_name, updated_at) VALUES ('spotify', 'a1', 'X', 'x', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO artists (provider, provider_id, name, sort_name, updated_at) VALUES ('spotify', 'a1', 'Y', 'y', 0)`); err == nil {
		t.Error("duplicate (provider, provider_id) accepted")
	}
	if err := exec(`INSERT INTO tracks (provider, provider_id, title, album_id, playable_uri, updated_at) VALUES ('spotify', 't1', 'T', 9999, 'u', 0)`); err == nil {
		t.Error("track with a missing album accepted: foreign keys are off")
	}
	if err := exec(`INSERT INTO library_items (provider, collection, kind, item_id, added_at, last_seen_gen) VALUES ('spotify', 'x', 'bogus', 1, 0, 0)`); err == nil {
		t.Error("unknown library kind accepted")
	}
}

// Several processes (here, goroutines with their own pools) opening the same
// new database at once must all succeed and apply each migration once.
func TestConcurrentOpenMigratesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	const openers = 4
	errs := make(chan error, openers)
	stores := make(chan *Store, openers)
	for range openers {
		go func() {
			s, err := Open(context.Background(), path)
			errs <- err
			stores <- s
		}()
	}
	for range openers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Open() error = %v", err)
		}
		if s := <-stores; s != nil {
			defer s.Close()
		}
	}
	check, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var rows int
	check.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&rows)
	want, _ := loadMigrations(migrationFS)
	if rows != len(want) {
		t.Errorf("schema_migrations rows = %d, want %d", rows, len(want))
	}
}

func TestOpenRejectsDSNCharacters(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "odd?name.db")); err == nil {
		t.Error("Open() accepted a path containing ?")
	}
}

func TestOpenSecuresDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), filepath.Join(dir, "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("directory mode = %v (%v), want 0700", info.Mode().Perm(), err)
	}
}

// A catalog written with older sort keys gets them recomputed on open, once.
func TestOpenRefreshesSortKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{
		{Ref: sref("a"), Title: "The Planets", Artists: []catalog.ArtistRecord{artistRec("ar", "The Holst Singers")}},
	}})
	apply(t, s, catalog.Snapshot{Provider: catalog.Local, Collection: "files", Albums: []catalog.AlbumRecord{
		{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "l"}, Title: "A Mix", Credit: "Various Artists",
			Artists: []catalog.ArtistRecord{{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "x"}, Name: "X"}}},
	}})
	// Simulate keys from the old rule, which dropped leading articles.
	if _, err := s.wdb.Exec(`UPDATE albums SET sort_title = 'planets', sort_artist = 'holst singers' WHERE provider_id = 'a';
		UPDATE albums SET sort_title = 'mix', sort_artist = 'x' WHERE provider_id = 'l';
		UPDATE artists SET sort_name = 'holst singers' WHERE provider_id = 'ar';
		PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	keys := func(query string) string {
		t.Helper()
		var a, b string
		if err := s.db.QueryRow(query).Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		return a + " / " + b
	}
	if got := keys(`SELECT sort_title, sort_artist FROM albums WHERE provider_id = 'a'`); got != "the planets / the holst singers" {
		t.Errorf("Spotify album keys = %q", got)
	}
	if got := keys(`SELECT sort_title, sort_artist FROM albums WHERE provider_id = 'l'`); got != "a mix / various artists" {
		t.Errorf("local album keys = %q, want its credit's", got)
	}
	if got := keys(`SELECT sort_name, name FROM artists WHERE provider_id = 'ar'`); got != "the holst singers / The Holst Singers" {
		t.Errorf("artist keys = %q", got)
	}
	var version int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != catalog.SortKeyVersion {
		t.Errorf("user_version = %d, want %d", version, catalog.SortKeyVersion)
	}
}

// The catalog and its WAL files are private to the user, including a
// database created with looser permissions before this rule.
func TestOpenMakesTheCatalogPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, p := range []string{path, path + "-wal"} {
		if err := os.Chmod(p, 0o644); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	s, err = Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.RecordSyncSuccess(context.Background(), "local", "files"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		fi, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}

// The writer waits out another process's long write transaction; reads keep
// the short timeout.
func TestBusyTimeouts(t *testing.T) {
	s := openTemp(t)
	for _, c := range []struct {
		db   *sql.DB
		want int
	}{{s.wdb, writeBusyTimeout}, {s.db, readBusyTimeout}} {
		var got int
		if err := c.db.QueryRow(`PRAGMA busy_timeout`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("busy_timeout = %d, want %d", got, c.want)
		}
	}
}
