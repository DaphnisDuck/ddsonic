package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/bjarneo/cliamp/catalog"
)

// Search ranking. bm25 is negative, more negative for better matches, and
// scales with the corpus, so boosts multiply it rather than add to it.
const (
	memberBoost = 0.5 // saved albums, followed artists, liked and local tracks
	exactBoost  = 1.0 // the title is exactly the query
	// candidates is how many of a kind's best text matches are boosted and
	// re-ranked. Ranking every match of a one-letter query costs hundreds
	// of milliseconds on a 100k-track catalog; the boosts only reorder close
	// matches, which the pool holds.
	candidates = 200
)

// searchKind is how one kind is searched. Its query runs in two phases: the
// FTS table picks the best text matches (filtered by provider inside the
// scan), then those are joined to their rows, boosted and re-ranked.
type searchKind struct {
	table string // its FTS table, ranked by bm25 with weights set in 002_search.sql
	// cols maps a query field to its column; a kind lacking a field a term
	// names cannot match that query.
	cols map[string]string
	// where limits the FTS scan beyond the provider filter.
	where string
	// selects reads the kind's columns and a score from hits (id, rank),
	// joined to the kind's table; ?2 is the exact-title text.
	selects string
	// favorite, when set, is SQL ranked before the score (stations).
	favorite string
	scan     func(*sql.Rows) (catalog.SearchResult, error)
}

var searchKinds = map[catalog.SearchKind]searchKind{
	catalog.SearchArtist: {
		table: "artists_fts",
		cols:  map[string]string{catalog.FieldTitle: "name", catalog.FieldArtist: "name"},
		selects: `SELECT ar.id, ar.provider, ar.provider_id, ar.name, ar.image_url,
				hits.rank * (1 + ` + boost("ar.id", catalog.KindArtist) + ` + ` + exact("ar.name") + `) AS score
			FROM hits JOIN artists ar ON ar.id = hits.id`,
		scan: func(r *sql.Rows) (res catalog.SearchResult, err error) {
			a := &catalog.Artist{}
			err = r.Scan(&a.ID, &a.Ref.Provider, &a.Ref.ProviderID, &a.Name, &a.ImageURL, &res.Score)
			res.Artist = a
			return res, err
		},
	},
	catalog.SearchAlbum: {
		table: "albums_fts",
		cols: map[string]string{catalog.FieldTitle: "title", catalog.FieldAlbum: "title",
			catalog.FieldArtist: "artist"},
		selects: `SELECT ` + albumColumns + `,
				hits.rank * (1 + ` + boost("al.id", catalog.KindAlbum) + ` + ` + exact("al.title") + `) AS score
			FROM hits JOIN albums al ON al.id = hits.id`,
		scan: func(r *sql.Rows) (res catalog.SearchResult, err error) {
			a, err := scanAlbumWith(r, &res.Score)
			res.Album = &a
			return res, err
		},
	},
	catalog.SearchTrack:   trackKind(),
	catalog.SearchStation: stationKind(),
	catalog.SearchPlaylist: {
		table: "playlists_fts",
		cols:  map[string]string{catalog.FieldTitle: "name"},
		selects: `SELECT p.id, p.provider, p.provider_id, p.name, p.own, p.track_count,
				hits.rank * (1 + ` + exact("p.name") + `) AS score
			FROM hits JOIN playlists p ON p.id = hits.id`,
		scan: func(r *sql.Rows) (res catalog.SearchResult, err error) {
			p := &catalog.Playlist{}
			err = r.Scan(&p.ID, &p.Ref.Provider, &p.Ref.ProviderID, &p.Name, &p.Own, &p.TrackCount, &res.Score)
			res.Playlist = p
			return res, err
		},
	},
}

// trackKind searches ordinary tracks, boosting library members.
func trackKind() searchKind {
	return searchKind{
		table:   "tracks_fts",
		cols:    trackCols,
		where:   `provider <> '` + catalog.Radio + `'`,
		selects: trackSelect(`hits.rank * (1 + ` + boost("t.id", catalog.KindTrack) + ` + ` + exact("t.title") + `)`),
		scan:    scanTrackResult,
	}
}

// stationKind searches radio stations, which rank favorites first.
func stationKind() searchKind {
	return searchKind{
		table:   "tracks_fts",
		cols:    trackCols,
		where:   `provider = '` + catalog.Radio + `'`,
		selects: trackSelect(`hits.rank * (1 + ` + exact("t.title") + `)`),
		favorite: `EXISTS (SELECT 1 FROM library_items li WHERE li.kind = '` + string(catalog.KindTrack) + `'
			AND li.item_id = t.id AND li.provider = '` + catalog.Radio + `' AND li.collection = 'favorites')`,
		scan: scanTrackResult,
	}
}

// trackCols maps query fields to tracks_fts columns.
var trackCols = map[string]string{catalog.FieldTitle: "title", catalog.FieldArtist: "artist",
	catalog.FieldAlbum: "album", catalog.FieldGenre: "genre"}

// trackSelect reads the hits' tracks with score as their score.
func trackSelect(score string) string {
	return `SELECT ` + trackColumns + `, ` + score + ` AS score
			FROM hits JOIN tracks t ON t.id = hits.id
			LEFT JOIN albums al ON al.id = t.album_id`
}

