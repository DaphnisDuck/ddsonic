package library

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
)

func TestLibraryMenuMergesSources(t *testing.T) {
	cat, live, _ := newCatalogFixture()
	local := catalog.Album{ID: 40, Ref: lref3(catalog.Local, "/m/c"), Title: "Cached", Artist: "Holst", Year: 1990}
	cat.albums = append(cat.albums, local)
	cat.albumTracks[40] = []catalog.Track{{ID: 41, Title: "Local track", AlbumID: 40, PlayableURI: "/m/c/1.flac"}}
	cat.artists = append(cat.artists, catalog.Artist{ID: 7, Ref: lref3(catalog.Local, "holst"), Name: "Holst"})
	root := Root(Sources{Spotify: live, Local: &fakeProvider{name: "Local"}, MusicDir: "/m", Catalog: cat, Synced: spotifySynced(live)})
	if got := titles(load(t, root)); len(got) == 0 || got[0] != "All Music" {
		t.Fatalf("Music = %v, want All Music first", got)
	}
	lib := child(t, root, "All Music")
	if got := titles(load(t, lib)); !slices.Equal(got, []string{"Albums", "Artists"}) {
		t.Fatalf("Library = %v", got)
	}

	albums := child(t, lib, "Albums")
	if cl, ok := albums.(CatalogLevel); !ok || cl.CatalogProvider() != "" {
		t.Error("Library Albums is not an every-source catalog level")
	}
	if _, ok := albums.(OrderedLevel); !ok {
		t.Error("Library Albums cannot be reordered")
	}
	rows := load(t, albums)
	type row struct{ title, detail string }
	var got []row
	for _, e := range rows {
		got = append(got, row{e.Title, e.Detail})
	}
	// The same title in both sources: two rows, side by side (then by
	// artist), labelled.
	want := []row{{"Cached", "Local · Holst · 1990"}, {"Cached", "Spotify · Ozawa"}, {"Not Cached", "Spotify · Muti"}}
	if !slices.Equal(got, want) {
		t.Fatalf("Library albums = %v, want %v", got, want)
	}
	// Each opens its own source's level.
	if tracks := load(t, rows[0].Open); len(tracks) != 1 || tracks[0].Track.Path != "/m/c/1.flac" {
		t.Errorf("local album = %+v", tracks)
	}
	if tracks := load(t, rows[1].Open); len(tracks) != 1 || tracks[0].Track.Path != "spotify:track:t10" {
		t.Errorf("Spotify album = %+v", tracks)
	}

	artists := load(t, child(t, lib, "Artists"))
	var names []string
	for _, e := range artists {
		names = append(names, e.Title+" · "+e.Detail)
	}
	if !slices.Equal(names, []string{"Ozawa · Spotify", "Holst · Local"}) {
		t.Errorf("Library artists = %v", names)
	}
}

func TestLibraryMenuEmpty(t *testing.T) {
	cat, live, _ := newCatalogFixture()
	cat.albums, cat.artists = nil, nil
	lib := child(t, Root(Sources{Spotify: live, Catalog: cat, Synced: spotifySynced(live)}), "All Music")
	for _, name := range []string{"Albums", "Artists"} {
		if got := titles(load(t, child(t, lib, name))); !slices.Equal(got, []string{emptyLibrary}) {
			t.Errorf("empty %s = %v", name, got)
		}
	}
	if got := titles(load(t, Root(Sources{Spotify: live}))); slices.Contains(got, "All Music") {
		t.Errorf("Music without a catalog = %v, want no All Music", got)
	}
}
