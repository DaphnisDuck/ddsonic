package sqlite

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// seed loads a small two-provider catalog:
//
//	spotify: artists Ozawa (followed), Muti; albums Mahler 5 (Ozawa, saved,
//	tracks cached), Roman Trilogy (Muti+Ozawa, saved, not cached), Rarity
//	(Ozawa, not saved); playlists Mine (own), Theirs (followed); one liked
//	track; one failed and one successful sync collection.
//	local: one album, so provider filtering is exercised.
func seed(t *testing.T, s *Store) {
	t.Helper()
	stmts := []string{
		`INSERT INTO artists (id, provider, provider_id, name, sort_name, updated_at) VALUES
			(1, 'spotify', 'ar-ozawa', 'Seiji Ozawa', 'ozawa, seiji', 0),
			(2, 'spotify', 'ar-muti',  'Riccardo Muti', 'muti, riccardo', 0),
			(3, 'local',   'Marriner', 'Neville Marriner', 'marriner, neville', 0)`,
		`INSERT INTO albums (id, provider, provider_id, title, sort_title, artist_credit, sort_artist, year, track_count, tracks_cached_at, updated_at) VALUES
			(10, 'spotify', 'al-m5',     'Mahler: Symphony No. 5', 'mahler: symphony no. 5', 'Seiji Ozawa',                'seiji ozawa',      1990, 2, 1, 0),
			(11, 'spotify', 'al-roman',  'Roman Trilogy',          'roman trilogy',          'Riccardo Muti, Seiji Ozawa', 'riccardo muti',    1985, 3, NULL, 0),
			(12, 'spotify', 'al-rarity', 'Rarity',                 'rarity',                 'Seiji Ozawa',                'seiji ozawa',      2001, 1, NULL, 0),
			(13, 'local',   '/m/roman',  'Roman Trilogy',          'roman trilogy',          'Neville Marriner',           'neville marriner', 1976, 1, 1, 0)`,
		`INSERT INTO album_artists (album_id, artist_id, position) VALUES
			(10, 1, 0), (11, 2, 0), (11, 1, 1), (12, 1, 0), (13, 3, 0)`,
		`INSERT INTO tracks (id, provider, provider_id, title, artist_credit, album_id, disc, track_no, duration_ms, playable_uri, updated_at) VALUES
			(100, 'spotify', 't-m5-2', 'II. Stürmisch bewegt', 'Seiji Ozawa', 10, 1, 2, 900000, 'spotify:track:t-m5-2', 0),
			(101, 'spotify', 't-m5-1', 'I. Trauermarsch',      'Seiji Ozawa', 10, 1, 1, 780000, 'spotify:track:t-m5-1', 0),
			(102, 'spotify', 't-loose', 'Loose Single',        'Riccardo Muti, Seiji Ozawa', NULL, 1, 0, 200000, 'spotify:track:t-loose', 0)`,
		`UPDATE tracks SET album_title = 'Some Single' WHERE id = 102`,
		`INSERT INTO album_tracks (album_id, track_id) VALUES (10, 100), (10, 101)`,
		`INSERT INTO track_artists (track_id, artist_id, position) VALUES (100, 1, 0), (101, 1, 0), (102, 2, 0), (102, 1, 1)`,
		`INSERT INTO playlists (id, provider, provider_id, name, own, track_count, updated_at) VALUES
			(20, 'spotify', 'pl-theirs', 'Theirs', 0, 0, 0),
			(21, 'spotify', 'pl-mine',   'Mine',   1, 2, 0)`,
		`INSERT INTO playlist_tracks (playlist_id, position, track_id) VALUES (21, 0, 102), (21, 1, 100)`,
		`INSERT INTO library_items (provider, collection, kind, item_id, added_at, last_seen_gen) VALUES
			('spotify', 'albums', 'album', 10, 1, 1), ('spotify', 'albums', 'album', 11, 1, 1),
			('spotify', 'artists', 'artist', 1, 1, 1),
			('spotify', 'playlists', 'playlist', 20, 1, 1), ('spotify', 'playlists', 'playlist', 21, 1, 1),
			('spotify', 'liked', 'track', 101, 5, 1), ('spotify', 'liked', 'track', 102, 9, 1),
			-- The same album held by a second collection is still listed once.
			('spotify', 'other', 'album', 10, 1, 1),
			('local', 'files', 'album', 13, 1, 1)`,
		`INSERT INTO sync_state (provider, collection, generation, last_attempt_at, last_success_at, last_error) VALUES
			('spotify', 'albums', 1, 2000, 2000, ''),
			('spotify', 'playlists', 1, 3000, 1000, 'http status 429')`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

func seeded(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s := openTemp(t)
	seed(t, s)
	return s, context.Background()
}

func TestAlbums(t *testing.T) {
	s, ctx := seeded(t)
	byArtist, err := s.Albums(ctx, catalog.Spotify, catalog.ByArtist)
	if err != nil {
		t.Fatal(err)
	}
	// Library albums only (not Rarity), by first artist (Muti < Ozawa).
	if len(byArtist) != 2 || byArtist[0].Title != "Roman Trilogy" || byArtist[1].Title != "Mahler: Symphony No. 5" {
		t.Fatalf("Albums(ByArtist) = %+v", byArtist)
	}
	got, err := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Mahler: Symphony No. 5" || got[1].Title != "Roman Trilogy" {
		t.Fatalf("Albums(ByTitle) = %+v", got)
	}
	mahler, roman := got[0], got[1]
	if roman.Artist != "Riccardo Muti, Seiji Ozawa" || roman.TracksCached || roman.Ref != (catalog.Ref{Provider: "spotify", ProviderID: "al-roman"}) {
		t.Errorf("Roman Trilogy = %+v", roman)
	}
	if !mahler.TracksCached || mahler.Year != 1990 || mahler.TrackCount != 2 {
		t.Errorf("Mahler 5 = %+v", mahler)
	}
	local, _ := s.Albums(ctx, catalog.Local, catalog.ByTitle)
	if len(local) != 1 || local[0].Artist != "Neville Marriner" {
		t.Errorf("local Albums() = %+v", local)
	}
}

func TestAlbumTracks(t *testing.T) {
	s, ctx := seeded(t)
	tracks, cached, err := s.AlbumTracks(ctx, 10)
	if err != nil || !cached {
		t.Fatalf("AlbumTracks(10) cached=%v err=%v", cached, err)
	}
	if len(tracks) != 2 || tracks[0].Title != "I. Trauermarsch" || tracks[1].TrackNo != 2 {
		t.Fatalf("tracks = %+v, want track order", tracks)
	}
	first := tracks[0]
	if first.Artist != "Seiji Ozawa" || first.AlbumTitle != "Mahler: Symphony No. 5" || first.AlbumID != 10 ||
		first.Duration != 13*time.Minute || first.PlayableURI != "spotify:track:t-m5-1" {
		t.Errorf("first track = %+v", first)
	}

	if tracks, cached, err := s.AlbumTracks(ctx, 11); err != nil || cached || len(tracks) != 0 {
		t.Errorf("uncached album: %d tracks, cached=%v, err=%v", len(tracks), cached, err)
	}
	if _, _, err := s.AlbumTracks(ctx, 999); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("AlbumTracks(missing) error = %v, want ErrNotFound", err)
	}
}

