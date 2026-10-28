package library

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
)

// An entry kept from an earlier listing must not open whatever entity has
// its ID now: SQLite gives a removed row's ID to the next one inserted.
func TestRetainedEntriesSurviveIDReuse(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	snapshot := func(album, artist, track string) catalog.Snapshot {
		snap := catalog.Snapshot{Provider: catalog.Local, Collection: "files"}
		if album != "" {
			ar := catalog.ArtistRecord{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: artist}, Name: artist}
			al := catalog.AlbumRecord{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/" + album}, Title: album,
				Artists: []catalog.ArtistRecord{ar}}
			snap.Albums = []catalog.AlbumRecord{al}
			snap.Artists = []catalog.ArtistRecord{ar}
			snap.Tracks = []catalog.TrackRecord{{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/" + track},
				Title: track, Album: &al, Artists: []catalog.ArtistRecord{ar}, PlayableURI: "/m/" + track}}
		}
		return snap
	}
	write := func(snap catalog.Snapshot) {
		t.Helper()
		if err := store.ApplySnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
		if err := store.Sweep(ctx, catalog.Local); err != nil {
			t.Fatal(err)
		}
	}
	retain := func(menu string) Entry {
		t.Helper()
		root, err := LocalCatalog(store, nil, "/m").Load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range root {
			if e.Title == menu {
				entries, err := e.Open.Load(ctx)
				if err != nil || len(entries) != 1 {
					t.Fatalf("%s = %+v, %v", menu, entries, err)
				}
				return entries[0]
			}
		}
		t.Fatalf("no %s menu", menu)
		return Entry{}
	}

	write(snapshot("Album A", "Artist A", "Song A"))
	album, artist := retain("Albums"), retain("Artists")

	// A sync removes A; the next one brings B, which takes A's old IDs.
	write(snapshot("", "", ""))
	write(snapshot("Album B", "Artist B", "Song B"))
	if again := retain("Albums"); again.ID != album.ID {
		t.Fatalf("B got ID %s, A had %s: SQLite did not reuse the ID, so this test proves nothing", again.ID, album.ID)
	}

	for _, e := range []Entry{album, artist} {
		got, err := e.Open.Load(ctx)
		if !errors.Is(err, catalog.ErrNotFound) {
			var titles []string
			for _, g := range got {
				titles = append(titles, g.Title)
			}
			t.Errorf("retained %q opened %q, %v; want ErrNotFound, not B", e.Title, titles, err)
		}
	}

	// An entity that comes back under a new ID still opens.
	write(snapshot("Album A", "Artist A", "Song A"))
	if got, err := album.Open.Load(ctx); err != nil || len(got) != 1 || !strings.HasSuffix(got[0].Title, "Song A") {
		t.Errorf("A back under a new ID opened %+v, %v", got, err)
	}
}

// Results of a source that is not set up (left in the catalog by an earlier
// configuration) neither show nor take the places of results that would.
func TestSearchSkipsUnconfiguredSourcesBeforeRanking(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	spotify := catalog.Snapshot{Provider: catalog.Spotify, Collection: "albums"}
	for i := range 25 {
		spotify.Albums = append(spotify.Albums, catalog.AlbumRecord{
			Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: fmt.Sprint("al", i)}, Title: "Planets"})
	}
	liked := catalog.Snapshot{Provider: catalog.Spotify, Collection: "liked", Tracks: []catalog.TrackRecord{{
		Ref: catalog.Ref{Provider: catalog.Spotify, ProviderID: "t1"}, Title: "Planets", PlayableURI: "spotify:track:t1"}}}
	local := catalog.AlbumRecord{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/holst"}, Title: "The Planets Suite"}
	files := catalog.Snapshot{Provider: catalog.Local, Collection: "files", Albums: []catalog.AlbumRecord{local}}
	for _, snap := range []catalog.Snapshot{spotify, liked, files} {
		if err := store.ApplySnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}
	view := newCatalogView(store, Sources{Catalog: store, MusicDir: "/m"}) // no Spotify

	titles := func(query string) []string {
		t.Helper()
		entries, err := view.results(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			if e.Section != beyondSection {
				out = append(out, e.Section+": "+e.Title)
			}
		}
		return out
	}
	if got := titles("planets"); !slices.Equal(got, []string{"Albums: The Planets Suite"}) {
		t.Errorf("search = %q, want only the local album, no Spotify album or track", got)
	}
	if got := titles("source:spotify planets"); !slices.Equal(got, []string{": " + noMatches}) {
		t.Errorf("source:spotify = %q, want no matches", got)
	}
}

// A searched track kept from an earlier search plays itself, never the
// track that took its ID, even when its album's ID was reused too.
func TestRetainedSearchTrackSurvivesIDReuse(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	write := func(name string) {
		t.Helper()
		snap := catalog.Snapshot{Provider: catalog.Local, Collection: "files"}
		if name != "" {
			al := catalog.AlbumRecord{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/" + name}, Title: "Album " + name}
			snap.Albums = []catalog.AlbumRecord{al}
			snap.Tracks = []catalog.TrackRecord{{Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/" + name + ".flac"},
				Title: "Song " + name, Album: &al, PlayableURI: "/m/" + name + ".flac"}}
		}
		if err := store.ApplySnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
		if err := store.Sweep(ctx, catalog.Local); err != nil {
			t.Fatal(err)
		}
	}
	view := newCatalogView(store, Sources{Catalog: store, MusicDir: "/m"})
	write("A")
	entries, err := view.results(ctx, "song")
	if err != nil {
		t.Fatal(err)
	}
	var song Entry
	for _, e := range entries {
		if e.Track != nil {
			song = e
		}
	}
	if song.PlayFrom == nil {
		t.Fatalf("no track row in %+v", entries)
	}
	write("")
	write("B") // takes A's album and track IDs
	tracks, at, err := song.PlayFrom(ctx)
	if err != nil || len(tracks) != 1 || tracks[at].Path != "/m/A.flac" {
		var paths []string
		for _, tr := range tracks {
			paths = append(paths, tr.Path)
		}
		t.Errorf("retained Song A plays %q from %d, %v; want only /m/A.flac", paths, at, err)
	}
}
