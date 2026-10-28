package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// libraryTracks selects a provider's tracks in the library: its liked
// tracks and those of its library playlists. Its two arguments are the
// provider.
const libraryTracks = `SELECT item_id FROM library_items WHERE provider = ? AND kind = 'track'
	UNION SELECT pt.track_id FROM playlist_tracks pt
		JOIN library_items li ON li.item_id = pt.playlist_id AND li.kind = 'playlist' AND li.provider = ?`

// UnenrichedTracks returns up to limit of provider's library tracks not yet
// read for enrichment, newest first.
func (s *Store) UnenrichedTracks(ctx context.Context, provider string, limit int) ([]catalog.Track, error) {
	return queryAll(ctx, s.db, scanTrack, `SELECT `+trackColumns+`
		FROM tracks t LEFT JOIN albums al ON al.id = t.album_id
		WHERE t.provider = ? AND t.enriched_at IS NULL AND t.id IN (`+libraryTracks+`)
		ORDER BY t.id DESC LIMIT ?`, provider, provider, provider, limit)
}

// EnrichTrack records an enrichment read of the track: its title, credits,
// album and year when the read found them, and in any case that it was
// read, so it is not read again.
func (s *Store) EnrichTrack(ctx context.Context, track catalog.Ref, meta catalog.TrackMetadata) error {
	tx, err := s.wdb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("enrich %s: begin: %w", track.ProviderID, err)
	}
	defer tx.Rollback()
	w := newWriter(ctx, tx, track.Provider)
	if err := w.enrich(track, meta); err != nil {
		return fmt.Errorf("enrich %s: %w", track.ProviderID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("enrich %s: commit: %w", track.ProviderID, err)
	}
	return nil
}

func (w *snapWriter) enrich(track catalog.Ref, meta catalog.TrackMetadata) error {
	ref, err := w.ref(track)
	if err != nil {
		return err
	}
	var id int64
	err = w.tx.QueryRowContext(w.ctx, `SELECT id FROM tracks WHERE provider = ? AND provider_id = ?`,
		ref.Provider, ref.ProviderID).Scan(&id)
	if err == sql.ErrNoRows {
		return catalog.ErrNotFound
	}
	if err != nil {
		return err
	}
	if meta.Found() {
		albumID := sql.NullInt64{}
		if meta.Album != nil {
			aid, err := w.album(*meta.Album)
			if err != nil {
				return err
			}
			albumID = sql.NullInt64{Int64: aid, Valid: true}
		}
		artistIDs, credit, err := w.credit(meta.Artists)
		if err != nil {
			return err
		}
		if err := w.exec(`UPDATE tracks SET
				title = COALESCE(NULLIF(?, ''), title),
				artist_credit = COALESCE(NULLIF(?, ''), artist_credit),
				album_id = COALESCE(?, album_id),
				year = COALESCE(NULLIF(?, 0), year)
			WHERE id = ?`, meta.Title, credit, albumID, meta.Year, id); err != nil {
			return err
		}
		if len(artistIDs) > 0 {
			if err := w.replaceCredits("track_artists", "track_id", id, artistIDs); err != nil {
				return err
			}
		}
	}
	return w.exec(`UPDATE tracks SET enriched_at = ? WHERE id = ?`, w.now, id)
}

// RecordEnrichFailure counts a run in which the track could not be read.
// At giveUpAfter counted runs it marks the track read, as a track that
// cannot be read at all is, and reports that it did.
func (s *Store) RecordEnrichFailure(ctx context.Context, track catalog.Ref, giveUpAfter int) (gaveUp bool, err error) {
	err = s.wdb.QueryRowContext(ctx, `UPDATE tracks SET enrich_failures = enrich_failures + 1,
			enriched_at = CASE WHEN enrich_failures + 1 >= ? THEN ? ELSE enriched_at END
		WHERE provider = ? AND provider_id = ?
		RETURNING enriched_at IS NOT NULL`,
		giveUpAfter, time.Now().UnixMilli(), track.Provider, track.ProviderID).Scan(&gaveUp)
	if errors.Is(err, sql.ErrNoRows) {
		err = catalog.ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("record enrich failure %s: %w", track.ProviderID, err)
	}
	return gaveUp, nil
}

// RefreshDerived makes provider's derived albums and artists the albums and
// credited artists of its enriched library tracks, so its Albums and
// Artists lists follow enrichment and the library.
func (s *Store) RefreshDerived(ctx context.Context, provider string) error {
	tx, err := s.wdb.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("refresh derived %s: begin: %w", provider, err)
	}
	defer tx.Rollback()
	enriched := `SELECT id FROM tracks WHERE provider = ? AND enriched_at IS NOT NULL AND id IN (` + libraryTracks + `)`
	now := time.Now().UnixMilli()
	stmts := []struct {
		query string
		args  []any
	}{
		{`DELETE FROM library_items WHERE provider = ? AND collection = ?`, []any{provider, catalog.CollectionDerived}},
		{`INSERT OR IGNORE INTO library_items (provider, collection, kind, item_id, added_at, last_seen_gen)
			SELECT DISTINCT ?, ?, ?, album_id, ?, 0 FROM tracks
			WHERE album_id IS NOT NULL AND id IN (` + enriched + `)`,
			[]any{provider, catalog.CollectionDerived, catalog.KindAlbum, now, provider, provider, provider}},
		{`INSERT OR IGNORE INTO library_items (provider, collection, kind, item_id, added_at, last_seen_gen)
			SELECT DISTINCT ?, ?, ?, artist_id, ?, 0 FROM track_artists
			WHERE track_id IN (` + enriched + `)`,
			[]any{provider, catalog.CollectionDerived, catalog.KindArtist, now, provider, provider, provider}},
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.query, st.args...); err != nil {
			return fmt.Errorf("refresh derived %s: %w", provider, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("refresh derived %s: commit: %w", provider, err)
	}
	return nil
}
