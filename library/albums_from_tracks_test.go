package library

import (
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
)

func youtubeSource(cols ...string) SyncedSource {
	return SyncedSource{Provider: catalog.YouTube, Title: "YouTube Music", Player: &fakeProvider{name: "YouTube Music"},
		Collections: cols, LikedTitle: "Liked Music", ListsFromTracks: true}
}

// A source whose albums and artists come from its tracks (YouTube Music)
// never lists them as "Albums" and "Artists", which would read as the saved
// albums and followed artists it does not sync: the lists are "Albums from
// tracks" and "Artists from tracks", and each says what it holds.
func TestPartialSourceListsAreNotCalledAlbumsAndArtists(t *testing.T) {
	cat, live, _ := newCatalogFixture()
	yt := youtubeSource(catalog.CollectionAlbums, catalog.CollectionArtists, catalog.CollectionPlaylists, catalog.CollectionLiked)
	menu := SyncedMenu(cat, yt)
	if got := titles(load(t, menu)); !slices.Equal(got, []string{"Albums from tracks", "Artists from tracks", "Playlists", "Liked Music"}) {
		t.Errorf("menu = %v", got)
	}
	level := child(t, menu, AlbumsFromTracks)
	if level.Title() != "Albums from tracks" {
		t.Errorf("level title = %q", level.Title())
	}
	if _, ok := level.(OrderedLevel); !ok {
		t.Error("the list lost its order key")
	}
	nl, ok := level.(NotedLevel)
	if !ok || len(nl.Note()) == 0 {
		t.Fatal("the list carries no note")
	}
	note := strings.Join(nl.Note(), " ")
	for _, want := range []string{
		"Saved YouTube Music Library Albums are not synchronized",
		"Liked Music", "synced playlists", "may be incomplete",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}

	artists := child(t, menu, ArtistsFromTracks)
	if artists.Title() != "Artists from tracks" {
		t.Errorf("artists level title = %q", artists.Title())
	}
	if cl, ok := artists.(CatalogLevel); !ok || cl.CatalogProvider() != catalog.YouTube {
		t.Error("the artists list is no longer a catalog level")
	}
	an, ok := artists.(NotedLevel)
	if !ok || !slices.Equal(an.Note(), []string{
		"Followed YouTube Music artists are not synchronized.",
		"Grouped from Liked Music and synced playlists.",
	}) {
		t.Errorf("artists note = %v", an)
	}
	if want := []string{
		"Saved YouTube Music Library Albums are not synchronized.",
		"Grouped from Liked Music and synced playlists; albums may be incomplete.",
	}; !slices.Equal(nl.Note(), want) {
		t.Errorf("albums note = %v, want %v", nl.Note(), want)
	}

	// Spotify's and All Music's lists keep their names and carry no note:
	// Spotify's are saved albums and followed artists.
	all := newCatalogView(cat, Sources{Catalog: cat, Synced: spotifySynced(live)}).libraryMenu()
	for name, m := range map[string]Level{"Spotify": SyncedMenu(cat, spotifySource(live)), "All Music": all} {
		for _, list := range []string{"Albums", "Artists"} {
			if nl, ok := child(t, m, list).(NotedLevel); ok && len(nl.Note()) != 0 {
				t.Errorf("%s %s carries a note: %v", name, list, nl.Note())
			}
		}
	}
	// The other lists of a partial source carry none either.
	for _, list := range []string{"Playlists", "Liked Music"} {
		if nl, ok := child(t, menu, list).(NotedLevel); ok && len(nl.Note()) != 0 {
			t.Errorf("%s carries a note: %v", list, nl.Note())
		}
	}
}

// An empty "… from tracks" list says why, once a sync has run; before that
// it is syncing, like every list. Spotify's empty lists stay empty.
func TestPartialSourceEmptyState(t *testing.T) {
	for list, want := range map[string]string{
		AlbumsFromTracks:  "No albums found in your tracks yet",
		ArtistsFromTracks: "No artists found in your tracks yet",
	} {
		t.Run(list, func(t *testing.T) {
			cat, live, _ := newCatalogFixture()
			cat.albums, cat.artists, cat.synced = nil, nil, false
			level := child(t, SyncedMenu(cat, youtubeSource(catalog.CollectionAlbums, catalog.CollectionArtists)), list)
			if got := load(t, level); len(got) != 1 || !strings.HasPrefix(got[0].Title, "Syncing") {
				t.Errorf("before the first sync: %+v", got)
			}
			cat.synced = true
			got := load(t, level)
			if len(got) != 1 || got[0].Title != want || got[0].Open != nil || got[0].Track != nil {
				t.Errorf("synced and empty: %+v", got)
			}
			spotify := strings.TrimSuffix(list, " from tracks")
			if got := load(t, child(t, SyncedMenu(cat, spotifySource(live)), spotify)); len(got) != 0 {
				t.Errorf("Spotify's empty %s = %+v, want no rows", spotify, got)
			}
		})
	}
}

// Where sources mix (All Music, search), a partial source's album and
// artist rows say they come from tracks; other sources' rows are unchanged.
func TestPartialSourceRowsSayFromTracks(t *testing.T) {
	cat, live, _ := newCatalogFixture()
	s := newCatalogView(cat, Sources{Catalog: cat, Synced: []SyncedSource{spotifySource(live), youtubeSource(catalog.CollectionAlbums)}})
	tests := []struct {
		album catalog.Album
		want  string
	}{
		{catalog.Album{ID: 70, Ref: lref3("youtube", "album:chicago/best"), Title: "Best of Chicago", Artist: "Chicago"}, "YouTube · from tracks · Chicago"},
		{catalog.Album{ID: 71, Ref: lref3("youtube", "album:unknown"), Title: "Untitled"}, "YouTube · from tracks"},
		{catalog.Album{ID: 1, Ref: sref2("al-cached"), Title: "Cached", Artist: "Ozawa"}, "Spotify · Ozawa"},
	}
	for _, tt := range tests {
		e, ok := s.albumRow(tt.album)
		if !ok || e.Detail != tt.want {
			t.Errorf("%s row detail = %q (ok %v), want %q", tt.album.Title, e.Detail, ok, tt.want)
		}
	}
	// Search results use the same rows.
	e, ok := s.entry(catalog.SearchResult{Kind: catalog.SearchAlbum, Provider: "youtube", Album: &tests[0].album})
	if !ok || e.Detail != tests[0].want {
		t.Errorf("search row detail = %q (ok %v)", e.Detail, ok)
	}

	artists := []struct {
		artist catalog.Artist
		want   string
	}{
		{catalog.Artist{ID: 80, Ref: lref3("youtube", "artist:chicago"), Name: "Chicago"}, "YouTube · from tracks"},
		{catalog.Artist{ID: 5, Ref: sref2("ar-ozawa"), Name: "Ozawa"}, "Spotify"},
	}
	for _, tt := range artists {
		e, ok := s.artistRow(tt.artist)
		if !ok || e.Detail != tt.want || e.Source != tt.artist.Ref.Provider {
			t.Errorf("%s row = %q from %q (ok %v), want %q", tt.artist.Name, e.Detail, e.Source, ok, tt.want)
		}
	}
	e, ok = s.entry(catalog.SearchResult{Kind: catalog.SearchArtist, Provider: "youtube", Artist: &artists[0].artist})
	if !ok || e.Detail != artists[0].want {
		t.Errorf("search artist row detail = %q (ok %v)", e.Detail, ok)
	}
}
