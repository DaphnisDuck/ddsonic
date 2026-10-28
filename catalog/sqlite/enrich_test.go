package sqlite

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
)

func yref(id string) catalog.Ref { return catalog.Ref{Provider: catalog.YouTube, ProviderID: id} }

// video is a track as the YouTube sync knows it: a video title and channel.
func video(id, title, channel string) catalog.TrackRecord {
	return catalog.TrackRecord{Ref: yref(id), Title: title, PlayableURI: "https://music.youtube.com/watch?v=" + id,
		Artists: []catalog.ArtistRecord{{Ref: yref("channel:" + channel), Name: channel}}}
}

func youtubeLibrary(t *testing.T) *Store {
	t.Helper()
	s := openTemp(t)
	apply(t, s, catalog.Snapshot{Provider: catalog.YouTube, Collection: "liked", Tracks: []catalog.TrackRecord{
		video("v1", "Beginnings (2002 Remaster)", "Chicago Band"),
		video("v2", "The Beach Boys - Kokomo [Official Music Video]", "PLAYaLOT"),
	}})
	apply(t, s, catalog.Snapshot{Provider: catalog.YouTube, Collection: "playlists", Playlists: []catalog.PlaylistRecord{
		{Ref: yref("PL1"), Name: "Mix", TracksFetched: true, Tracks: []catalog.TrackRecord{video("v3", "Libertango", "Astor Piazzolla Oficial")}},
	}})
	return s
}

func titlesOf(tracks []catalog.Track) []string {
	var out []string
	for _, t := range tracks {
		out = append(out, t.Title)
	}
	return out
}

func TestEnrichTrack(t *testing.T) {
	s := youtubeLibrary(t)
	ctx := context.Background()
	todo, err := s.UnenrichedTracks(ctx, catalog.YouTube, 10)
	if err != nil || len(todo) != 3 {
		t.Fatalf("unenriched = %q, %v; want the liked and playlist tracks", titlesOf(todo), err)
	}
	chicago := []catalog.ArtistRecord{{Ref: yref("artist:chicago"), Name: "Chicago"}}
	meta := catalog.TrackMetadata{Title: "Beginnings", Artists: chicago, Year: 1969,
		Album: &catalog.AlbumRecord{Ref: yref("album:chicago/best"), Title: "The Very Best of Chicago", Artists: chicago, Year: 1969}}
	if err := s.EnrichTrack(ctx, yref("v1"), meta); err != nil {
		t.Fatal(err)
	}
	// A read that found nothing only marks the track.
	if err := s.EnrichTrack(ctx, yref("v2"), catalog.TrackMetadata{}); err != nil {
		t.Fatal(err)
	}
	todo, _ = s.UnenrichedTracks(ctx, catalog.YouTube, 10)
	if !slices.Equal(titlesOf(todo), []string{"Libertango"}) {
		t.Errorf("unenriched after = %q", titlesOf(todo))
	}
	// A later sync of the raw videos keeps what enrichment found.
	apply(t, s, catalog.Snapshot{Provider: catalog.YouTube, Collection: "liked", Tracks: []catalog.TrackRecord{
		video("v1", "Beginnings (2002 Remaster)", "Chicago Band"),
		video("v2", "The Beach Boys - Kokomo [Official Music Video]", "PLAYaLOT"),
	}})
	liked, _ := s.LikedTracks(ctx, catalog.YouTube)
	byTitle := map[string]catalog.Track{}
	for _, tr := range liked {
		byTitle[tr.Title] = tr
	}
	b, ok := byTitle["Beginnings"]
	if !ok || b.Artist != "Chicago" || b.AlbumTitle != "The Very Best of Chicago" || b.Year != 1969 {
		t.Errorf("enriched track after sync = %+v", liked)
	}
	if k := byTitle["The Beach Boys - Kokomo [Official Music Video]"]; k.Artist != "PLAYaLOT" {
		t.Errorf("unfound track = %+v, want it unchanged", k)
	}
	if err := s.EnrichTrack(ctx, yref("missing"), meta); err == nil {
		t.Error("enriched a track the catalog does not have")
	}
}

// Derived albums and artists follow enriched library tracks.
func TestRefreshDerived(t *testing.T) {
	s := youtubeLibrary(t)
	ctx := context.Background()
	piazzolla := []catalog.ArtistRecord{{Ref: yref("artist:astor piazzolla"), Name: "Astor Piazzolla"}}
	if err := s.EnrichTrack(ctx, yref("v3"), catalog.TrackMetadata{Title: "Libertango", Artists: piazzolla,
		Album: &catalog.AlbumRecord{Ref: yref("album:piazzolla/soul"), Title: "The Soul of Tango", Artists: piazzolla, Year: 2000}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RefreshDerived(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	albums, _ := s.Albums(ctx, catalog.YouTube, catalog.ByTitle)
	artists, _ := s.Artists(ctx, catalog.YouTube)
	if len(albums) != 1 || albums[0].Title != "The Soul of Tango" || len(artists) != 1 || artists[0].Name != "Astor Piazzolla" {
		t.Fatalf("albums %+v, artists %+v; want only the enriched track's", albums, artists)
	}
	if tracks, _, _ := s.AlbumTracks(ctx, albums[0].ID); len(tracks) != 1 || tracks[0].Title != "Libertango" {
		t.Errorf("album tracks = %+v", tracks)
	}
	// The track leaves the library (its playlist is emptied): the album and
	// artist leave the lists.
	apply(t, s, catalog.Snapshot{Provider: catalog.YouTube, Collection: "playlists", Playlists: []catalog.PlaylistRecord{
		{Ref: yref("PL1"), Name: "Mix", TracksFetched: true},
	}})
	if err := s.RefreshDerived(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	if albums, _ := s.Albums(ctx, catalog.YouTube, catalog.ByTitle); len(albums) != 0 {
		t.Errorf("albums after removal = %+v", albums)
	}
}

func TestRecordEnrichFailureGivesUpAfterRuns(t *testing.T) {
	s := youtubeLibrary(t)
	ctx := context.Background()
	for run := 1; run <= 3; run++ {
		gaveUp, err := s.RecordEnrichFailure(ctx, yref("v3"), 3)
		if err != nil || gaveUp != (run == 3) {
			t.Fatalf("run %d: gaveUp %v, %v", run, gaveUp, err)
		}
		todo, _ := s.UnenrichedTracks(ctx, catalog.YouTube, 10)
		if slices.Contains(titlesOf(todo), "Libertango") == (run == 3) {
			t.Errorf("run %d: unenriched = %q", run, titlesOf(todo))
		}
	}
	if _, err := s.RecordEnrichFailure(ctx, yref("gone"), 3); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("unknown track: %v, want ErrNotFound", err)
	}
}
