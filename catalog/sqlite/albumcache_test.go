package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

func TestCacheAlbumTracks(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{
		{Ref: sref("al1"), Title: "Symphonies", Year: 1987, ArtworkURL: "https://img"},
	}})
	albums, err := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if err != nil || len(albums) != 1 {
		t.Fatalf("Albums() = %v, %v", albums, err)
	}
	id := albums[0].ID

	// The API's simplified tracks name no album; they join the cached one.
	err = s.CacheAlbumTracks(ctx, sref("al1"), []catalog.TrackRecord{
		{Ref: sref("t2"), Title: "II.", Disc: 1, TrackNo: 2, PlayableURI: "spotify:track:t2", Artists: []catalog.ArtistRecord{artistRec("ar1", "Ozawa")}},
		{Ref: sref("t1"), Title: "I.", Disc: 1, TrackNo: 1, PlayableURI: "spotify:track:t1", Duration: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	tracks, cached, err := s.AlbumTracks(ctx, id)
	if err != nil || !cached {
		t.Fatalf("AlbumTracks() cached=%v err=%v", cached, err)
	}
	var titles []string
	for _, tr := range tracks {
		titles = append(titles, tr.Title)
		if tr.AlbumID != id || tr.AlbumTitle != "Symphonies" || tr.Year != 1987 || tr.ArtworkURL != "https://img" {
			t.Errorf("track %s = %+v, want the album's details", tr.Title, tr)
		}
	}
	if !slices.Equal(titles, []string{"I.", "II."}) || tracks[1].Artist != "Ozawa" {
		t.Errorf("tracks = %+v", tracks)
	}
	var count int
	if err := s.db.QueryRow(`SELECT track_count FROM albums WHERE id = ?`, id).Scan(&count); err != nil || count != 2 {
		t.Errorf("track_count = %d, %v; want the cached count when Spotify gave none", count, err)
	}
	if left, err := s.UncachedAlbums(ctx, catalog.Spotify); err != nil || len(left) != 0 {
		t.Errorf("UncachedAlbums() after caching = %v, %v", left, err)
	}
}

func TestCacheAlbumTracksRejects(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.CacheAlbumTracks(ctx, sref("missing"), nil); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("unknown album: %v, want ErrNotFound", err)
	}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{{Ref: sref("al1"), Title: "A"}}})
	foreign := catalog.TrackRecord{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "x"}, Title: "x"}
	if err := s.CacheAlbumTracks(ctx, sref("al1"), []catalog.TrackRecord{foreign}); err == nil {
		t.Error("a foreign track was cached")
	}
	// The failed write left the album uncached.
	if left, _ := s.UncachedAlbums(ctx, catalog.Spotify); len(left) != 1 {
		t.Errorf("UncachedAlbums() = %v, want the album still uncached", left)
	}
}

func TestUncachedAlbumsNewestSavedFirst(t *testing.T) {
	s := openTemp(t)
	day := func(d int) time.Time { return time.Date(2024, 1, d, 0, 0, 0, 0, time.UTC) }
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{
		{Ref: sref("old"), Title: "Old", AddedAt: day(1)},
		{Ref: sref("new"), Title: "New", AddedAt: day(3)},
		{Ref: sref("mid"), Title: "Mid", AddedAt: day(2)},
	}})
	// An album known only through a liked track is not a library album.
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{
		{Ref: sref("t"), Title: "T", Album: &catalog.AlbumRecord{Ref: sref("other"), Title: "Other"}},
	}})
	got, err := s.UncachedAlbums(context.Background(), catalog.Spotify)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, a := range got {
		titles = append(titles, a.Title)
	}
	if !slices.Equal(titles, []string{"New", "Mid", "Old"}) {
		t.Errorf("UncachedAlbums() = %v", titles)
	}
}

// A cached list is the provider's whole list: a shorter or empty one
// replaces it, and liked tracks of the album do not join it. A track that
// leaves the list stays in the catalog while a collection still holds it.
func TestCacheAlbumTracksReplacesTheList(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	album := catalog.AlbumRecord{Ref: sref("al1"), Title: "Symphonies"}
	rec := func(id string, no int) catalog.TrackRecord {
		return catalog.TrackRecord{Ref: sref(id), Title: id, Disc: 1, TrackNo: no, PlayableURI: "spotify:track:" + id}
	}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{album}})
	albums, err := s.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if err != nil || len(albums) != 1 {
		t.Fatalf("Albums() = %v, %v", albums, err)
	}
	id := albums[0].ID
	list := func(want ...string) {
		t.Helper()
		tracks, cached, err := s.AlbumTracks(ctx, id)
		if got := titlesOf(tracks); err != nil || !cached || !slices.Equal(got, want) {
			t.Errorf("AlbumTracks() = %q cached=%v err=%v, want %q", got, cached, err, want)
		}
	}
	cache := func(recs ...catalog.TrackRecord) {
		t.Helper()
		if err := s.CacheAlbumTracks(ctx, sref("al1"), recs); err != nil {
			t.Fatal(err)
		}
	}

	cache(rec("a", 1), rec("b", 2))
	// b is also liked, and a liked bonus track names the album.
	liked := []catalog.TrackRecord{rec("b", 2), rec("bonus", 9)}
	for i := range liked {
		liked[i].Album = &album
	}
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: liked})
	list("a", "b")

	cache(rec("b", 2), rec("a", 1)) // reordered: still listed by track number
	list("a", "b")

	cache(rec("a", 1)) // b left the album
	list("a")
	sweep(t, s)
	likedTracks, err := s.LikedTracks(ctx, catalog.Spotify)
	got := titlesOf(likedTracks)
	slices.Sort(got)
	if err != nil || !slices.Equal(got, []string{"b", "bonus"}) {
		t.Errorf("liked after b left the album = %q, %v; want b kept for Liked", got, err)
	}

	cache() // the provider now lists no tracks
	list()
}

// Upgrading keeps every cached album's list: until v7 it was the tracks
// that named the album.
func TestAlbumTracksMigrationBackfills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	v6, err := sql.Open("sqlite", path+"?"+dsnParams)
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= "007" {
			continue
		}
		data, err := fs.ReadFile(migrationFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		old["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	if err := migrate(context.Background(), v6, old); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO albums (id, provider, provider_id, title, sort_title, tracks_cached_at, updated_at) VALUES
			(1, 'spotify', 'cached', 'Cached', 'cached', 1, 0), (2, 'spotify', 'not', 'Not Cached', 'not cached', NULL, 0)`,
		`INSERT INTO tracks (id, provider, provider_id, title, album_id, disc, track_no, playable_uri, updated_at) VALUES
			(10, 'spotify', 't2', 'Two', 1, 1, 2, 'u', 0), (11, 'spotify', 't1', 'One', 1, 1, 1, 'u', 0),
			(12, 'spotify', 't3', 'Liked', 2, 1, 1, 'u', 0)`,
	} {
		if _, err := v6.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	v6.Close()

	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tracks, cached, err := s.AlbumTracks(context.Background(), 1)
	if got := titlesOf(tracks); err != nil || !cached || !slices.Equal(got, []string{"One", "Two"}) {
		t.Errorf("cached album after upgrade = %q cached=%v err=%v", got, cached, err)
	}
	if n := count(t, s, "album_tracks"); n != 2 {
		t.Errorf("album_tracks rows = %d, want only the cached album's 2", n)
	}
}