func scanTrackResult(r *sql.Rows) (res catalog.SearchResult, err error) {
	t, err := scanTrackWith(r, &res.Score)
	res.Track = &t
	return res, err
}

// boost is the membership boost of an entity of kind, as SQL.
func boost(id string, kind catalog.Kind) string {
	return fmt.Sprintf("%v * EXISTS (SELECT 1 FROM library_items li WHERE li.kind = '%s' AND li.item_id = %s)",
		memberBoost, kind, id)
}

// exact is the exact-title boost as SQL, comparing column to the
// query's plain text (?2).
func exact(column string) string {
	return fmt.Sprintf("%v * (lower(%s) = lower(?2))", exactBoost, column)
}

// Search implements catalog.Catalog. Each kind is searched on its own
// connection, at the same time, so a query waits for the slowest kind
// rather than for all of them in turn.
func (s *Store) Search(ctx context.Context, q catalog.Query, limit int) (catalog.SearchResults, error) {
	out := catalog.SearchResults{}
	if q.Empty() || limit <= 0 {
		return out, nil
	}
	type found struct {
		kind    catalog.SearchKind
		results []catalog.SearchResult
		err     error
	}
	ch := make(chan found)
	n := 0
	for _, kind := range catalog.SearchKinds {
		if !q.Wants(kind) {
			continue
		}
		n++
		go func() {
			results, err := s.searchKind(ctx, kind, q, limit)
			ch <- found{kind, results, err}
		}()
	}
	var errs []error
	for range n {
		f := <-ch
		if f.err != nil {
			errs = append(errs, fmt.Errorf("search %ss: %w", f.kind, f.err))
		} else if len(f.results) > 0 {
			out[f.kind] = f.results
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func (s *Store) searchKind(ctx context.Context, kind catalog.SearchKind, q catalog.Query, limit int) ([]catalog.SearchResult, error) {
	spec := searchKinds[kind]
	match, ok := ftsMatch(q.Terms, spec.cols)
	if !ok {
		return nil, nil
	}
	// Numbered parameters: ?1 the match, ?2 the plain text, then providers,
	// the candidate pool and the limit.
	args := []any{match, exactTitleText(q)}
	where := ""
	if spec.where != "" {
		where = " AND " + spec.where
	}
	if len(q.Providers) > 0 {
		marks := make([]string, len(q.Providers))
		for i, p := range q.Providers {
			args = append(args, p)
			marks[i] = fmt.Sprintf("?%d", len(args))
		}
		where += " AND provider IN (" + strings.Join(marks, ", ") + ")"
	}
	args = append(args, max(candidates, limit))
	pool := len(args)
	args = append(args, limit)
	order := "score"
	if spec.favorite != "" {
		order = spec.favorite + " DESC, score"
	}
	query := fmt.Sprintf(`WITH hits AS (
			SELECT rowid AS id, rank FROM %[1]s
			WHERE %[1]s MATCH ?1%[2]s
			ORDER BY rank LIMIT ?%[3]d)
		%[4]s
		ORDER BY %[5]s LIMIT ?%[6]d`, spec.table, where, pool, spec.selects, order, len(args))
	results, err := queryAll(ctx, s.db, spec.scan, query, args...)
	for i := range results {
		results[i].Kind = kind
		results[i].Provider = resultProvider(results[i])
	}
	return results, err
}

func resultProvider(r catalog.SearchResult) string {
	switch {
	case r.Artist != nil:
		return r.Artist.Ref.Provider
	case r.Album != nil:
		return r.Album.Ref.Provider
	case r.Track != nil:
		return r.Track.Ref.Provider
	case r.Playlist != nil:
		return r.Playlist.Ref.Provider
	}
	return ""
}

// exactTitleText is what a title must equal to earn the exact-title boost:
// the query's words, when none is limited to a field.
func exactTitleText(q catalog.Query) string {
	words := make([]string, len(q.Terms))
	for i, t := range q.Terms {
		if t.Field != catalog.FieldAny {
			return ""
		}
		words[i] = t.Text
	}
	return strings.Join(words, " ")
}

// wordRunes counts the letters and digits in s: what the tokenizer keeps.
func wordRunes(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n++
		}
	}
	return n
}

// ftsMatch builds an FTS5 MATCH expression requiring every term, each
// quoted so no user text is read as FTS syntax, and each a prefix query so
// results follow typing. A single character matches only as a whole word:
// as a prefix it matches most of a library, which is slow to rank and no
// use to read. ok is false when a term names a field the table has no
// column for.
func ftsMatch(terms []catalog.Term, cols map[string]string) (string, bool) {
	parts := make([]string, len(terms))
	for i, t := range terms {
		expr := `"` + strings.ReplaceAll(t.Text, `"`, `""`) + `"`
		if wordRunes(t.Text) > 1 {
			expr += " *"
		}
		if t.Field != catalog.FieldAny {
			col, ok := cols[t.Field]
			if !ok {
				return "", false
			}
			expr = col + " : " + expr
		}
		parts[i] = expr
	}
	return strings.Join(parts, " AND "), true
}
