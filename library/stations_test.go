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
func (r *shuffledRadio) BrowseEntries() []provider.BrowseEntry {
	return []provider.BrowseEntry{
		{ID: "Countries", Name: "Browse all countries", Mode: provider.BrowseGenres},
		{ID: "Genres & Tags", Name: "Browse tags", Mode: provider.BrowseGenres},
	}
}
func (r *shuffledRadio) GenreBrowserFor(id string) provider.GenreBrowser {
	return &shuffledGenres{r, id}
}

type shuffledGenres struct {
	r    *shuffledRadio
	name string
}

func (g *shuffledGenres) GenreLabel() string { return g.name }
func (g *shuffledGenres) Genres() ([]provider.GenreInfo, error) {
	return []provider.GenreInfo{{ID: "jazz", Name: "jazz (6)"}}, nil
}
func (*shuffledGenres) GenreSortTypes() []provider.SortType {
	return []provider.SortType{{ID: "votes"}}
}
func (g *shuffledGenres) GenreTracks(string, string) ([]playlist.Track, error) {
	return slices.Clone(g.r.stations), nil
}

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

// Favorites, a country's and a tag's stations, and the radio directory's
// search results all list stations A–Z, whatever order the provider gave.
func TestStationListsSortAZ(t *testing.T) {
	prov := &shuffledRadio{fakeProvider: fakeProvider{name: "Radio"}, stations: unsortedStations}
	r := radio(prov, nil)
	browse := child(t, r, "Browse Stations")
	search := &catalogView{radioProv: prov}
	var directory Level
	for _, e := range search.beyond(catalog.ParseQuery("jazz")) {
		if e.Open != nil {
			directory = e.Open
		}
	}
	if directory == nil {
		t.Fatal("no \"Search the radio directory\" row")
	}
	lists := map[string]Level{
		"Favorites":                child(t, r, "Favorites"),
		"a country's stations":     child(t, child(t, browse, "Countries"), "jazz (6)"),
		"a tag's stations":         child(t, child(t, browse, "Genres & Tags"), "jazz (6)"),
		"radio directory's search": directory,
	}
	for name, l := range lists {
		if got := trackRows(t, load(t, l)); !slices.Equal(got, stationsAZ) {
			t.Errorf("%s = %q\nwant %q", name, got, stationsAZ)
		}
	}
	if !slices.Equal(stationNames(prov.stations), stationNames(unsortedStations)) {
		t.Error("listing sorted the provider's own stations")
	}
}

// The cliamp radio channels keep the order the channel list gives: it is
// cliamp radio's own order, and the startup queue plays them in it.
func TestCliampRadioChannelsKeepTheirOrder(t *testing.T) {
	channels := &fakeProvider{name: "cliamp radio", lists: []playlist.PlaylistInfo{
		{ID: "z", Name: "Zen"}, {ID: "a", Name: "Ambient · live"}, {ID: "m", Name: "Mixed"},
	}}
	rows := load(t, child(t, child(t, radio(&fakeRadio{fakeProvider{name: "Radio"}}, channels), "Browse Stations"), "cliamp radio"))
	if got := titles(rows); !slices.Equal(got, []string{"Zen", "Ambient · live", "Mixed"}) {
		t.Errorf("channels = %v, want the channel list's own order", got)
	}
}
