package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// searchable is a Spotify provider that can also search live, and a radio
// provider with a directory.
type searchable struct{ liveSpotify }

func (*searchable) SearchTracks(context.Context, string, int) ([]playlist.Track, error) {
	return nil, nil
}

type directoryRadio struct{ fakeProvider }

func (*directoryRadio) SearchStationTracks(q string) ([]playlist.Track, error) {
	return []playlist.Track{{Path: "https://dir/" + q, Title: "Directory " + q}}, nil
}

func lref3(p, id string) catalog.Ref { return catalog.Ref{Provider: p, ProviderID: id} }

func newSearchFixture(t *testing.T) (*fakeCatalog, *searchable, Level) {
	t.Helper()
	cat, _, _ := newCatalogFixture()
	spotifyAlbum := cat.albums[0] // cached, ID 1
	localAlbum := catalog.Album{ID: 40, Ref: lref3(catalog.Local, "/m/holst"), Title: "The Planets", Artist: "Holst", Year: 1990}
	cat.albums = append(cat.albums, localAlbum)
	cat.albumTracks[40] = []catalog.Track{
		{ID: 41, Ref: lref3(catalog.Local, "/m/holst/1.flac"), Title: "Mars", AlbumID: 40, PlayableURI: "/m/holst/1.flac"},
		{ID: 42, Ref: lref3(catalog.Local, "/m/holst/2.flac"), Title: "Venus", AlbumID: 40, PlayableURI: "/m/holst/2.flac"},
	}
	cat.found = catalog.SearchResults{
		catalog.SearchArtist: {
			{Kind: catalog.SearchArtist, Provider: catalog.Spotify, Artist: &catalog.Artist{ID: 5, Ref: sref2("ar-ozawa"), Name: "Ozawa"}},
			{Kind: catalog.SearchArtist, Provider: catalog.Local, Artist: &catalog.Artist{ID: 6, Ref: lref3(catalog.Local, "holst"), Name: "Holst"}},
		},
		catalog.SearchAlbum: {
			{Kind: catalog.SearchAlbum, Provider: catalog.Spotify, Album: &spotifyAlbum},
			{Kind: catalog.SearchAlbum, Provider: catalog.Local, Album: &localAlbum},
		},
		catalog.SearchTrack: {
			{Kind: catalog.SearchTrack, Provider: catalog.Local, Track: &cat.albumTracks[40][1]},
		},
		catalog.SearchPlaylist: {
			{Kind: catalog.SearchPlaylist, Provider: catalog.Spotify, Playlist: &cat.playlists[0]},
		},
		catalog.SearchStation: {
			{Kind: catalog.SearchStation, Provider: catalog.Radio, Track: &catalog.Track{ID: 50, Ref: lref3(catalog.Radio, "https://wbgo"), Title: "WBGO", PlayableURI: "https://wbgo"}},
		},
	}
	sp := &searchable{liveSpotify{fakeProvider: fakeProvider{name: "Spotify"}}}
	root := Root(Sources{Spotify: sp, Local: &fakeProvider{name: "Local"}, Radio: &directoryRadio{fakeProvider{name: "Radio"}},
		MusicDir: "/m", Catalog: cat, Synced: spotifySynced(sp)})
	return cat, sp, child(t, root, "Search")
}

func searchFor(t *testing.T, root Level, query string) []Entry {
	t.Helper()
	sl, ok := root.(SearchLevel)
	if !ok {
		t.Fatalf("Search is %T, not a SearchLevel", root)
	}
	return load(t, sl.WithQuery(query))
}

