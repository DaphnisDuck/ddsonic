// ddsonic: tests for the library and catalog station helpers.

package radio

import "testing"

func TestLocalStationTracksAndFavoritesObserver(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New(Options{Favorites: &Favorites{byURL: map[string]struct{}{}}})
	local := p.LocalStationTracks()
	if len(local) == 0 || local[0].Path != BuiltinURL || !local[0].Stream {
		t.Fatalf("local stations = %+v, want the built-in station first", local)
	}

	toggled := 0
	p.OnFavoritesToggled(func() { toggled++ })
	p.AppendCatalog([]CatalogStation{{Name: "WBGO", URL: "https://wbgo"}})
	if _, _, err := p.ToggleFavorite("c:0"); err != nil {
		t.Fatal(err)
	}
	if toggled != 1 {
		t.Errorf("observer calls = %d, want 1", toggled)
	}
	if favs := p.FavoriteTracks(); len(favs) != 1 || favs[0].Path != "https://wbgo" {
		t.Errorf("favorites = %+v", favs)
	}
}

// A station starred through the store itself, as the library's f and
// cliamp's queue star do it, reaches the observer as one starred through
// the provider does: the catalogued favorites follow every toggle.
func TestFavoritesStoreToggleReachesTheObserver(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	favs := LoadFavorites()
	p := New(Options{Favorites: favs})
	toggled := 0
	p.OnFavoritesToggled(func() { toggled++ })
	station := CatalogStation{Name: "WBGO", URL: "https://wbgo"}
	for want := 1; want <= 2; want++ {
		if _, err := favs.Toggle(station); err != nil {
			t.Fatal(err)
		}
		if toggled != want {
			t.Fatalf("observer calls after %d toggles = %d", want, toggled)
		}
	}

	// Another provider's store is not this one's.
	other := LoadFavorites()
	New(Options{Favorites: other})
	if _, err := other.Toggle(station); err != nil {
		t.Fatal(err)
	}
	if toggled != 2 {
		t.Errorf("observer calls after another store's toggle = %d, want 2", toggled)
	}
}

// What plays but is not a directory station gives no station to star.
func TestOnlyDirectoryStationsAreStations(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := New(Options{Favorites: LoadFavorites()})
	for _, track := range p.LocalStationTracks() {
		if s, ok := StationFromTrack(track); ok {
			t.Errorf("radios.toml station %q is the directory station %+v", track.Title, s)
		}
	}
	p.AppendCatalog([]CatalogStation{{Name: "WBGO", URL: "https://wbgo"}})
	tracks, err := p.Tracks("c:0")
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := StationFromTrack(tracks[0]); !ok || s.Name != "WBGO" || s.URL != "https://wbgo" {
		t.Errorf("directory station = %+v, %v", s, ok)
	}
}
