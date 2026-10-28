package library

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

func lref(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Local, ProviderID: id} }

func newLocalFixture() (*fakeCatalog, Level) {
	mahler := catalog.Album{ID: 1, Ref: lref("mahler"), Title: "Mahler 5", Artist: "Ozawa", Year: 1990}
	mix := catalog.Album{ID: 2, Ref: lref("mix"), Title: "Mix", Artist: "Various Artists"}
	cat := &fakeCatalog{
		synced: true,
		albums: []catalog.Album{mahler, mix},
		albumTracks: map[int64][]catalog.Track{
			1: {{ID: 10, Title: "I", PlayableURI: "/m/Mahler/5/CD1/01.flac"}, {ID: 11, Title: "IV", PlayableURI: "/m/Mahler/5/CD2/01.flac"}},
		},
		cached:     map[int64]bool{}, // local albums are never "cached"; they are indexed
		artists:    []catalog.Artist{{ID: 5, Name: "Ozawa"}, {ID: 6, Name: "Ravel"}},
		artistAlbs: map[int64][]catalog.Album{5: {mahler}, 6: {mix}},
		genres:     []catalog.Genre{{Name: "Classical", AlbumCount: 2}},
		genreAlbs:  map[string][]catalog.Album{"Classical": {mahler, mix}},
	}
	return cat, LocalCatalog(cat, &fakeProvider{name: "Local"}, "/m")
}

func TestLocalCatalogMenu(t *testing.T) {
	_, root := newLocalFixture()
	if got := titles(load(t, root)); !slices.Equal(got, []string{"Albums", "Artists", "Genres", "Folders", "Playlists"}) {
		t.Errorf("local menu = %v", got)
	}
	for _, name := range []string{"Albums", "Artists", "Genres"} {
		if cl, ok := child(t, root, name).(CatalogLevel); !ok || cl.CatalogProvider() != catalog.Local {
			t.Errorf("%s is not a local catalog level", name)
		}
	}
	if got := titles(load(t, local(&fakeProvider{name: "Local"}))); !slices.Equal(got, []string{"Folders", "Playlists"}) {
		t.Errorf("menu without a catalog = %v", got)
	}
}

func TestLocalCatalogBrowse(t *testing.T) {
	_, root := newLocalFixture()
	albums := load(t, child(t, root, "Albums"))
	if len(albums) != 2 || albums[0].Detail != "Ozawa · 1990" || albums[1].Detail != "Various Artists" || albums[0].ID != "1" {
		t.Fatalf("albums = %+v", albums)
	}
	// An indexed album opens from the catalog even though it is not "cached".
	tracks := load(t, albums[0].Open)
	var paths []string
	for _, e := range tracks {
		paths = append(paths, e.Track.Path)
	}
	if !slices.Equal(paths, []string{"/m/Mahler/5/CD1/01.flac", "/m/Mahler/5/CD2/01.flac"}) {
		t.Errorf("album tracks = %v", paths)
	}

	ravel := child(t, child(t, root, "Artists"), "Ravel")
	if got := titles(load(t, ravel)); !slices.Equal(got, []string{"Mix"}) {
		t.Errorf("Ravel's albums = %v", got)
	}
	genres := load(t, child(t, root, "Genres"))
	if len(genres) != 1 || genres[0].Detail != "2 albums" {
		t.Fatalf("genres = %+v", genres)
	}
	if got := titles(load(t, genres[0].Open)); !slices.Equal(got, []string{"Mahler 5", "Mix"}) {
		t.Errorf("Classical albums = %v", got)
	}
}

func TestLocalCatalogPlaceholders(t *testing.T) {
	cat, _ := newLocalFixture()
	cat.albums, cat.artists, cat.genres, cat.synced = nil, nil, nil, false
	root := LocalCatalog(cat, nil, "/m")
	for _, name := range []string{"Albums", "Artists", "Genres"} {
		if got := titles(load(t, child(t, root, name))); len(got) != 1 || got[0] != "Indexing your music… (press r to retry if this persists)" {
			t.Errorf("%s before the first index = %v", name, got)
		}
	}
	cat.synced = true
	if got := titles(load(t, child(t, root, "Albums"))); !slices.Equal(got, []string{"No music found in /m"}) {
		t.Errorf("empty index = %v, want a message naming /m", got)
	}
}

func TestRootLocalNeedsCatalogForIndex(t *testing.T) {
	cat, _ := newLocalFixture()
	var prov playlist.Provider = &fakeProvider{name: "Local"}
	local := child(t, Root(Sources{Local: prov, MusicDir: "/m", Catalog: cat}), "Local")
	if got := titles(load(t, local)); !slices.Equal(got, []string{"Albums", "Artists", "Genres", "Folders", "Playlists"}) {
		t.Errorf("Local with a catalog = %v", got)
	}
	local = child(t, Root(Sources{Local: prov, MusicDir: "/m"}), "Local")
	if got := titles(load(t, local)); !slices.Equal(got, []string{"Folders", "Playlists"}) {
		t.Errorf("Local without a catalog = %v", got)
	}
}
