package library

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// st is a station track as the radio provider builds one: the name is the
// title, the stream is the path.
func st(name, url string) playlist.Track {
	return playlist.Track{Path: url, Title: name, Stream: true, Realtime: true}
}

func stationNames(tracks []playlist.Track) []string {
	out := make([]string, len(tracks))
	for i, t := range tracks {
		out[i] = t.Title + " <" + t.Path + ">"
	}
	return out
}

// The cases list stations in the order they must come out.
var stationOrderCases = []struct {
	name string
	want []playlist.Track
}{
	{"Latin, case-insensitive", []playlist.Track{
		st("Alpha", "https://1"), st("bbc Radio 3", "https://2"), st("Charlie FM", "https://3"), st("delta", "https://4"),
	}},
	{"accented Latin files with its base letter", []playlist.Track{
		st("Ångström FM", "https://1"), st("Eagle", "https://2"), st("écho", "https://3"), st("Échos du Nord", "https://4"),
		st("Øresund", "https://5"), st("Radio Zürich", "https://6"), st("Zeta", "https://7"),
	}},
	{"Cyrillic, case-insensitive", []playlist.Track{
		st("Авторадио", "https://1"), st("Европа Плюс", "https://2"), st("радио Рекорд", "https://3"), st("Радио Ретро", "https://4"),
	}},
	{"Greek, case- and accent-insensitive", []playlist.Track{
		st("Άλφα Radio", "https://1"), st("βήτα", "https://2"), st("Γάμμα FM", "https://3"), st("Ωμέγα", "https://4"),
	}},
	{"Japanese, by code point (hiragana, katakana, kanji)", []playlist.Track{
		st("あおぞらラジオ", "https://1"), st("エフエム東京", "https://2"), st("ラジオ日本", "https://3"), st("東京FM", "https://4"),
	}},
	{"mixed scripts: digits, Latin, Greek, Cyrillic, Arabic, CJK", []playlist.Track{
		st("1.FM", "https://1"), st("#Jazz", "https://2"), st("Zeta", "https://3"), st("Αλφα", "https://4"),
		st("Альфа", "https://5"), st("إذاعة القرآن", "https://6"), st("東京", "https://7"),
	}},
	{"names equal but for case: exact name, then stream", []playlist.Track{
		st("JAZZ FM", "https://a"), st("Jazz FM", "https://a"), st("Jazz FM", "https://b"), st("jazz fm", "https://a"),
	}},
	{"names equal but for accents: exact name decides", []playlist.Track{
		st("CAFÉ", "https://c"), st("Cafe", "https://b"), st("Café", "https://a"),
	}},
	{"Greek and Cyrillic names equal but for case (Greek code points come first)", []playlist.Track{
		st("ΡΑΔΙΟ", "https://2"), st("ράδιο", "https://2"), st("РАДИО", "https://1"), st("Радио", "https://1"), st("радио", "https://1"),
	}},
}

func TestSortedStations(t *testing.T) {
	for _, tc := range stationOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			in := slices.Clone(tc.want)
			slices.Reverse(in)
			if got := sortedStations(in); !slices.Equal(stationNames(got), stationNames(tc.want)) {
				t.Errorf("sortedStations = %q\nwant              %q", stationNames(got), stationNames(tc.want))
			}
		})
	}
}

// Whatever order the stations arrive in, they come out in the same order.
func TestSortedStationsIgnoresArrivalOrder(t *testing.T) {
	for _, tc := range stationOrderCases {
		for seed := range uint64(50) {
			in := slices.Clone(tc.want)
			r := rand.New(rand.NewPCG(seed, 27))
			r.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
			if got := sortedStations(in); !slices.Equal(stationNames(got), stationNames(tc.want)) {
				t.Fatalf("%s, seed %d: sortedStations = %q, want %q", tc.name, seed, stationNames(got), stationNames(tc.want))
			}
		}
	}
}

