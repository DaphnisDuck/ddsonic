package library

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// fakeProvider is a playlist.Provider with canned lists and tracks.
type fakeProvider struct {
	name   string
	lists  []playlist.PlaylistInfo
	tracks map[string][]playlist.Track
	err    error
	asked  []string // Tracks IDs requested
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Playlists() ([]playlist.PlaylistInfo, error) { return f.lists, f.err }

func (f *fakeProvider) Tracks(id string) ([]playlist.Track, error) {
	f.asked = append(f.asked, id)
	return f.tracks[id], f.err
}

// fakeSpotify adds the capabilities the Spotify provider has.
type fakeSpotify struct {
	fakeProvider
}

func (*fakeSpotify) Authenticate() error { return nil }

func (*fakeSpotify) AlbumSortTypes() []provider.SortType { return nil }

func (*fakeSpotify) DefaultAlbumSort() string { return "" }

func (s *fakeSpotify) AlbumList(_ string, offset, _ int) ([]provider.AlbumInfo, error) {
	if s.err != nil {
		return nil, s.err
	}
	if offset > 0 {
		return nil, nil
	}
	return []provider.AlbumInfo{
		{ID: "a2", Name: "Rite of Spring", Artist: "Stravinsky"},
		{ID: "a1", Name: "Mahler 5", Artist: "Ozawa"},
	}, nil
}

func (*fakeSpotify) Artists() ([]provider.ArtistInfo, error) {
	return []provider.ArtistInfo{{ID: "ar1", Name: "Mahler"}}, nil
}

func (*fakeSpotify) ArtistAlbums(string) ([]provider.AlbumInfo, error) {
	return []provider.AlbumInfo{{ID: "al1", Name: "Symphony No. 5", Year: 1982}}, nil
}

func (*fakeSpotify) AlbumTracks(id string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "spotify:track:" + id, Title: "Trauermarsch"}}, nil
}

func load(t *testing.T, l Level) []Entry {
	t.Helper()
	entries, err := l.Load(context.Background())
	if err != nil {
		t.Fatalf("%s.Load() error = %v", l.Title(), err)
	}
	return entries
}

func titles(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title
	}
	return out
}

// child opens the entry titled title in l.
func child(t *testing.T, l Level, title string) Level {
	t.Helper()
	entries := load(t, l)
	for _, e := range entries {
		if e.Title == title {
			if e.Open == nil {
				t.Fatalf("%s / %s is not browsable", l.Title(), title)
			}
			return e.Open
		}
	}
	t.Fatalf("%s has no entry %q (have %v)", l.Title(), title, titles(entries))
	return nil
}

func TestRootListsConfiguredSources(t *testing.T) {
	local := &fakeProvider{name: "Local"}
	spot := &fakeSpotify{fakeProvider{name: "Spotify"}}
	tests := []struct {
		name       string
		src        Sources
		want       []string
		wantSearch playlist.Provider
	}{
		{
			name:       "everything",
			src:        Sources{Spotify: spot, Local: local, Radio: &fakeProvider{name: "Radio"}},
			want:       []string{"Spotify", "Local", "Radio", "Search"},
			wantSearch: spot,
		},
		{
			name:       "no spotify searches local",
			src:        Sources{Local: local},
			want:       []string{"Local", "Search"},
			wantSearch: local,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := load(t, Root(tt.src))
			if got := titles(entries); !slices.Equal(got, tt.want) {
				t.Fatalf("root = %v, want %v", got, tt.want)
			}
			search := entries[len(entries)-1]
			if search.Intent != IntentSearch || search.Provider != tt.wantSearch {
				t.Errorf("search entry = %+v, want IntentSearch with provider %v", search, tt.wantSearch)
			}
		})
	}
}

