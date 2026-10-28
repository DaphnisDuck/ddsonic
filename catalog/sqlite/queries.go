package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// Library list queries filter entities by membership (id IN library_items),
// which lists an item once even when two collections hold it, and read the
// credits denormalized onto albums and tracks, so no row needs a join.

// memberIDs selects the item IDs of a provider's library members of a kind.
const memberIDs = `SELECT item_id FROM library_items WHERE provider = ? AND kind = ?`

// anyMemberIDs is memberIDs where an empty provider means every provider;
// its provider argument is given twice.
const anyMemberIDs = `SELECT item_id FROM library_items WHERE (? = '' OR provider = ?) AND kind = ?`

const albumColumns = `al.id, al.provider, al.provider_id, al.title, al.artist_credit,
	al.year, al.track_count, al.artwork_url, al.tracks_cached_at IS NOT NULL`

const trackColumns = `t.id, t.provider, t.provider_id, t.title, t.artist_credit,
	COALESCE(t.album_id, 0), COALESCE(al.title, t.album_title), t.disc, t.track_no,
	t.duration_ms, t.playable_uri, t.genre, t.year, COALESCE(al.artwork_url, '')`

// Albums implements catalog.Catalog.
func (s *Store) Albums(ctx context.Context, provider string, order catalog.AlbumOrder) ([]catalog.Album, error) {
	orderBy := "al.sort_title, al.sort_artist"
	if order == catalog.ByArtist {
		orderBy = "al.sort_artist, al.sort_title"
	}
	return queryAll(ctx, s.db, scanAlbum, `SELECT `+albumColumns+` FROM albums al
		WHERE al.id IN (`+anyMemberIDs+`)
		ORDER BY `+orderBy+`, al.id`, provider, provider, catalog.KindAlbum)
}

// Album implements catalog.Catalog.
func (s *Store) Album(ctx context.Context, id int64) (catalog.Album, error) {
	albums, err := queryAll(ctx, s.db, scanAlbum, `SELECT `+albumColumns+` FROM albums al WHERE al.id = ?`, id)
	if err != nil {
		return catalog.Album{}, err
	}
	if len(albums) == 0 {
		return catalog.Album{}, fmt.Errorf("album %d: %w", id, catalog.ErrNotFound)
	}
	return albums[0], nil
}

// betweenReads runs between the statements of a read: after a by-ref
// lookup, and between an album's cached flag and its tracks. Tests replace
// it to commit a write there.
var betweenReads = func() {}

// AlbumTracks implements catalog.Catalog.
func (s *Store) AlbumTracks(ctx context.Context, albumID int64) (tracks []catalog.Track, cached bool, err error) {
	err = s.read(ctx, func(tx *sql.Tx) error {
		tracks, cached, err = albumTracks(ctx, tx, albumID)
		return err
	})
	return tracks, cached, err
}

// AlbumTracksByRef implements catalog.Catalog.
func (s *Store) AlbumTracksByRef(ctx context.Context, ref catalog.Ref) (tracks []catalog.Track, cached bool, err error) {
	err = s.read(ctx, func(tx *sql.Tx) error {
		id, err := refID(ctx, tx, catalog.KindAlbum, ref)
		if err != nil {
			return err
		}
		tracks, cached, err = albumTracks(ctx, tx, id)
		return err
	})
	return tracks, cached, err
}

// read runs fn in one read-only transaction on the read handle, so all it
// reads comes from one snapshot even when a sync commits meanwhile.
// ReadOnly makes the driver begin a deferred transaction rather than the
// DSN's BEGIN IMMEDIATE, so a read never takes the write lock.
func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("catalog read: %w", err)
	}
	defer tx.Rollback()
	return fn(tx)
}

// albumTracks reads an album's cached flag and tracks within tx.
func albumTracks(ctx context.Context, tx *sql.Tx, albumID int64) ([]catalog.Track, bool, error) {
	var cached bool
	err := tx.QueryRowContext(ctx, `SELECT tracks_cached_at IS NOT NULL FROM albums WHERE id = ?`, albumID).Scan(&cached)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("album %d: %w", albumID, catalog.ErrNotFound)
	}
	if err != nil {
		return nil, false, fmt.Errorf("album %d: %w", albumID, err)
	}
	betweenReads()
	// A cached album lists exactly its cached tracks; an uncached one, what
	// the catalog knows of it from other collections.
	query := `SELECT ` + trackColumns + `
		FROM tracks t LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.album_id = ?
		ORDER BY t.disc, t.track_no, t.title`
	if cached {
		query = `SELECT ` + trackColumns + `
		FROM album_tracks x JOIN tracks t ON t.id = x.track_id LEFT JOIN albums al ON al.id = x.album_id
		WHERE x.album_id = ?
		ORDER BY t.disc, t.track_no, t.title`
	}
	tracks, err := queryAll(ctx, tx, scanTrack, query, albumID)
	if err != nil {
		return nil, false, err
	}
	return tracks, cached, nil
}

