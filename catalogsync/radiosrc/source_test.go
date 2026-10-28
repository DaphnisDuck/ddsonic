package radiosrc

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/playlist"
)

type fakeRadio struct{ favorites, local []playlist.Track }

func (f *fakeRadio) FavoriteTracks() []playlist.Track     { return f.favorites }
func (f *fakeRadio) LocalStationTracks() []playlist.Track { return f.local }

func station(url, name, tags string) playlist.Track {
	return playlist.Track{Path: url, Title: name, Genre: tags, Stream: true, Realtime: true}
}

func TestFetchMapsStations(t *testing.T) {
	src := New(&fakeRadio{
		favorites: []playlist.Track{station("https://wbgo/stream", "WBGO", "jazz,news"), {Title: "no url"}},
		local:     []playlist.Track{station("https://cliamp/radio", "cliamp radio", "")},
	})
	snap, err := src.Fetch(context.Background(), Favorites, catalogsync.Known{})
	if err != nil {
		t.Fatal(err)
	}
	want := catalog.TrackRecord{Ref: catalog.Ref{Provider: catalog.Radio, ProviderID: "https://wbgo/stream"},
		Title: "WBGO", Genre: "jazz,news", PlayableURI: "https://wbgo/stream"}
	if snap.Provider != catalog.Radio || snap.Collection != Favorites || len(snap.Tracks) != 1 || snap.Tracks[0].Ref != want.Ref ||
		snap.Tracks[0].Title != want.Title || snap.Tracks[0].Genre != want.Genre || snap.Tracks[0].PlayableURI != want.PlayableURI {
		t.Errorf("favorites = %+v, want one station %+v", snap, want)
	}
	if snap, err := src.Fetch(context.Background(), Custom, catalogsync.Known{}); err != nil || len(snap.Tracks) != 1 {
		t.Errorf("custom = %+v, %v", snap, err)
	}
	if _, err := src.Fetch(context.Background(), "nope", catalogsync.Known{}); err == nil {
		t.Error("unknown collection fetched")
	}
}

// Stations sync into the catalog, search finds them with favorites first,
// and an unstarred station leaves search.
func TestStationsSearchable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	radio := &fakeRadio{
		favorites: []playlist.Track{station("https://wbgo", "WBGO Newark", "jazz")},
		local:     []playlist.Track{station("https://jjj", "Jazz Jazz Jazz", "jazz"), station("https://cliamp", "cliamp radio", "")},
	}
	eng := catalogsync.New(store, nil, New(radio))
	stations := func(query string) []string {
		t.Helper()
		if err := eng.Sync(ctx, catalog.Radio); err != nil {
			t.Fatal(err)
		}
		res, err := store.Search(ctx, catalog.ParseQuery(query), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(res[catalog.SearchTrack]) != 0 {
			t.Errorf("stations listed as tracks: %+v", res[catalog.SearchTrack])
		}
		var names []string
		for _, r := range res[catalog.SearchStation] {
			names = append(names, r.Track.Title)
		}
		return names
	}
	if got := stations("jazz"); !slices.Equal(got, []string{"WBGO Newark", "Jazz Jazz Jazz"}) {
		t.Errorf("jazz = %q, want the favorite first", got)
	}
	if got := stations("cliamp"); !slices.Equal(got, []string{"cliamp radio"}) {
		t.Errorf("cliamp = %q", got)
	}
	radio.favorites = nil
	if got := stations("wbgo"); len(got) != 0 {
		t.Errorf("unstarred station still found: %q", got)
	}
}
