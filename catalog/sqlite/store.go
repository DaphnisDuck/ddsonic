// Package sqlite implements catalog.Catalog on a SQLite database using the
// pure-Go modernc.org/sqlite driver.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	msqlite "modernc.org/sqlite" // also registers the "sqlite" driver
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/bjarneo/cliamp/catalog"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrSchemaTooNew means the database was migrated by a newer ddsonic than
// this one, so this binary must not write to it.
var ErrSchemaTooNew = errors.New("catalog database schema is newer than this ddsonic")

// dsnParams apply to every pooled connection. WAL lets the UI read while a
// sync writes; _txlock=immediate takes the write lock when a transaction
// begins, so a sync never fails upgrading a read lock mid-transaction.
const dsnParams = "_journal_mode=WAL&_foreign_keys=on&_synchronous=NORMAL&_txlock=immediate"

// Busy timeouts, in milliseconds. Only background syncs write, and another
// ddsonic on the same catalog can hold the write lock for a whole snapshot
// (a local index takes seconds), so the writer waits long; reads never wait
// on a writer under WAL.
const (
	readBusyTimeout  = 5000
	writeBusyTimeout = 60000
)

// Store is a catalog.Catalog and catalog.Writer backed by SQLite. Reads use
// a pooled handle; writes go through a single-connection handle, so writers
// queue in Go rather than contending for SQLite's write lock.
type Store struct {
	db  *sql.DB // reads
	wdb *sql.DB // writes; one connection
}

var (
	_ catalog.Catalog = (*Store)(nil)
	_ catalog.Writer  = (*Store)(nil)
)

// Open opens (creating if needed) the catalog at dbPath and applies pending
// migrations. Several processes may open the same new database at once.
func Open(ctx context.Context, dbPath string) (*Store, error) {
	if strings.ContainsAny(dbPath, "?#") {
		return nil, fmt.Errorf("catalog path %q: must not contain ? or #", dbPath)
	}
	// Track URIs can carry stream tokens, so the catalog is private to the
	// user even if its directory already existed with looser permissions.
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create catalog directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure catalog directory: %w", err)
	}
	if err := securePrivateFiles(dbPath); err != nil {
		return nil, err
	}
	dsn := dbPath + "?" + dsnParams + "&_busy_timeout="
	wdb, err := sql.Open("sqlite", dsn+strconv.Itoa(writeBusyTimeout))
	if err != nil {
		return nil, fmt.Errorf("open catalog %s: %w", dbPath, err)
	}
	wdb.SetMaxOpenConns(1)
	if err := retryBusy(ctx, func() error { return migrate(ctx, wdb, migrationFS) }); err != nil {
		wdb.Close()
		return nil, err
	}
	if err := retryBusy(ctx, func() error { return refreshSortKeys(ctx, wdb) }); err != nil {
		wdb.Close()
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn+strconv.Itoa(readBusyTimeout))
	if err != nil {
		wdb.Close()
		return nil, fmt.Errorf("open catalog %s: %w", dbPath, err)
	}
	// Search reads each kind on its own connection at once, and a browse
	// query may run beside it: keep one connection per kind plus one open.
	db.SetMaxIdleConns(len(catalog.SearchKinds) + 1)
	return &Store{db: db, wdb: wdb}, nil
}

// securePrivateFiles creates the database file private to the user, and
// makes an existing one and its WAL files private too. SQLite gives the WAL
// files it creates the database file's mode.
func securePrivateFiles(dbPath string) error {
	f, err := os.OpenFile(dbPath, os.O_RDONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("create catalog %s: %w", dbPath, err)
	}
	f.Close()
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("secure catalog file: %w", err)
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error { return errors.Join(s.db.Close(), s.wdb.Close()) }

// migrationBusyWindow bounds how long Open retries a migration that another
// process is initializing the same new database file for.
const migrationBusyWindow = 5 * time.Second

// retryBusy retries fn while it fails with SQLITE_BUSY. SQLite returns BUSY
// without waiting on busy_timeout while another connection initializes a new
// WAL database, so two processes opening one new catalog can collide. fn must
// be idempotent; migrate is, because each step re-checks the version.
func retryBusy(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(migrationBusyWindow)
	for attempt := 1; ; attempt++ {
		err := fn()
		if !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(time.Duration(attempt)*20*time.Millisecond, 200*time.Millisecond)):
		}
	}
}