// kindTables names each entity kind's table.
var kindTables = map[catalog.Kind]string{
	catalog.KindAlbum: "albums", catalog.KindArtist: "artists", catalog.KindPlaylist: "playlists",
}

// refID returns the catalog ID the entity of kind with ref has now, within
// tx, or ErrNotFound.
func refID(ctx context.Context, tx *sql.Tx, kind catalog.Kind, ref catalog.Ref) (int64, error) {
	table, ok := kindTables[kind]
	if !ok {
		return 0, fmt.Errorf("catalog %s: unknown kind", kind)
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE provider = ? AND provider_id = ?`,
		ref.Provider, ref.ProviderID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%s %s: %w", kind, ref.ProviderID, catalog.ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("%s %s: %w", kind, ref.ProviderID, err)
	}
	betweenReads()
	return id, nil
}

// UncachedAlbums implements catalogsync.AlbumStore: provider's library
// albums whose track lists are not cached, most recently saved first.
func (s *Store) UncachedAlbums(ctx context.Context, provider string) ([]catalog.Album, error) {
	return queryAll(ctx, s.db, scanAlbum, `SELECT `+albumColumns+` FROM albums al
		JOIN (SELECT item_id, max(added_at) AS added_at FROM library_items
			WHERE provider = ? AND kind = ? GROUP BY item_id) li ON li.item_id = al.id
		WHERE al.tracks_cached_at IS NULL
		ORDER BY li.added_at DESC, al.id`, provider, catalog.KindAlbum)
}

// Artists implements catalog.Catalog.
func (s *Store) Artists(ctx context.Context, provider string) ([]catalog.Artist, error) {
	return queryAll(ctx, s.db, func(r *sql.Rows) (a catalog.Artist, err error) {
		err = r.Scan(&a.ID, &a.Ref.Provider, &a.Ref.ProviderID, &a.Name, &a.ImageURL)
		return a, err
	}, `SELECT ar.id, ar.provider, ar.provider_id, ar.name, ar.image_url FROM artists ar
		WHERE ar.id IN (`+anyMemberIDs+`)
		ORDER BY ar.sort_name, ar.provider`, provider, provider, catalog.KindArtist)
}

// ArtistAlbums implements catalog.Catalog.
func (s *Store) ArtistAlbums(ctx context.Context, artistID int64) ([]catalog.Album, error) {
	return artistAlbums(ctx, s.db, artistID)
}

// ArtistAlbumsByRef implements catalog.Catalog.
func (s *Store) ArtistAlbumsByRef(ctx context.Context, ref catalog.Ref) (albums []catalog.Album, err error) {
	err = s.read(ctx, func(tx *sql.Tx) error {
		id, err := refID(ctx, tx, catalog.KindArtist, ref)
		if err != nil {
			return err
		}
		albums, err = artistAlbums(ctx, tx, id)
		return err
	})
	return albums, err
}

func artistAlbums(ctx context.Context, q querier, artistID int64) ([]catalog.Album, error) {
	return queryAll(ctx, q, scanAlbum, `SELECT `+albumColumns+`
		FROM album_artists x JOIN albums al ON al.id = x.album_id
		WHERE x.artist_id = ?
		ORDER BY al.year DESC, al.sort_title`, artistID)
}

// Playlists implements catalog.Catalog.
func (s *Store) Playlists(ctx context.Context, provider string) ([]catalog.Playlist, error) {
	return queryAll(ctx, s.db, func(r *sql.Rows) (p catalog.Playlist, err error) {
		err = r.Scan(&p.ID, &p.Ref.Provider, &p.Ref.ProviderID, &p.Name, &p.Own, &p.TrackCount)
		return p, err
	}, `SELECT p.id, p.provider, p.provider_id, p.name, p.own, p.track_count FROM playlists p
		WHERE p.id IN (`+memberIDs+`)
		ORDER BY p.own DESC, lower(p.name)`, provider, catalog.KindPlaylist)
}

// PlaylistTracks implements catalog.Catalog.
func (s *Store) PlaylistTracks(ctx context.Context, playlistID int64) ([]catalog.Track, error) {
	return playlistTracks(ctx, s.db, playlistID)
}

// PlaylistTracksByRef implements catalog.Catalog.
func (s *Store) PlaylistTracksByRef(ctx context.Context, ref catalog.Ref) (tracks []catalog.Track, err error) {
	err = s.read(ctx, func(tx *sql.Tx) error {
		id, err := refID(ctx, tx, catalog.KindPlaylist, ref)
		if err != nil {
			return err
		}
		tracks, err = playlistTracks(ctx, tx, id)
		return err
	})
	return tracks, err
}

func playlistTracks(ctx context.Context, q querier, playlistID int64) ([]catalog.Track, error) {
	return queryAll(ctx, q, scanTrack, `SELECT `+trackColumns+`
		FROM playlist_tracks pt JOIN tracks t ON t.id = pt.track_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE pt.playlist_id = ?
		ORDER BY pt.position`, playlistID)
}

// Genres implements catalog.Catalog. SQLite's lower() folds ASCII only,
// which covers genre tags in practice.
func (s *Store) Genres(ctx context.Context, provider string) ([]catalog.Genre, error) {
	return queryAll(ctx, s.db, func(r *sql.Rows) (g catalog.Genre, err error) {
		err = r.Scan(&g.Name, &g.AlbumCount)
		return g, err
	}, `SELECT min(t.genre), count(DISTINCT t.album_id) FROM tracks t
		WHERE t.provider = ? AND t.genre <> '' AND t.album_id IN (`+memberIDs+`)
		GROUP BY lower(t.genre)
		ORDER BY lower(t.genre)`, provider, provider, catalog.KindAlbum)
}

// GenreAlbums implements catalog.Catalog.
func (s *Store) GenreAlbums(ctx context.Context, provider, genre string) ([]catalog.Album, error) {
	return queryAll(ctx, s.db, scanAlbum, `SELECT `+albumColumns+` FROM albums al
		WHERE al.id IN (`+memberIDs+`)
			AND al.id IN (SELECT album_id FROM tracks WHERE provider = ? AND lower(genre) = lower(?))
		ORDER BY al.sort_title, al.sort_artist`, provider, catalog.KindAlbum, provider, genre)
}

// IndexedFiles returns the local file index with each file's track, for
// the local indexer to skip files that have not changed. Files whose track
// is gone are left out, so they are read again.
func (s *Store) IndexedFiles(ctx context.Context) ([]catalog.IndexedFile, error) {
	return queryAll(ctx, s.db, func(r *sql.Rows) (f catalog.IndexedFile, err error) {
		var durationMS int64
		t := &f.Track
		err = r.Scan(&f.Path, &f.Size, &f.MTimeNS,
			&t.ID, &t.Ref.Provider, &t.Ref.ProviderID, &t.Title, &t.Artist,
			&t.AlbumID, &t.AlbumTitle, &t.Disc, &t.TrackNo, &durationMS, &t.PlayableURI,
			&t.Genre, &t.Year, &t.ArtworkURL)
		t.Duration = time.Duration(durationMS) * time.Millisecond
		return f, err
	}, `SELECT lf.path, lf.size, lf.mtime_ns, `+trackColumns+`
		FROM local_files lf JOIN tracks t ON t.id = lf.track_id
		LEFT JOIN albums al ON al.id = t.album_id`)
}

// LikedTracks implements catalog.Catalog.
func (s *Store) LikedTracks(ctx context.Context, provider string) ([]catalog.Track, error) {
	return queryAll(ctx, s.db, scanTrack, `SELECT `+trackColumns+`
		FROM library_items li JOIN tracks t ON t.id = li.item_id
		LEFT JOIN albums al ON al.id = t.album_id
		WHERE li.provider = ? AND li.kind = ?
		ORDER BY li.added_at DESC, t.id`, provider, catalog.KindTrack)
}

// SyncStatus implements catalog.Catalog.
func (s *Store) SyncStatus(ctx context.Context, provider string) ([]catalog.CollectionStatus, error) {
	return queryAll(ctx, s.db, func(r *sql.Rows) (st catalog.CollectionStatus, err error) {
		var attempt, success sql.NullInt64
		err = r.Scan(&st.Collection, &attempt, &success, &st.LastError)
		st.LastAttempt, st.LastSuccess = millisTime(attempt), millisTime(success)
		return st, err
	}, `SELECT collection, last_attempt_at, last_success_at, last_error
		FROM sync_state WHERE provider = ? ORDER BY collection`, provider)
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// queryAll runs query and scans every row with scan.
func queryAll[T any](ctx context.Context, db querier, scan func(*sql.Rows) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("catalog query: %w", err)
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("catalog scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog rows: %w", err)
	}
	return out, nil
}

func scanAlbum(r *sql.Rows) (catalog.Album, error) { return scanAlbumWith(r) }

// scanAlbumWith scans albumColumns, then extra columns into extra.
func scanAlbumWith(r *sql.Rows, extra ...any) (a catalog.Album, err error) {
	err = r.Scan(append([]any{&a.ID, &a.Ref.Provider, &a.Ref.ProviderID, &a.Title, &a.Artist,
		&a.Year, &a.TrackCount, &a.ArtworkURL, &a.TracksCached}, extra...)...)
	return a, err
}

func scanTrack(r *sql.Rows) (catalog.Track, error) { return scanTrackWith(r) }

// scanTrackWith scans trackColumns, then extra columns into extra.
func scanTrackWith(r *sql.Rows, extra ...any) (t catalog.Track, err error) {
	var durationMS int64
	err = r.Scan(append([]any{&t.ID, &t.Ref.Provider, &t.Ref.ProviderID, &t.Title, &t.Artist,
		&t.AlbumID, &t.AlbumTitle, &t.Disc, &t.TrackNo, &durationMS, &t.PlayableURI,
		&t.Genre, &t.Year, &t.ArtworkURL}, extra...)...)
	t.Duration = time.Duration(durationMS) * time.Millisecond
	return t, err
}

func millisTime(v sql.NullInt64) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return time.UnixMilli(v.Int64)
}