func TestAlbumTracksReadsOneSnapshot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{
		{Ref: sref("al1"), Title: "Symphonies"},
	}})
	albums, err := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if err != nil || len(albums) != 1 {
		t.Fatalf("Albums() = %v, %v", albums, err)
	}
	id := albums[0].ID

	// A sync caches the album between the flag read and the track read.
	t.Cleanup(func() { betweenReads = func() {} })
	betweenReads = func() {
		betweenReads = func() {}
		err := s.CacheAlbumTracks(ctx, sref("al1"), []catalog.TrackRecord{
			{Ref: sref("t1"), Title: "I.", Disc: 1, TrackNo: 1, PlayableURI: "spotify:track:t1"},
		})
		if err != nil {
			t.Errorf("CacheAlbumTracks() = %v", err)
		}
	}
	tracks, cached, err := s.AlbumTracks(ctx, id)
	if err != nil || cached || len(tracks) != 0 {
		t.Errorf("read across the sync: %d tracks, cached=%v, err=%v; want the album as it was before", len(tracks), cached, err)
	}
	tracks, cached, err = s.AlbumTracks(ctx, id)
	if err != nil || !cached || len(tracks) != 1 {
		t.Errorf("read after the sync: %d tracks, cached=%v, err=%v; want the cached track", len(tracks), cached, err)
	}
}

// Finding an album by ref and reading it happen in one snapshot: a sync
// that removes the album and gives its ID to another between the two
// cannot make the read return the other album.
func TestAlbumTracksByRefReadsOneSnapshot(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	album := func(id string) catalog.Snapshot {
		al := catalog.AlbumRecord{Ref: sref(id), Title: "Album " + id}
		return catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{al}}
	}
	apply(t, s, album("a"))
	if err := s.CacheAlbumTracks(ctx, sref("a"), []catalog.TrackRecord{{Ref: sref("ta"), Title: "Song A", PlayableURI: "u"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { betweenReads = func() {} })
	betweenReads = func() {
		betweenReads = func() {}
		apply(t, s, catalog.Snapshot{Collection: "albums"})
		sweep(t, s)
		apply(t, s, album("b"))
		if err := s.CacheAlbumTracks(ctx, sref("b"), []catalog.TrackRecord{{Ref: sref("tb"), Title: "Song B", PlayableURI: "u"}}); err != nil {
			t.Error(err)
		}
	}
	if tracks, _, err := s.AlbumTracksByRef(ctx, sref("a")); err != nil || !slices.Equal(titlesOf(tracks), []string{"Song A"}) {
		t.Errorf("read across the sync = %q, %v; want album a as it was", titlesOf(tracks), err)
	}
	if _, _, err := s.AlbumTracksByRef(ctx, sref("a")); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("after the sync = %v, want ErrNotFound", err)
	}
}