// Sorting moves stations and changes nothing in them: names keep their
// case, accents, punctuation and spacing.
func TestSortedStationsKeepsStationsAsTheyAre(t *testing.T) {
	in := []playlist.Track{
		st("  Radio  Ñandú ", "https://n"), st("«Эхо Москвы»", "https://e"), st("ΣΚΑΪ 100.3", "https://s"),
		{Path: "https://m", Title: "WBGO", Stream: true, ProviderMeta: map[string]string{"radio.name": "WBGO", "radio.country": "US"}},
	}
	want := slices.Clone(in)
	got := sortedStations(slices.Clone(in))
	for _, w := range want {
		i := slices.IndexFunc(got, func(g playlist.Track) bool { return reflect.DeepEqual(g, w) })
		if i < 0 {
			t.Errorf("station %q is missing or changed after sorting: %+v", w.Title, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("sorted %d stations, want %d", len(got), len(want))
	}
}

// Two rows of one station (same name and stream; the directory can list a
// station twice) look and play the same; they keep their arrival order.
func TestSortedStationsKeepsDuplicatesInArrivalOrder(t *testing.T) {
	first := playlist.Track{Path: "https://x", Title: "Echo", ProviderMeta: map[string]string{"radio.country": "DE"}}
	second := playlist.Track{Path: "https://x", Title: "Echo", ProviderMeta: map[string]string{"radio.country": "AT"}}
	got := sortedStations([]playlist.Track{st("Zulu", "https://z"), first, second})
	if got[0].Meta("radio.country") != "DE" || got[1].Meta("radio.country") != "AT" || got[2].Title != "Zulu" {
		t.Errorf("sortedStations = %+v, want Echo (DE), Echo (AT), Zulu", got)
	}
}

// — The lists that show stations —

// shuffledRadio is a radio provider whose directory and favorites return
// stations in no particular order.
type shuffledRadio struct {
	fakeProvider
	stations []playlist.Track
	channels []playlist.PlaylistInfo
}

var unsortedStations = []playlist.Track{
	st("WBGO Jazz 88.3", "https://w"), st("Échos", "https://e"), st("bbc Radio 3", "https://b"),
	st("Радио Ретро", "https://r"), st("Alpha", "https://a2"), st("ALPHA", "https://a1"),
}

var stationsAZ = []string{
	"ALPHA <https://a1>", "Alpha <https://a2>", "bbc Radio 3 <https://b>", "Échos <https://e>",
	"WBGO Jazz 88.3 <https://w>", "Радио Ретро <https://r>",
}

func (r *shuffledRadio) FavoriteTracks() []playlist.Track { return slices.Clone(r.stations) }
func (r *shuffledRadio) SearchStationTracks(string) ([]playlist.Track, error) {
	return slices.Clone(r.stations), nil
}
func (*shuffledRadio) StationSelection() string { return "Top 200 by votes" }
func (r *shuffledRadio) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{ID: "Countries", Name: "Browse all countries", Mode: provider.BrowseGenres},
		{ID: "Genres & Tags", Name: "Browse tags", Mode: provider.BrowseGenres},
	}
}
func (r *shuffledRadio) GenreBrowserFor(id string) provider.GenreBrowser {
	if id == "Genres & Tags" {
		return &shuffledGenres{r, id, tagsByCount}
	}
	return &shuffledGenres{r, id, unsortedCountries}
}

// shuffledGenres is a category browser. A category's stations are the
// radio's for "jazz", and one station named after the category otherwise,
// so a row shows which category it opened.
type shuffledGenres struct {
	r      *shuffledRadio
	name   string
	genres []provider.GenreInfo
}

func (g *shuffledGenres) GenreLabel() string                    { return g.name }
func (g *shuffledGenres) Genres() ([]provider.GenreInfo, error) { return g.genres, nil }
func (*shuffledGenres) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "votes"}}
}
func (g *shuffledGenres) GenreTracks(id, _ string) ([]playlist.Track, error) {
	if id == "jazz" {
		return slices.Clone(g.r.stations), nil
	}
	return []playlist.Track{st("in "+id, "https://"+id)}, nil
}

