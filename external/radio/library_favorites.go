// ddsonic: favorite and local stations for the library navigation and the
// catalog. Kept in its own file so upstream merges of provider.go stay
// conflict-free.

package radio

import (
	"sync"

	"github.com/bjarneo/cliamp/playlist"
)

// FavoriteTracks returns the favorite stations as playable tracks, in saved
// order. Unlike Playlists it is unaffected by an active catalog search.
func (p *Provider) FavoriteTracks() []playlist.Track {
	stations := p.favorites.Stations()
	tracks := make([]playlist.Track, len(stations))
	for i, s := range stations {
		tracks[i] = stationTrack(s)
	}
	return tracks
}

// LocalStationTracks returns the built-in station and those from
// radios.toml as playable tracks, in file order.
func (p *Provider) LocalStationTracks() []playlist.Track {
	p.mu.Lock()
	defer p.mu.Unlock()
	tracks := make([]playlist.Track, len(p.stations))
	for i, s := range p.stations {
		tracks[i] = playlist.Track{Path: s.url, Title: s.name, Stream: true, Realtime: true}
	}
	return tracks
}

// The locks of a favorite toggle, in the order they are taken. A later one
// is never held while an earlier one is taken:
//
//	Favorites.mutate  one toggle at a time in this process; held across the
//	                  favorites file's lock and the disk
//	the file's lock   one toggle at a time across processes
//	                  (radio_favorites.toml.lock)
//	Favorites.mu      what readers take (Contains, Stations, Count, Revision,
//	                  drawing among them); a toggle takes it twice, briefly:
//	                  to read whether the station is starred, and to publish
//	                  the saved result. Never held across the file's lock or
//	                  the disk, so a reader never waits for a save.
//
// Provider.mu is not one of them. A toggle through the provider
// (ToggleFavorite) holds it only to find the station, and releases it
// before the save: the UI takes it to list the pane's rows (Playlists),
// also when a track starts, and must not wait for the favorites file. The
// provider's methods read the store under Provider.mu (Provider.mu, then
// Favorites.mu); the store calls nothing of the provider's, so Provider.mu
// is never taken under a store lock. The observer runs after every one of
// these is released.

// Toggle stars s, or unstars it (toggle, in favorites.go), then tells the
// observer. It may wait for another ddsonic's save and for the disk: call
// it off the UI goroutine.
func (f *Favorites) Toggle(s CatalogStation) (bool, error) {
	favorite, err := f.toggle(s)
	if err == nil {
		f.toggled()
	}
	return favorite, err
}

// favoriteObservers holds each favorites store's OnFavoritesToggled
// callback, so the upstream Favorites struct needs no new field for it.
var favoriteObservers sync.Map // *Favorites → func()

// OnFavoritesToggled registers fn to run after every saved toggle of the
// provider's favorites, whichever way it was made: through the provider
// (ToggleFavorite) or through the store (the library's f, cliamp's queue
// star). It is how the catalogued favorites are resynced. fn runs on the
// toggling goroutine with no lock held, so it may read the store and the
// provider; it must not block.
func (p *Provider) OnFavoritesToggled(fn func()) { favoriteObservers.Store(p.favorites, fn) }

// toggled runs the store's observer. Call it with no lock held.
func (f *Favorites) toggled() {
	if fn, ok := favoriteObservers.Load(f); ok {
		fn.(func())()
	}
}

// SearchStationTracks searches the Radio Browser directory by station name
// and returns the playable stations, most voted first. Unlike SearchCatalog
// it leaves the provider's own search results alone.
func (p *Provider) SearchStationTracks(query string) ([]playlist.Track, error) {
	stations, err := Stations(StationQuery{Name: query, Order: SortVotes, Limit: searchLimit})
	if err != nil {
		return nil, err
	}
	return stationTracks(streamableStations(stations)), nil
}