func TestAlbumTracksDuringWrite(t *testing.T) {
	s, ctx := seeded(t)
	// A sync holds the write lock; reading an album must not wait for it.
	w, err := s.wdb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	if _, err := w.ExecContext(ctx, `UPDATE albums SET title = title WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if tracks, cached, err := s.AlbumTracks(rctx, 10); err != nil || !cached || len(tracks) != 2 {
		t.Errorf("AlbumTracks(10) during a write: %d tracks, cached=%v, err=%v", len(tracks), cached, err)
	}
}

func TestArtistsAndArtistAlbums(t *testing.T) {
	s, ctx := seeded(t)
	artists, err := s.Artists(ctx, catalog.Spotify)
	if err != nil || len(artists) != 1 || artists[0].Name != "Seiji Ozawa" {
		t.Fatalf("Artists() = %+v, %v; want only the followed artist", artists, err)
	}
	albums, err := s.ArtistAlbums(ctx, artists[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, a := range albums {
		titles = append(titles, a.Title)
	}
	// Every credited album, saved or not, newest first.
	if want := []string{"Rarity", "Mahler: Symphony No. 5", "Roman Trilogy"}; !slices.Equal(titles, want) {
		t.Errorf("ArtistAlbums() = %v, want %v", titles, want)
	}
}

func TestPlaylists(t *testing.T) {
	s, ctx := seeded(t)
	pls, err := s.Playlists(ctx, catalog.Spotify)
	if err != nil || len(pls) != 2 || pls[0].Name != "Mine" || !pls[0].Own || pls[1].Own {
		t.Fatalf("Playlists() = %+v, %v; want own first", pls, err)
	}
	tracks, err := s.PlaylistTracks(ctx, pls[0].ID)
	if err != nil || len(tracks) != 2 || tracks[0].Title != "Loose Single" {
		t.Fatalf("PlaylistTracks() = %+v, %v", tracks, err)
	}
	loose := tracks[0]
	if loose.AlbumID != 0 || loose.AlbumTitle != "Some Single" || loose.Artist != "Riccardo Muti, Seiji Ozawa" {
		t.Errorf("album-less track = %+v", loose)
	}
}

func TestLikedTracks(t *testing.T) {
	s, ctx := seeded(t)
	liked, err := s.LikedTracks(ctx, catalog.Spotify)
	if err != nil || len(liked) != 2 || liked[0].Title != "Loose Single" {
		t.Fatalf("LikedTracks() = %+v, %v; want most recently liked first", liked, err)
	}
}

func TestSyncStatus(t *testing.T) {
	s, ctx := seeded(t)
	st, err := s.SyncStatus(ctx, catalog.Spotify)
	if err != nil || len(st) != 2 {
		t.Fatalf("SyncStatus() = %+v, %v", st, err)
	}
	albums, pls := st[0], st[1]
	if albums.Collection != "albums" || albums.LastError != "" || !albums.LastSuccess.Equal(time.UnixMilli(2000)) {
		t.Errorf("albums status = %+v", albums)
	}
	if !strings.Contains(pls.LastError, "429") || !pls.LastAttempt.After(pls.LastSuccess) {
		t.Errorf("playlists status = %+v, want a failed attempt after the last success", pls)
	}
	if none, _ := s.SyncStatus(ctx, catalog.Local); len(none) != 0 {
		t.Errorf("local SyncStatus() = %+v, want none", none)
	}
}

func TestAlbum(t *testing.T) {
	s, ctx := seeded(t)
	albums, err := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if err != nil || len(albums) == 0 {
		t.Fatalf("Albums() = %v, %v", albums, err)
	}
	got, err := s.Album(ctx, albums[0].ID)
	if err != nil || got != albums[0] {
		t.Errorf("Album(%d) = %+v, %v; want %+v", albums[0].ID, got, err, albums[0])
	}
	if _, err := s.Album(ctx, -1); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("Album(-1) = %v, want ErrNotFound", err)
	}
}

// An empty provider lists every provider's albums and artists together.
func TestAlbumsAndArtistsAcrossProviders(t *testing.T) {
	s, ctx := seeded(t)
	albums, err := s.Albums(ctx, "", catalog.ByTitle)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range albums {
		got = append(got, a.Ref.Provider+":"+a.Title)
	}
	spotify, _ := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	local, _ := s.Albums(ctx, catalog.Local, catalog.ByTitle)
	if len(albums) != len(spotify)+len(local) || len(local) == 0 {
		t.Errorf("all albums = %v, want Spotify's %d and Local's %d", got, len(spotify), len(local))
	}
	if !slices.IsSortedFunc(albums, func(a, b catalog.Album) int {
		return strings.Compare(catalog.SortKey(a.Title), catalog.SortKey(b.Title))
	}) {
		t.Errorf("all albums not by title: %v", got)
	}
	artists, err := s.Artists(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	sa, _ := s.Artists(ctx, catalog.Spotify)
	la, _ := s.Artists(ctx, catalog.Local)
	if len(artists) != len(sa)+len(la) {
		t.Errorf("all artists = %d, want %d + %d", len(artists), len(sa), len(la))
	}
}
