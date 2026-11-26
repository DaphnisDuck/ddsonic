package library

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// snapshotChannels is the cliamp radio channel list of the dated real
// snapshot (external/radio/testdata), as the channel provider lists it:
// each channel with songs, named as served.
func snapshotChannels(t *testing.T) *fakeProvider {
	t.Helper()
	data, err := os.ReadFile("../external/radio/testdata/channels-20261125.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Stations []struct {
			ID, Name, Stream string
			Tracks           int
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Stations) == 0 {
		t.Fatal("empty snapshot")
	}
	p := &fakeProvider{name: "cliamp radio", tracks: map[string][]playlist.Track{}}
	for _, s := range doc.Stations {
		p.lists = append(p.lists, playlist.PlaylistInfo{ID: s.ID, Name: s.Name, TrackCount: s.Tracks})
		p.tracks[s.ID] = []playlist.Track{{Path: "https://songs/" + s.ID + "/1", Title: s.Name + " song"}}
	}
	return p
}

func refs(rs ...ChannelRef) func() []ChannelRef { return func() []ChannelRef { return rs } }

// Every song channel of the real list can be a favorite from Browse; a
// live-only channel (none today) cannot.
func TestBrowseChannelRowCarriesChannelOnlyWithSongs(t *testing.T) {
	channels := snapshotChannels(t)
	channels.lists = append(channels.lists, playlist.PlaylistInfo{ID: "live", Name: "Live · live"})
	rows := load(t, child(t, child(t, radio(&fakeRadio{fakeProvider{name: "Radio"}}, channels, nil), "Browse Stations"), "cliamp radio"))
	for _, e := range rows {
		if live := e.Title == "Live · live"; (e.Channel == nil) != live {
			t.Errorf("row %q Channel = %+v", e.Title, e.Channel)
		} else if !live && e.Channel.Name != e.Title {
			t.Errorf("row %q Channel = %+v, want its own name", e.Title, e.Channel)
		}
	}
}

// Radio → Favorites lists starred stations and favorite channels in one
// A–Z list, channels marked and under their saved name; Enter on a channel
// queues its songs, and on a station plays the stations alone.
func TestRadioFavoritesMergesChannelsAZ(t *testing.T) {
	channels := snapshotChannels(t)
	prov := &savedRadio{directoryRadio: directoryRadio{fakeProvider{name: "Radio"}},
		starred: []playlist.Track{{Path: "https://z", Title: "Zulu FM"}, {Path: "https://a", Title: "Alpha FM"}}}
	fav := child(t, Root(Sources{Radio: prov, Channels: channels,
		ChannelFavorites: refs(ChannelRef{ID: "lofi", Name: "Lofi"}, ChannelRef{ID: "meditative", Name: "Calm (saved)"})}), "Radio")
	rows := load(t, child(t, fav, "Favorites"))
	if got, want := titles(rows), []string{"Alpha FM", "Calm (saved)", "Lofi", "Zulu FM"}; !slices.Equal(got, want) {
		t.Fatalf("Favorites = %q, want %q", got, want)
	}
	for _, e := range rows {
		if isChannel := e.Channel != nil; isChannel != (e.Detail == channelMarker) || isChannel == (e.Track != nil) {
			t.Errorf("row %q: Channel %v, Detail %q, Track %v", e.Title, e.Channel, e.Detail, e.Track)
		}
	}
	played, err := rows[1].Play(t.Context())
	if err != nil || len(played) != 1 || played[0].Path != "https://songs/meditative/1" {
		t.Errorf("channel favorite plays %+v, %v; want the channel's songs", played, err)
	}
	if queued, _ := Tracks(rows, 3); !slices.Equal(stationNames(queued), []string{"Alpha FM <https://a>", "Zulu FM <https://z>"}) {
		t.Errorf("station plays with %q, want the stations alone", stationNames(queued))
	}
}

// Radio → Favorites shows with channel favorites alone: no station provider
// favorites, the channels are still listed.
func TestRadioFavoritesWithoutStationFavorites(t *testing.T) {
	r := radio(&fakeProvider{name: "Radio"}, snapshotChannels(t), refs(ChannelRef{ID: "lofi", Name: "Lofi"}))
	if got := titles(load(t, child(t, r, "Favorites"))); !slices.Equal(got, []string{"Lofi"}) {
		t.Errorf("Favorites = %q", got)
	}
}