// Countries arrive grouped by region, most stations first; tags most
// stations first, as the directory gives them.
var (
	unsortedCountries = []provider.GenreInfo{
		{ID: "jazz", Name: "Sweden", Group: "Europe"}, {ID: "AR", Name: "Argentina", Group: "Americas"},
		{ID: "NO", Name: "Norway", Group: "Europe", Favorite: true},
	}
	// Most stations first, and far from A–Z.
	tagsByCount = []provider.GenreInfo{
		{ID: "rock", Name: "rock (900)"}, {ID: "pop", Name: "pop (800)"}, {ID: "jazz", Name: "jazz (100)"},
		{ID: "ambient", Name: "ambient (50)"}, {ID: "électro", Name: "électro (12)"}, {ID: "80s", Name: "80s (3)"},
	}
)

func trackRows(t *testing.T, entries []Entry) []string {
	t.Helper()
	var out []string
	for _, e := range entries {
		if e.Track == nil {
			t.Fatalf("row %q is not a station", e.Title)
		}
		if e.Title != e.Track.Title {
			t.Errorf("row shows %q for the station %q", e.Title, e.Track.Title)
		}
		out = append(out, e.Track.Title+" <"+e.Track.Path+">")
	}
	return out
}

func directorySearch(t *testing.T, prov playlist.Provider) Level {
	t.Helper()
	for _, e := range (&catalogView{radioProv: prov}).beyond(catalog.ParseQuery("jazz")) {
		if e.Open != nil {
			return e.Open
		}
	}
	t.Fatal("no \"Search the radio directory\" row")
	return nil
}

func noteOf(l Level) []string {
	if nl, ok := l.(NotedLevel); ok {
		return nl.Note()
	}
	return nil
}

// Favorites, a country's and a tag's stations, and the radio directory's
// search results all list stations A–Z, whatever order the provider gave.
// The directory's lists say which of its stations they are; Favorites,
// all of them, says nothing.
func TestStationListsSortAZ(t *testing.T) {
	prov := &shuffledRadio{fakeProvider: fakeProvider{name: "Radio"}, stations: unsortedStations}
	r := radio(prov, nil)
	browse := child(t, r, "Browse Stations")
	selected := []string{"Top 200 by votes, listed A–Z."}
	lists := []struct {
		name string
		l    Level
		note []string
	}{
		{"Favorites", child(t, r, "Favorites"), nil},
		{"a country's stations", child(t, child(t, browse, "Countries"), "Sweden"), selected},
		{"a tag's stations", child(t, child(t, browse, "Genres & Tags"), "jazz (100)"), selected},
		{"radio directory's search", directorySearch(t, prov), selected},
	}
	for _, tc := range lists {
		entries := load(t, tc.l)
		if got := trackRows(t, entries); !slices.Equal(got, stationsAZ) {
			t.Errorf("%s = %q\nwant %q", tc.name, got, stationsAZ)
		}
		if got := noteOf(tc.l); !slices.Equal(got, tc.note) {
			t.Errorf("%s note = %q, want %q", tc.name, got, tc.note)
		}
		// Playing from the list queues it as shown.
		if queued, at := Tracks(entries, 2); !slices.Equal(stationNames(queued), stationsAZ) || at != 2 {
			t.Errorf("%s plays %q from %d, want the list as shown from 2", tc.name, stationNames(queued), at)
		}
	}
	if !slices.Equal(stationNames(prov.stations), stationNames(unsortedStations)) {
		t.Error("listing sorted the provider's own stations")
	}
}

// A provider that does not say what its directory lists hold gets no note.
func TestStationListWithoutSelectionHasNoNote(t *testing.T) {
	l := directorySearch(t, &directoryRadio{fakeProvider{name: "Radio"}})
	if note := noteOf(l); note != nil {
		t.Errorf("note = %q, want none", note)
	}
	if rows := load(t, l); len(rows) != 1 || rows[0].Track == nil {
		t.Errorf("directory search = %+v, want its one station", rows)
	}
}