func TestSearchResultsBySection(t *testing.T) {
	_, _, search := newSearchFixture(t)
	if got := load(t, search); len(got) != 0 {
		t.Errorf("empty query = %+v, want nothing", got)
	}
	rows := searchFor(t, search, "holst")
	type row struct{ section, title, detail string }
	var got []row
	for _, e := range rows {
		got = append(got, row{e.Section, e.Title, e.Detail})
	}
	want := []row{
		{"Artists", "Ozawa", "Spotify"},
		{"Artists", "Holst", "Local"},
		{"Albums", "Cached", "Spotify · Ozawa"},
		{"Albums", "The Planets", "Local · Holst · 1990"},
		{"Tracks", "Venus", "Local"},
		{"Playlists", "Mine", "Spotify · 1 tracks"},
		{"Stations", "WBGO", "Radio"},
		{beyondSection, "Search Spotify for “holst”", ""},
		{beyondSection, "Search the radio directory for “holst”", ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows =\n%v\nwant\n%v", got, want)
	}
	ids := map[string]bool{}
	for _, e := range rows[:7] {
		if e.ID == "" || ids[e.ID] {
			t.Errorf("row %q has ID %q, want a unique one", e.Title, e.ID)
		}
		ids[e.ID] = true
	}
	// The explicit rows carry the query without its operators.
	rows = searchFor(t, search, "artist:holst mars")
	if spotify := rows[len(rows)-2]; spotify.Intent != IntentSearch || spotify.Query != "holst mars" {
		t.Errorf("Spotify row = %+v", spotify)
	}
	if got := titles(load(t, rows[len(rows)-1].Open)); !slices.Equal(got, []string{"Directory holst mars"}) {
		t.Errorf("radio directory = %v", got)
	}
}

func TestSearchNoMatchesAndMore(t *testing.T) {
	cat, _, search := newSearchFixture(t)
	cat.found = nil
	if got := titles(searchFor(t, search, "zzz")); !slices.Equal(got, []string{noMatches,
		"Search Spotify for “zzz”", "Search the radio directory for “zzz”"}) {
		t.Errorf("no matches = %v", got)
	}

	var many []catalog.SearchResult
	for i := range 30 {
		many = append(many, catalog.SearchResult{Kind: catalog.SearchArtist, Provider: catalog.Local,
			Artist: &catalog.Artist{ID: int64(100 + i), Ref: lref3(catalog.Local, fmt.Sprint(i)), Name: fmt.Sprintf("Artist %d", i)}})
	}
	cat.found = catalog.SearchResults{catalog.SearchArtist: many}
	rows := searchFor(t, search, "artist")
	var artists int
	var more *Entry
	for i, e := range rows {
		if e.Section == "Artists" {
			if strings.HasPrefix(e.Title, "More") {
				more = &rows[i]
			} else {
				artists++
			}
		}
	}
	if artists != 5 || more == nil || more.Title != "More artists…" {
		t.Fatalf("artists shown = %d, more = %+v", artists, more)
	}
	if got := load(t, more.Open); len(got) != 30 {
		t.Errorf("More artists = %d rows, want all 30", len(got))
	}
	if last := cat.queries[len(cat.queries)-1]; !slices.Equal(last.Kinds, []catalog.SearchKind{catalog.SearchArtist}) {
		t.Errorf("More searched kinds %v", last.Kinds)
	}
}

func playFrom(t *testing.T, e Entry) ([]string, int) {
	t.Helper()
	if e.Track == nil || e.PlayFrom == nil {
		t.Fatalf("row %q is not a playable search track: %+v", e.Title, e)
	}
	tracks, i, err := e.PlayFrom(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, tr := range tracks {
		titles = append(titles, tr.Title)
	}
	return titles, i
}

// A searched track plays its album from that track; a station plays alone,
// as a stream.
func TestSearchTrackPlaysItsAlbum(t *testing.T) {
	_, _, search := newSearchFixture(t)
	rows := searchFor(t, search, "venus")
	var venus, wbgo Entry
	for _, e := range rows {
		switch e.Title {
		case "Venus":
			venus = e
		case "WBGO":
			wbgo = e
		}
	}
	if got, i := playFrom(t, venus); !slices.Equal(got, []string{"Mars", "Venus"}) || i != 1 {
		t.Errorf("Venus plays %v from %d, want its album from Venus", got, i)
	}
	if got, i := playFrom(t, wbgo); !slices.Equal(got, []string{"WBGO"}) || i != 0 || !wbgo.Track.Stream || !wbgo.Track.Realtime {
		t.Errorf("station plays %v from %d, stream %v", got, i, wbgo.Track.Stream)
	}
}

// fetchCatalog fetches uncached albums, or fails like an offline one.
type fetchCatalog struct {
	*fakeCatalog
	offline bool
}

func (f *fetchCatalog) FetchAlbumTracks(_ context.Context, a catalog.Album) ([]catalog.Track, error) {
	if f.offline {
		return nil, errors.New("offline")
	}
	return []catalog.Track{{ID: 60, Ref: sref2("t60"), Title: "One"}, {ID: 61, Ref: sref2("t61"), Title: "Two", AlbumID: a.ID}}, nil
}

func TestSearchTrackFetchesUncachedAlbum(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	fc := &fetchCatalog{fakeCatalog: cat}
	s := &catalogView{cat: fc, synced: map[string]*catalogBrowser{catalog.Spotify: {cat: fc}}}
	two := catalog.Track{ID: 61, Ref: sref2("t61"), Title: "Two", AlbumID: 2, PlayableURI: "spotify:track:t61"}
	e, ok := s.entry(catalog.SearchResult{Kind: catalog.SearchTrack, Provider: catalog.Spotify, Track: &two})
	if !ok {
		t.Fatal("track result dropped")
	}
	if got, i := playFrom(t, e); !slices.Equal(got, []string{"One", "Two"}) || i != 1 {
		t.Errorf("uncached album plays %v from %d, want it fetched", got, i)
	}
	fc.offline = true
	if got, i := playFrom(t, e); !slices.Equal(got, []string{"Two"}) || i != 0 {
		t.Errorf("offline plays %v from %d, want just the track", got, i)
	}
	lone := catalog.Track{ID: 70, Title: "Single", PlayableURI: "spotify:track:x"}
	e, _ = s.entry(catalog.SearchResult{Kind: catalog.SearchTrack, Provider: catalog.Spotify, Track: &lone})
	if got, _ := playFrom(t, e); !slices.Equal(got, []string{"Single"}) {
		t.Errorf("album-less track plays %v", got)
	}
}

func TestRootSearchWithoutCatalog(t *testing.T) {
	root := Root(Sources{Spotify: &fakeProvider{name: "Spotify"}})
	for _, e := range load(t, root) {
		if e.Title == "Search" && (e.Intent != IntentSearch || e.Open != nil) {
			t.Errorf("Search without a catalog = %+v, want the provider search intent", e)
		}
	}
}

// An uncached album holds only the tracks the catalog met elsewhere; when it
// cannot be fetched, only the searched track plays, never that fragment.
func TestSearchTrackNeverPlaysAPartialAlbum(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	liked := catalog.Track{ID: 61, Ref: sref2("t61"), Title: "Two", AlbumID: 2, PlayableURI: "spotify:track:t61"}
	other := catalog.Track{ID: 62, Ref: sref2("t62"), Title: "Also liked", AlbumID: 2, PlayableURI: "spotify:track:t62"}
	cat.albumTracks[2] = []catalog.Track{liked, other} // album 2 is uncached
	for name, c := range map[string]catalog.Catalog{
		"no fetcher": cat,
		"offline":    &fetchCatalog{fakeCatalog: cat, offline: true},
	} {
		s := &catalogView{cat: c}
		e, _ := s.entry(catalog.SearchResult{Kind: catalog.SearchTrack, Provider: catalog.Spotify, Track: &liked})
		if got, i := playFrom(t, e); !slices.Equal(got, []string{"Two"}) || i != 0 {
			t.Errorf("%s: plays %v from %d, want just the track", name, got, i)
		}
	}
	s := &catalogView{cat: &fetchCatalog{fakeCatalog: cat}}
	e, _ := s.entry(catalog.SearchResult{Kind: catalog.SearchTrack, Provider: catalog.Spotify, Track: &liked})
	if got, i := playFrom(t, e); !slices.Equal(got, []string{"One", "Two"}) || i != 1 {
		t.Errorf("online: plays %v from %d, want the fetched album", got, i)
	}
}

// A playlist of any synced source opens from search, labelled with it.
func TestSearchPlaylistOfAnySyncedSource(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	mix := catalog.Playlist{ID: 90, Ref: lref3("youtube", "PLmix"), Name: "Road Trip", TrackCount: 1}
	cat.plTracks[90] = []catalog.Track{{ID: 91, Title: "Song", PlayableURI: "https://music.youtube.com/watch?v=x"}}
	cat.found = catalog.SearchResults{catalog.SearchPlaylist: {{Kind: catalog.SearchPlaylist, Provider: "youtube", Playlist: &mix}}}
	yt := SyncedSource{Provider: "youtube", Title: "YouTube Music", Player: &fakeProvider{name: "YouTube Music"},
		Collections: []string{catalog.CollectionPlaylists}}
	search := child(t, Root(Sources{Catalog: cat, Synced: []SyncedSource{yt}}), "Search")
	rows := searchFor(t, search, "road")
	if len(rows) == 0 || rows[0].Title != "Road Trip" || rows[0].Detail != "YouTube · 1 tracks" {
		t.Fatalf("rows = %+v", rows)
	}
	if got := load(t, rows[0].Open); len(got) != 1 || got[0].Track.Path != "https://music.youtube.com/watch?v=x" {
		t.Errorf("playlist tracks = %+v", got)
	}
}

// Each synced source that can search live gets its own row.
func TestSearchLiveRowPerSearchableSource(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	cat.found = nil
	yt := SyncedSource{Provider: "youtube", Title: "YouTube Music", Player: &searchable{liveSpotify{fakeProvider: fakeProvider{name: "YouTube Music"}}},
		Collections: []string{catalog.CollectionPlaylists}}
	quiet := SyncedSource{Provider: "other", Title: "Other", Player: &fakeProvider{name: "Other"}, Collections: []string{catalog.CollectionPlaylists}}
	search := child(t, Root(Sources{Catalog: cat, Synced: []SyncedSource{yt, quiet}}), "Search")
	rows := searchFor(t, search, "x")
	if got := titles(rows); !slices.Equal(got, []string{noMatches, "Search YouTube Music for “x”"}) {
		t.Errorf("rows = %v", got)
	}
	if last := rows[len(rows)-1]; last.Intent != IntentSearch || last.Provider != yt.Player || last.Query != "x" {
		t.Errorf("YouTube row = %+v", last)
	}
}

// neverFetch fails the test when an album fetch is attempted.
type neverFetch struct {
	*fakeCatalog
	t *testing.T
}

func (n neverFetch) FetchAlbumTracks(context.Context, catalog.Album) ([]catalog.Track, error) {
	n.t.Error("a partial source's album was fetched")
	return nil, errors.New("no")
}

// A partial source's album (YouTube's: only your tracks of it) opens and
// plays as it is, never fetched whole.
func TestListsFromTracks(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	album := catalog.Album{ID: 70, Ref: lref3("youtube", "album:chicago/best"), Title: "Best of Chicago", Artist: "Chicago"}
	cat.albums = append(cat.albums, album)
	beginnings := catalog.Track{ID: 71, Ref: lref3("youtube", "v1"), Title: "Beginnings", AlbumID: 70, PlayableURI: "https://music.youtube.com/watch?v=v1"}
	smile := catalog.Track{ID: 72, Ref: lref3("youtube", "v2"), Title: "Make Me Smile", AlbumID: 70, PlayableURI: "https://music.youtube.com/watch?v=v2"}
	cat.albumTracks[70] = []catalog.Track{beginnings, smile} // not "cached"
	c := neverFetch{cat, t}
	yt := SyncedSource{Provider: "youtube", Title: "YouTube Music", Player: &fakeProvider{name: "YouTube Music"},
		Collections: []string{catalog.CollectionAlbums}, ListsFromTracks: true}

	albums := load(t, child(t, SyncedMenu(c, yt), AlbumsFromTracks))
	var row Entry
	for _, e := range albums {
		if e.Title == "Best of Chicago" {
			row = e
		}
	}
	if got := titles(load(t, row.Open)); !slices.Equal(got, []string{"Beginnings", "Make Me Smile"}) {
		t.Errorf("album opens as %v", got)
	}
	s := newCatalogView(c, Sources{Catalog: c, Synced: []SyncedSource{yt}})
	e, _ := s.entry(catalog.SearchResult{Kind: catalog.SearchTrack, Provider: "youtube", Track: &smile})
	if got, i := playFrom(t, e); !slices.Equal(got, []string{"Beginnings", "Make Me Smile"}) || i != 1 {
		t.Errorf("searched track plays %v from %d, want its known album from it", got, i)
	}
}

// Rows record their source where it is decided: a source's root entry, and
// each row of a list that mixes sources.
func TestRowsCarryTheirSource(t *testing.T) {
	cat, sp, search := newSearchFixture(t)
	var got []string
	for _, e := range searchFor(t, search, "holst") {
		got = append(got, e.Title+"="+e.Source)
	}
	want := []string{"Ozawa=spotify", "Holst=local", "Cached=spotify", "The Planets=local", "Venus=local",
		"Mine=spotify", "WBGO=radio", "Search Spotify for “holst”=", "Search the radio directory for “holst”=radio"}
	if !slices.Equal(got, want) {
		t.Errorf("search sources = %v, want %v", got, want)
	}

	root := Root(Sources{Spotify: sp, Local: &fakeProvider{name: "Local"}, Radio: &fakeProvider{name: "Radio"},
		MusicDir: "/m", Catalog: cat, Synced: spotifySynced(sp)})
	got = nil
	for _, e := range load(t, root) {
		got = append(got, e.Title+"="+e.Source)
	}
	if want := []string{"All Music=", "Spotify=spotify", "Local=local", "Radio=radio", "Search="}; !slices.Equal(got, want) {
		t.Errorf("root sources = %v, want %v", got, want)
	}
	for _, e := range load(t, child(t, child(t, root, "All Music"), "Albums")) {
		if e.Source == "" {
			t.Errorf("All Music album %q has no source", e.Title)
		}
	}
}

// Loads of one More… level can overlap (open, back out, open again before
// the first finishes); each must restrict its own query. Run with -race.
func TestSearchMoreLoadsOverlap(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	s := &catalogView{cat: cat, synced: map[string]*catalogBrowser{catalog.Spotify: {cat: cat}}}
	more := s.more(catalog.ParseQuery("ozawa"), catalog.SearchAlbum, "Albums")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := more.Load(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// sharedRecording is a search fixture in which one recording is held by
// every kind of source at once: Local, Spotify and a source whose albums
// come from its tracks (YouTube Music), with a radio station beside them.
func sharedRecording(t *testing.T) (*fakeCatalog, Level) {
	t.Helper()
	cat, _, _ := newCatalogFixture()
	held := func(id int64, provider, uri string) catalog.SearchResult {
		return catalog.SearchResult{Kind: catalog.SearchTrack, Provider: provider, Track: &catalog.Track{
			ID: id, Ref: lref3(provider, uri), Title: "Shared Song", Artist: "Echo Artist", PlayableURI: uri}}
	}
	cat.found = catalog.SearchResults{
		catalog.SearchTrack: {
			held(71, catalog.Local, "/m/echo/1.mp3"),
			held(72, catalog.Spotify, "spotify:track:t72"),
			held(73, catalog.YouTube, "https://music.youtube.com/watch?v=v73"),
		},
		catalog.SearchStation: {{Kind: catalog.SearchStation, Provider: catalog.Radio,
			Track: &catalog.Track{ID: 74, Ref: lref3(catalog.Radio, "https://echo"), Title: "Echo FM", PlayableURI: "https://echo"}}},
	}
	sp := &searchable{liveSpotify{fakeProvider: fakeProvider{name: "Spotify"}}}
	yt := SyncedSource{Provider: catalog.YouTube, Title: "YouTube Music", Player: &fakeProvider{name: "YouTube Music"},
		Collections: []string{catalog.CollectionLiked}, ListsFromTracks: true}
	root := Root(Sources{Spotify: sp, Local: &fakeProvider{name: "Local"}, Radio: &directoryRadio{fakeProvider{name: "Radio"}},
		MusicDir: "/m", Catalog: cat, Synced: append(spotifySynced(sp), yt)})
	return cat, child(t, root, "Search")
}

// Every row of an ordinary search says which source it is from, track and
// station rows included. A recording held in several sources is one row
// per source, and the label is all that tells those rows apart.
func TestSearchTrackRowsNameTheirSource(t *testing.T) {
	_, search := sharedRecording(t)
	type row struct{ section, title, source, detail string }
	var got []row
	for _, e := range searchFor(t, search, "shared") {
		if e.Track == nil {
			continue
		}
		got = append(got, row{e.Section, e.Title, e.Source, e.Detail})
		// The label is the source's display name, whatever the source.
		if e.Detail != SourceLabel(e.Source) {
			t.Errorf("row %q of %q is labelled %q, want %q", e.Title, e.Source, e.Detail, SourceLabel(e.Source))
		}
	}
	want := []row{
		{"Tracks", "Shared Song", catalog.Local, "Local"},
		{"Tracks", "Shared Song", catalog.Spotify, "Spotify"},
		// A track is a synced track: "from tracks" belongs to the albums
		// and artists such a source derives, not to the track itself.
		{"Tracks", "Shared Song", catalog.YouTube, "YouTube"},
		{"Stations", "Echo FM", catalog.Radio, "Radio"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("track and station rows =\n%v\nwant\n%v", got, want)
	}
}

// The label is on the row, not in what the row plays: the track that is
// played or queued keeps its own title and artist.
func TestSearchTrackLabelStaysOutOfTheTrack(t *testing.T) {
	_, search := sharedRecording(t)
	for _, e := range searchFor(t, search, "shared") {
		if e.Track == nil {
			continue
		}
		if e.Title != e.Track.Title || strings.Contains(e.Track.Title+e.Track.Artist+e.Track.Album, e.Detail) {
			t.Errorf("row %q of %q plays %+v: the source label leaked into the track", e.Title, e.Source, *e.Track)
		}
		if e.PlayFrom == nil {
			t.Errorf("row %q of %q has nothing to play", e.Title, e.Source)
		}
	}
}

// "More tracks…" lists rows of the same kind, built the same way.
func TestSearchMoreTrackRowsNameTheirSource(t *testing.T) {
	cat, search := sharedRecording(t)
	var many []catalog.SearchResult
	for i := range 30 {
		provider := []string{catalog.Local, catalog.Spotify, catalog.YouTube}[i%3]
		many = append(many, catalog.SearchResult{Kind: catalog.SearchTrack, Provider: provider, Track: &catalog.Track{
			ID: int64(200 + i), Ref: lref3(provider, fmt.Sprint("t", i)), Title: fmt.Sprintf("Song %d", i), PlayableURI: fmt.Sprint("uri", i)}})
	}
	cat.found = catalog.SearchResults{catalog.SearchTrack: many}
	var more *Entry
	rows := searchFor(t, search, "song")
	for i, e := range rows {
		if e.Section == "Tracks" && strings.HasPrefix(e.Title, "More") {
			more = &rows[i]
		}
	}
	if more == nil {
		t.Fatalf("no More row among %v", titles(rows))
	}
	all := load(t, more.Open)
	if len(all) != 30 {
		t.Fatalf("More tracks = %d rows, want 30", len(all))
	}
	for _, e := range all {
		if e.Detail == "" || e.Detail != SourceLabel(e.Source) {
			t.Errorf("More row %q of %q is labelled %q", e.Title, e.Source, e.Detail)
		}
	}
}

// Outside search a list is one source's, and its track rows carry no
// label: an album's tracks, a playlist's, Liked Songs.
func TestBrowsedTrackRowsCarryNoSourceLabel(t *testing.T) {
	_, _, root := newCatalogFixture()
	lists := map[string][]Entry{
		"an album's tracks":   load(t, child(t, child(t, root, "Albums"), "Cached")),
		"a playlist's tracks": load(t, child(t, child(t, root, "Playlists"), "Mine")),
		"Liked Songs":         load(t, child(t, root, "Liked Songs")),
	}
	for name, entries := range lists {
		tracks := 0
		for _, e := range entries {
			if e.Track == nil {
				continue
			}
			tracks++
			if e.Detail != "" {
				t.Errorf("%s: track row %q is labelled %q, want none", name, e.Title, e.Detail)
			}
		}
		if tracks == 0 {
			t.Errorf("%s: no track rows to check: %v", name, titles(entries))
		}
	}
}
