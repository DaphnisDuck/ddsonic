package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// spotifyShaped returns snapshots the size of a real Spotify library:
// 2,000 saved albums of 12 tracks, 1,000 liked songs, and 20 playlists of
// 300 tracks.
func spotifyShaped() []catalog.Snapshot {
	sp := func(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Spotify, ProviderID: id} }
	artist := func(n int) []catalog.ArtistRecord {
		return []catalog.ArtistRecord{{Ref: sp(fmt.Sprintf("ar%d", n%600)), Name: fmt.Sprintf("Artist %d", n%600)}}
	}
	album := func(n int) catalog.AlbumRecord {
		return catalog.AlbumRecord{Ref: sp(fmt.Sprintf("al%d", n)), Title: fmt.Sprintf("Album %d", n), Artists: artist(n),
			Year: 1950 + n%70, TrackCount: 12, ArtworkURL: fmt.Sprintf("https://i.scdn.co/image/%d", n),
			AddedAt: time.UnixMilli(int64(1_600_000_000_000 + n))}
	}
	track := func(n int) catalog.TrackRecord {
		a := album(n / 12)
		return catalog.TrackRecord{Ref: sp(fmt.Sprintf("tr%d", n)), Title: fmt.Sprintf("Track %d", n), Artists: a.Artists,
			Album: &a, TrackNo: n%12 + 1, Duration: time.Duration(180+n%120) * time.Second,
			PlayableURI: fmt.Sprintf("spotify:track:%d", n), AddedAt: time.UnixMilli(int64(1_600_000_000_000 + n))}
	}
	albums := catalog.Snapshot{Provider: catalog.Spotify, Collection: catalog.CollectionAlbums}
	for n := range 2000 {
		albums.Albums = append(albums.Albums, album(n))
	}
	liked := catalog.Snapshot{Provider: catalog.Spotify, Collection: catalog.CollectionLiked}
	for n := range 1000 {
		liked.Tracks = append(liked.Tracks, track(n*7))
	}
	playlists := catalog.Snapshot{Provider: catalog.Spotify, Collection: catalog.CollectionPlaylists}
	for p := range 20 {
		pl := catalog.PlaylistRecord{Ref: sp(fmt.Sprintf("pl%d", p)), Name: fmt.Sprintf("Playlist %d", p), Own: true,
			Snapshot: "s1", TracksFetched: true, TrackCount: 300}
		for i := range 300 {
			pl.Tracks = append(pl.Tracks, track(p*300+i*3))
		}
		playlists.Playlists = append(playlists.Playlists, pl)
	}
	return []catalog.Snapshot{albums, liked, playlists}
}

// walFrames checkpoints the WAL and turns automatic checkpoints off, runs
// fn, and returns the WAL frames (pages) fn wrote.
func walFrames(b *testing.B, s *Store, fn func()) int {
	b.Helper()
	if _, err := s.wdb.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		b.Fatal(err)
	}
	if _, err := s.wdb.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		b.Fatal(err)
	}
	fn()
	var busy, frames, done int
	if err := s.wdb.QueryRow(`PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &done); err != nil {
		b.Fatal(err)
	}
	return frames
}

func applyAll(b *testing.B, s *Store, snaps []catalog.Snapshot) {
	b.Helper()
	for _, snap := range snaps {
		if err := s.ApplySnapshot(context.Background(), snap); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSyncWrites measures writing a Spotify-sized library: a first
// sync into an empty catalog, and a resync where nothing changed. Run with
// go test ./catalog/sqlite -run '^$' -bench SyncWrites -benchtime 3x.
func BenchmarkSyncWrites(b *testing.B) {
	snaps := spotifyShaped()
	b.Run("first", func(b *testing.B) {
		frames := 0
		for i := 0; b.Loop(); i++ {
			b.StopTimer()
			s, err := Open(context.Background(), filepath.Join(b.TempDir(), fmt.Sprintf("library%d.db", i)))
			if err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			frames = walFrames(b, s, func() { applyAll(b, s, snaps) })
			b.StopTimer()
			s.Close()
			b.StartTimer()
		}
		b.ReportMetric(float64(frames), "wal-pages")
	})
	b.Run("unchanged", func(b *testing.B) {
		s, err := Open(context.Background(), filepath.Join(b.TempDir(), "library.db"))
		if err != nil {
			b.Fatal(err)
		}
		defer s.Close()
		applyAll(b, s, snaps)
		frames := 0
		for b.Loop() {
			frames = walFrames(b, s, func() { applyAll(b, s, snaps) })
		}
		b.ReportMetric(float64(frames), "wal-pages")
	})
}