// Genres & Tags keeps the browser's order, most stations first: the
// directory has thousands of small tags, and A–Z would put them ahead of
// the genres people look for. Labels are as the browser gave them, and each
// row opens its own tag, its stations A–Z. Countries keep the browser's
// order too: grouped by region, the provider's order within one.
func TestRadioCategories(t *testing.T) {
	prov := &shuffledRadio{fakeProvider: fakeProvider{name: "Radio"}, stations: unsortedStations}
	browse := child(t, radio(prov, nil), "Browse Stations")

	tags := load(t, child(t, browse, "Genres & Tags"))
	want := titles(sliceEntries(tagsByCount))
	if got := titles(tags); !slices.Equal(got, want) {
		t.Errorf("tags = %q\nwant the browser's order %q", got, want)
	}
	idOf := map[string]string{}
	for _, g := range tagsByCount {
		idOf[g.Name] = g.ID
	}
	for _, e := range tags {
		id := idOf[e.Title]
		if id == "" || e.Open.Title() != e.Title {
			t.Errorf("tag row %q opens %q", e.Title, e.Open.Title())
			continue
		}
		wantRows := []string{"in " + id + " <https://" + id + ">"}
		if id == "jazz" {
			wantRows = stationsAZ
		}
		if got := trackRows(t, load(t, e.Open)); !slices.Equal(got, wantRows) {
			t.Errorf("tag row %q opens %q, want tag %q", e.Title, got, id)
		}
	}
	if !slices.Equal(titles(load(t, child(t, browse, "Genres & Tags"))), titles(tags)) {
		t.Error("tags reopen in another order")
	}

	countries := load(t, child(t, browse, "Countries"))
	if got, want := titles(countries), []string{"Sweden", "Norway", "Argentina"}; !slices.Equal(got, want) {
		t.Errorf("countries = %q, want the browser's order by region %q", got, want)
	}
	if !countries[1].Favorite || countries[1].Section != "Europe" {
		t.Errorf("Norway = %+v, want a pinned place in Europe", countries[1])
	}
	if !slices.Equal(want, []string{"rock (900)", "pop (800)", "jazz (100)", "ambient (50)", "électro (12)", "80s (3)"}) {
		t.Errorf("the fixture changed: %q", want)
	}
}

func sliceEntries(genres []provider.GenreInfo) []Entry {
	out := make([]Entry, len(genres))
	for i, g := range genres {
		out[i] = Entry{Title: g.Name}
	}
	return out
}

// The cliamp radio channels list A–Z by the name each row shows, " · live"
// and all, and each row plays its own channel. The channel list itself is
// left as the provider holds it.
func TestCliampRadioChannelsSortAZ(t *testing.T) {
	lists := []playlist.PlaylistInfo{
		{ID: "z", Name: "Zen"}, {ID: "a", Name: "Ambient · live"}, {ID: "m", Name: "Mixed", TrackCount: 40},
		{ID: "c", Name: "ambient"}, {ID: "b", Name: "Ambient · live"},
	}
	channels := &fakeProvider{name: "cliamp radio", lists: slices.Clone(lists), tracks: map[string][]playlist.Track{}}
	for _, l := range lists {
		channels.tracks[l.ID] = []playlist.Track{st(l.Name, "https://"+l.ID)}
	}
	rows := load(t, child(t, child(t, radio(&fakeRadio{fakeProvider{name: "Radio"}}, channels), "Browse Stations"), "cliamp radio"))
	if got, want := titles(rows), []string{"ambient", "Ambient · live", "Ambient · live", "Mixed", "Zen"}; !slices.Equal(got, want) {
		t.Fatalf("channels = %q, want %q", got, want)
	}
	for i, wantID := range []string{"c", "a", "b", "m", "z"} {
		played, err := rows[i].Play(t.Context())
		if err != nil || len(played) != 1 || played[0].Path != "https://"+wantID {
			t.Errorf("row %d (%q) plays %+v, %v; want channel %q", i, rows[i].Title, played, err, wantID)
		}
	}
	if noteOf(child(t, child(t, radio(&fakeRadio{fakeProvider{name: "Radio"}}, channels), "Browse Stations"), "cliamp radio")) != nil {
		t.Error("the channels have a note")
	}
	if !slices.EqualFunc(channels.lists, lists, func(a, b playlist.PlaylistInfo) bool { return a == b }) {
		t.Error("listing sorted the provider's own channel list")
	}
}
