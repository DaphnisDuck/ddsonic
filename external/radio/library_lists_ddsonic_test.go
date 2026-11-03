package radio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/bjarneo/cliamp/provider"
)

// StationSelection is what the library shows above a country's or a tag's
// stations and a directory search: it must say what those queries ask the
// directory for. The library asks with the first sort type.
func TestStationSelectionDescribesTheQueries(t *testing.T) {
	var n int
	if _, err := fmt.Sscanf((&Provider{}).StationSelection(), "Top %d by votes", &n); err != nil {
		t.Fatalf("StationSelection() = %q: %v", (&Provider{}).StationSelection(), err)
	}
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("order")+" "+r.URL.Query().Get("limit"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]CatalogStation{})
	}))
	t.Cleanup(srv.Close)
	installCatalogClient(t, srv.URL)

	p := newTestProvider(t)
	first := radioSortTypes()[0].ID
	queries := map[string]func() error{
		"a country": func() error { _, err := p.GenreTracks(Place{Code: "NO"}.ID(), first); return err },
		"a region":  func() error { _, err := p.GenreTracks(Place{Code: "NO", State: "Oslo"}.ID(), first); return err },
		"a tag": func() error {
			_, err := p.GenreBrowserFor(browseTagsID).GenreTracks("jazz", p.GenreBrowserFor(browseTagsID).GenreSortTypes()[0].ID)
			return err
		},
		"a directory search": func() error { _, err := p.SearchStationTracks("jazz"); return err },
	}
	want := SortVotes + " " + strconv.Itoa(n)
	for name, query := range queries {
		mu.Lock()
		asked = nil
		mu.Unlock()
		if err := query(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		mu.Lock()
		if len(asked) != 1 || asked[0] != want {
			t.Errorf("%s asked the directory for %q, want one request for %q", name, asked, want)
		}
		mu.Unlock()
	}
}

// The library lists the tag browser's categories A–Z; the country browser
// keeps its own order.
func TestOnlyTheTagBrowserIsListedByName(t *testing.T) {
	p := newTestProvider(t)
	type named interface {
		GenreName(provider.GenreInfo) string
	}
	if _, ok := p.GenreBrowserFor(browseTagsID).(named); !ok {
		t.Error("the tag browser is not marked to list by name")
	}
	if _, ok := p.GenreBrowserFor(browseCountriesID).(named); ok {
		t.Error("the country browser is marked to list by name")
	}
}

// A tag's GenreName is its row's label without the count, from the tag
// itself: names with parentheses, punctuation or terminal escapes keep
// them as the label shows them, and the label is the name and the count.
func TestTagGenreNameIsTheLabelWithoutItsCount(t *testing.T) {
	d := &directory{tags: []Tag{
		{Name: "rock", StationCount: 90}, {Name: "rock (80s)", StationCount: 3},
		{Name: "\x1b[31mjazz (live)\x1b[0m", StationCount: 12}, {Name: "drum & bass (uk)", StationCount: 7},
		{Name: "(what?)", StationCount: 2},
	}}
	d.serve(t)
	p := newPlaceProvider(t, "")
	browser := p.GenreBrowserFor(browseTagsID)
	genres, err := browser.Genres()
	if err != nil {
		t.Fatalf("Genres: %v", err)
	}
	named := browser.(interface {
		GenreName(provider.GenreInfo) string
	})
	want := map[string]string{
		"rock": "rock", "rock (80s)": "rock (80s)", "\x1b[31mjazz (live)\x1b[0m": "jazz (live)",
		"drum & bass (uk)": "drum & bass (uk)", "(what?)": "(what?)",
	}
	counts := map[string]int{}
	for _, tag := range d.tags {
		counts[tag.Name] = tag.StationCount
	}
	if len(genres) != len(want) {
		t.Fatalf("genres = %+v, want %d tags", genres, len(want))
	}
	for _, g := range genres {
		name := named.GenreName(g)
		if name != want[g.ID] {
			t.Errorf("GenreName(%q) = %q, want %q", g.ID, name, want[g.ID])
		}
		if label := fmt.Sprintf("%s (%d)", name, counts[g.ID]); g.Name != label {
			t.Errorf("label %q, want the name and count %q", g.Name, label)
		}
	}
}