func isBusy(err error) bool {
	var e *msqlite.Error
	return errors.As(err, &e) && e.Code()&0xff == sqlite3.SQLITE_BUSY
}

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads migrations/NNN_name.sql from fsys, ordered by NNN.
// Versions must run 1, 2, 3… with no gaps or repeats.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	paths, err := fs.Glob(fsys, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var out []migration
	for _, p := range paths {
		name := path.Base(p)
		num, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(num)
		if !ok || err != nil || version < 1 {
			return nil, fmt.Errorf("migration %q: name must be NNN_description.sql", name)
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}
	slices.SortFunc(out, func(a, b migration) int { return a.version - b.version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %s: expected version %d", m.name, i+1)
		}
	}
	return out, nil
}

// migrate applies every migration newer than the database's version, each in
// its own transaction together with its schema_migrations row, so a failing
// migration leaves the database at the previous version. applyMigration
// re-checks the version under the write lock, so a process that loses a race
// to open a new database skips what the winner already applied.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	current, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("%w (database v%d, supported v%d)", ErrSchemaTooNew, current, len(migrations))
	}
	for _, m := range migrations[current:] {
		if err := applyMigration(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

// schemaVersion creates the version table if needed and returns the applied
// version. It runs in a write transaction because SQLite's busy handler does
// not always wait for a plain CREATE racing another process on a new file.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	tx, err := db.BeginTx(ctx, nil) // BEGIN IMMEDIATE via _txlock
	if err != nil {
		return 0, fmt.Errorf("read schema version: begin: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return 0, fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("read schema version: commit: %w", err)
	}
	return current, nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil) // BEGIN IMMEDIATE via _txlock
	if err != nil {
		return fmt.Errorf("migration %s: begin: %w", m.name, err)
	}
	defer tx.Rollback() // no-op after Commit
	var applied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = ?)`, m.version).Scan(&applied); err != nil {
		return fmt.Errorf("migration %s: check version: %w", m.name, err)
	}
	if applied {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("migration %s: record version: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migration %s: commit: %w", m.name, err)
	}
	return nil
}

// refreshSortKeys recomputes the stored sort keys when they were written by
// an older catalog.SortKey. The database's user_version records the key
// version, so this runs once per change of the rules.
func refreshSortKeys(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("read sort key version: %w", err)
	}
	if version >= catalog.SortKeyVersion {
		return nil
	}
	if err := rewriteSortKeys(ctx, db); err != nil {
		return fmt.Errorf("refresh sort keys: %w", err)
	}
	return nil
}

// rewriteSortKeys recomputes every artist's and album's sort keys and
// records the key version, in one transaction.
func rewriteSortKeys(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// rekey runs update, prepared once, with the keys of each (id, name,
	// credit) row that query reads.
	rekey := func(query, update string, keys func(name, credit string) []any) error {
		type row struct {
			id           int64
			name, credit string
		}
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		var all []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.name, &r.credit); err != nil {
				rows.Close()
				return err
			}
			all = append(all, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx, update)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range all {
			if _, err := stmt.ExecContext(ctx, append(keys(r.name, r.credit), r.id)...); err != nil {
				return err
			}
		}
		return nil
	}

	if err := rekey(`SELECT id, name, '' FROM artists`, `UPDATE artists SET sort_name = ? WHERE id = ?`,
		func(name, _ string) []any { return []any{catalog.SortKey(name)} }); err != nil {
		return err
	}
	// An album sorts under its display credit when the writer set one (a
	// local index: the artist or Various Artists), else its first artist.
	if err := rekey(`SELECT al.id, al.title, CASE WHEN al.provider = '`+catalog.Local+`' THEN al.artist_credit
			ELSE COALESCE((SELECT ar.name FROM album_artists x JOIN artists ar ON ar.id = x.artist_id
				WHERE x.album_id = al.id ORDER BY x.position LIMIT 1), '') END
		FROM albums al`, `UPDATE albums SET sort_title = ?, sort_artist = ? WHERE id = ?`,
		func(title, credit string) []any {
			sortArtist := ""
			if credit != "" {
				sortArtist = catalog.SortKey(credit)
			}
			return []any{catalog.SortKey(title), sortArtist}
		}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, catalog.SortKeyVersion)); err != nil {
		return err
	}
	return tx.Commit()
}