func TestSpotifyLevels(t *testing.T) {
	spot := &fakeSpotify{fakeProvider{
		name: "Spotify",
		lists: []playlist.PlaylistInfo{
			{ID: SpotifyLikedSongsID, Name: "Your Music", Section: "Library"},
			{ID: "p1", Name: "Mine", Section: SpotifyOwnPlaylistsSection, TrackCount: 3},
			{ID: "p2", Name: "Theirs", Section: SpotifyFollowedPlaylistsSection},
		},
		tracks: map[string][]playlist.Track{
			SpotifyLikedSongsID: {{Path: "spotify:track:1", Title: "Liked"}},
		},
	}}
	root := Spotify(spot)
	if got, want := titles(load(t, root)), []string{"Albums", "Artists", "Playlists", "Liked Songs"}; !slices.Equal(got, want) {
		t.Fatalf("spotify menu = %v, want %v", got, want)
	}

	albums := load(t, child(t, root, "Albums"))
	if got := titles(albums); !slices.Equal(got, []string{"Mahler 5", "Rite of Spring"}) {
		t.Fatalf("albums = %v, want sorted by artist", got)
	}
	if albums[0].Detail != "Ozawa" || albums[0].Open.Title() != "Mahler 5" {
		t.Errorf("album row = %+v, want Mahler 5 by Ozawa", albums[0])
	}
	if got := load(t, albums[0].Open); len(got) != 1 || got[0].Track.Path != "spotify:track:a1" {
		t.Fatalf("album tracks = %+v", got)
	}
	if _, ok := albums[0].Open.(AuthLevel); !ok {
		t.Error("album level does not offer sign-in")
	}

	playlists := load(t, child(t, root, "Playlists"))
	if got := titles(playlists); !slices.Equal(got, []string{"Mine", "Theirs"}) {
		t.Errorf("playlists = %v", got)
	}
	if playlists[0].Section != SpotifyOwnPlaylistsSection || playlists[0].Detail != "3 tracks" {
		t.Errorf("playlist row = %+v", playlists[0])
	}

	liked := load(t, child(t, root, "Liked Songs"))
	if len(liked) != 1 || spot.asked[len(spot.asked)-1] != SpotifyLikedSongsID {
		t.Errorf("liked songs = %+v (asked %v)", liked, spot.asked)
	}

	artistAlbums := load(t, child(t, child(t, root, "Artists"), "Mahler"))
	if len(artistAlbums) != 1 || artistAlbums[0].Detail != "1982" {
		t.Fatalf("artist albums = %+v", artistAlbums)
	}
	if got := load(t, artistAlbums[0].Open); len(got) != 1 || got[0].Track.Path != "spotify:track:al1" {
		t.Errorf("artist album tracks = %+v", got)
	}
}

func TestSpotifyShowsOnlyAdvertisedCapabilities(t *testing.T) {
	got := titles(load(t, Spotify(&fakeProvider{name: "Spotify"})))
	if want := []string{"Playlists", "Liked Songs"}; !slices.Equal(got, want) {
		t.Errorf("menu = %v, want %v without album/artist browsing", got, want)
	}
}

func TestLoadErrorsPropagate(t *testing.T) {
	spot := &fakeSpotify{fakeProvider{name: "Spotify", err: playlist.ErrNeedsAuth}}
	_, err := child(t, Spotify(spot), "Albums").Load(context.Background())
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("Load() error = %v, want ErrNeedsAuth", err)
	}
}

// fakeRadio has favorites and one labelled category route.
type fakeRadio struct {
	fakeProvider
}

func (*fakeRadio) FavoriteTracks() []playlist.Track {
	return []playlist.Track{{Path: "http://a", Title: "SomaFM", Stream: true}}
}

// starredRadio has favorites in the order they were starred.
type starredRadio struct{ fakeRadio }

func (*starredRadio) FavoriteTracks() []playlist.Track {
	return []playlist.Track{{Path: "http://w", Title: "WBGO"}, {Path: "http://b", Title: "BBC Radio 3"}, {Path: "http://s", Title: "SomaFM"}}
}