// A favorite whose channel a loaded list no longer has is stale; a list
// that fails to load is the temporary load error; a channel without songs
// is an error, never its stream. The favorite stays either way.
func TestChannelFavoritePlayErrors(t *testing.T) {
	loadErr := errors.New("cliamp radio channels: timeout")
	noSongs := snapshotChannels(t)
	noSongs.tracks["lofi"] = []playlist.Track{{Path: "https://radio/lofi/stream", Realtime: true}}
	empty := snapshotChannels(t)
	empty.tracks["lofi"] = nil
	failing := snapshotChannels(t)
	failing.err = loadErr
	for _, tc := range []struct {
		name     string
		channels *fakeProvider
		id       string
		want     func(error) bool
	}{
		{"gone", snapshotChannels(t), "retired", func(err error) bool { return errors.Is(err, ErrChannelGone) }},
		{"temporary", failing, "lofi", func(err error) bool { return errors.Is(err, loadErr) && !errors.Is(err, ErrChannelGone) }},
		{"stream only", noSongs, "lofi", func(err error) bool { return err != nil && strings.Contains(err.Error(), "no songs") }},
		{"no songs", empty, "lofi", func(err error) bool { return err != nil && strings.Contains(err.Error(), "no songs") }},
	} {
		favs := refs(ChannelRef{ID: tc.id, Name: "Saved"})
		played, err := channelFavoriteEntry(tc.channels, favs()[0]).Play(t.Context())
		if played != nil || !tc.want(err) {
			t.Errorf("%s: plays %+v, %v", tc.name, played, err)
		}
		if got := titles(load(t, child(t, radio(&fakeProvider{name: "Radio"}, tc.channels, favs), "Favorites"))); !slices.Equal(got, []string{"Saved"}) {
			t.Errorf("%s: Favorites = %q, want the favorite kept", tc.name, got)
		}
	}
}

// Search's Stations lists a favorite channel whose saved name matches,
// ahead of the catalog's stations, unless the query rules out stations or
// radio. Search's word rules apply: a prefix, a whole single letter.
func TestSearchListsMatchingChannelFavoritesFirst(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	cat.found = catalog.SearchResults{catalog.SearchStation: {
		{Kind: catalog.SearchStation, Provider: catalog.Radio, Track: &catalog.Track{ID: 50, Ref: lref3(catalog.Radio, "https://wbgo"), Title: "WBGO", PlayableURI: "https://wbgo"}},
	}}
	search := child(t, Root(Sources{Radio: &directoryRadio{fakeProvider{name: "Radio"}}, Channels: snapshotChannels(t), Catalog: cat,
		ChannelFavorites: refs(ChannelRef{ID: "lofi", Name: "Lofi"}, ChannelRef{ID: "edm", Name: "EDM Mix"})}), "Search")
	stations := func(query string) []string {
		var got []string
		for _, e := range searchFor(t, search, query) {
			if e.Section == "Stations" {
				got = append(got, e.Title)
			}
		}
		return got
	}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"lo", []string{"Lofi", "WBGO"}},
		{"mix", []string{"EDM Mix", "WBGO"}},
		{"m", []string{"WBGO"}},
		{"edm mi", []string{"EDM Mix", "WBGO"}},
		{"edm m", []string{"WBGO"}},
		{"source:radio lofi", []string{"Lofi", "WBGO"}},
		{"source:spotify lofi", nil},
		{"type:album lofi", nil},
		{"artist:lofi", []string{"WBGO"}},
	} {
		if got := stations(tc.query); !slices.Equal(got, tc.want) {
			t.Errorf("%q Stations = %q, want %q", tc.query, got, tc.want)
		}
	}
	for _, e := range searchFor(t, search, "lofi") {
		if e.Title == "Lofi" && (e.Channel == nil || e.Channel.ID != "lofi" || e.Detail != channelMarker) {
			t.Errorf("searched channel = %+v", e)
		}
	}
}
