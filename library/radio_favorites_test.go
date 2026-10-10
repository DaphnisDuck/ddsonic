package library

import (
	"maps"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// savedRadio is a radio provider holding the given starred stations.
type savedRadio struct {
	directoryRadio
	starred []playlist.Track
}

func (r *savedRadio) FavoriteTracks() []playlist.Track { return slices.Clone(r.starred) }

// The Favorites list says it lists the starred stations, so the UI rereads
// it when one is starred or unstarred; no other Radio level does.
func TestRadioFavoritesLevelIsMarked(t *testing.T) {
	prov := &savedRadio{directoryRadio: directoryRadio{fakeProvider{name: "Radio"}},
		starred: []playlist.Track{{Path: "https://b", Title: "Beta"}, {Path: "https://a", Title: "Alpha"}}}
	radio := child(t, Root(Sources{Radio: prov}), "Radio")
	favorites := child(t, radio, "Favorites")
	if _, ok := favorites.(StarredStationsLevel); !ok {
		t.Errorf("Favorites is %T, want a StarredStationsLevel", favorites)
	}
	if got := titles(load(t, favorites)); !slices.Equal(got, []string{"Alpha", "Beta"}) {
		t.Errorf("Favorites = %v, want the starred stations by name", got)
	}
	for _, l := range []Level{radio, child(t, radio, "Browse Stations")} {
		if _, ok := l.(StarredStationsLevel); ok {
			t.Errorf("%s is a StarredStationsLevel", l.Title())
		}
	}
}

// The catalog holds a station's name and stream alone. A searched station
// that is starred carries the station as the favorites hold it, so it can
// be unstarred from the results and from the queue it plays into; one that
// is not starred (radios.toml's) carries none, and is no directory station.
func TestSearchedStarredStationCarriesItsStation(t *testing.T) {
	cat, _, _ := newCatalogFixture()
	cat.found = catalog.SearchResults{catalog.SearchStation: {
		{Kind: catalog.SearchStation, Provider: catalog.Radio, Track: &catalog.Track{ID: 50, Ref: lref3(catalog.Radio, "https://wbgo"), Title: "WBGO", PlayableURI: "https://wbgo"}},
		{Kind: catalog.SearchStation, Provider: catalog.Radio, Track: &catalog.Track{ID: 51, Ref: lref3(catalog.Radio, "https://mine"), Title: "Mine", PlayableURI: "https://mine"}},
	}}
	meta := map[string]string{"radio.name": "WBGO Jazz 88.3", "radio.url": "https://wbgo", "radio.country": "United States"}
	prov := &savedRadio{directoryRadio: directoryRadio{fakeProvider{name: "Radio"}},
		starred: []playlist.Track{{Path: "https://other", Title: "Other"}, {Path: "https://wbgo", Title: "WBGO Jazz 88.3", ProviderMeta: meta}}}
	search := child(t, Root(Sources{Radio: prov, Catalog: cat}), "Search")

	rows := map[string]Entry{}
	for _, e := range searchFor(t, search, "o") {
		rows[e.Title] = e
	}
	wbgo, mine := rows["WBGO"], rows["Mine"]
	if wbgo.Track == nil || mine.Track == nil {
		t.Fatalf("rows = %v, want both stations", slices.Collect(maps.Keys(rows)))
	}
	if !maps.Equal(wbgo.Track.ProviderMeta, meta) || wbgo.Track.Path != "https://wbgo" || !wbgo.Track.Realtime {
		t.Errorf("starred station's track = %+v, want the favorite's station on the catalog's stream", *wbgo.Track)
	}
	played, _, err := wbgo.PlayFrom(t.Context())
	if err != nil || len(played) != 1 || !maps.Equal(played[0].ProviderMeta, meta) {
		t.Errorf("starred station plays %+v, %v; want the same station", played, err)
	}
	if len(mine.Track.ProviderMeta) != 0 {
		t.Errorf("unstarred station's track carries %v, want nothing", mine.Track.ProviderMeta)
	}
}