func TestRadioFavoritesSortedByName(t *testing.T) {
	favs := load(t, child(t, radio(&starredRadio{fakeRadio{fakeProvider{name: "Radio"}}}, nil), "Favorites"))
	if got := titles(favs); !slices.Equal(got, []string{"BBC Radio 3", "SomaFM", "WBGO"}) {
		t.Errorf("favorites = %v, want A–Z", got)
	}
}

func (*fakeRadio) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{ID: "browse:countries", Name: "Browse all countries", Mode: provider.BrowseGenres},
		{ID: "browse:albums", Name: "Albums", Mode: provider.BrowseAlbums},
	}
}

func (*fakeRadio) GenreBrowserFor(id string) provider.GenreBrowser {
	if id == "browse:countries" {
		return countries{}
	}
	return nil
}

type countries struct{}

func (countries) GenreLabel() string { return "Countries" }
func (countries) Genres() ([]provider.GenreInfo, error) {
	return []provider.GenreInfo{{ID: "NO", Name: "Norway", Group: "Europe", Favorite: true}}, nil
}
func (countries) GenreSortTypes() []provider.SortType { return []provider.SortType{{ID: "votes"}} }
func (countries) GenreTracks(id, sort string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "http://" + id + "/" + sort, Stream: true}}, nil
}

func TestRadioLevels(t *testing.T) {
	r := &fakeRadio{fakeProvider{name: "Radio"}}
	channels := &fakeProvider{name: "cliamp radio", lists: []playlist.PlaylistInfo{{ID: "ch1", Name: "Chill"}}}
	root := radio(r, channels)
	if got := titles(load(t, root)); !slices.Equal(got, []string{"Favorites", "Browse Stations"}) {
		t.Fatalf("radio menu = %v", got)
	}

	favs := load(t, child(t, root, "Favorites"))
	if len(favs) != 1 || favs[0].Title != "SomaFM" || favs[0].Track == nil {
		t.Fatalf("favorites = %+v", favs)
	}

	browse := child(t, root, "Browse Stations")
	if got := titles(load(t, browse)); !slices.Equal(got, []string{"cliamp radio", "Countries"}) {
		t.Fatalf("browse = %v", got)
	}
	chans := load(t, child(t, browse, "cliamp radio"))
	if len(chans) != 1 || chans[0].Title != "Chill" || chans[0].Play == nil {
		t.Fatalf("channels = %+v", chans)
	}
	countryRows := load(t, child(t, browse, "Countries"))
	if len(countryRows) != 1 || countryRows[0].Title != "Norway" || !countryRows[0].Favorite || countryRows[0].Section != "Europe" {
		t.Fatalf("countries = %+v", countryRows)
	}
	if got := load(t, countryRows[0].Open); len(got) != 1 || got[0].Track.Path != "http://NO/votes" {
		t.Errorf("stations = %+v", got)
	}
}

func TestTracks(t *testing.T) {
	a, b := playlist.Track{Path: "a"}, playlist.Track{Path: "b"}
	entries := []Entry{{Title: "menu row"}, {Track: &a}, {Track: &b}}
	tests := []struct {
		i      int
		wantAt int
	}{{0, -1}, {1, 0}, {2, 1}}
	for _, tt := range tests {
		tracks, at := Tracks(entries, tt.i)
		if len(tracks) != 2 || at != tt.wantAt {
			t.Errorf("Tracks(entries, %d) = %d tracks at %d, want 2 at %d", tt.i, len(tracks), at, tt.wantAt)
		}
	}
}

func TestGroupBySection(t *testing.T) {
	in := []Entry{
		{Title: "US", Section: "Americas"}, {Title: "DE", Section: "Europe"},
		{Title: "MX", Section: "Americas"}, {Title: "FR", Section: "Europe"},
	}
	if got, want := titles(groupBySection(in)), []string{"US", "MX", "DE", "FR"}; !slices.Equal(got, want) {
		t.Errorf("groupBySection = %v, want %v", got, want)
	}
}
