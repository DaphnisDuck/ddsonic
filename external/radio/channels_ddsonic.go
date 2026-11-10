// ddsonic: a live cliamp radio channel is a station the favorites can hold
// (#135). Its name and stream URL are all a favorite keeps
// (radio_favorites.toml, keyed by URL), and all Favorites needs to play it
// again. A song channel stays out: it opens as a list of its songs, which a
// stream favorite would not. Kept in its own file so upstream merges of
// channels.go stay small.

package radio

import "github.com/bjarneo/cliamp/playlist"

// liveTrack is a live channel's stream as a station track: the track the
// channel plays, carrying the station identity StationFromTrack reads.
func (c Channel) liveTrack() playlist.Track {
	return stationTrack(CatalogStation{Name: c.Name, URL: c.Stream})
}

// LiveStationTrack is the station track of the live channel with the given
// slug, from the channel list already loaded. ok is false for a song
// channel, an unknown slug, and while no list is loaded: it never
// downloads, so it is safe on the UI thread.
func (p *ChannelProvider) LiveStationTrack(id string) (track playlist.Track, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.channels {
		if c.ID == id {
			if c.hasSongs() {
				return track, false
			}
			return c.liveTrack(), true
		}
	}
	return track, false
}

// NewChannelsFrom is a provider over a channel list already in hand, used
// as given: it downloads the list only after Refresh. The library's tests
// build Radio > Browse Stations > cliamp radio from it.
func NewChannelsFrom(channels []Channel) *ChannelProvider {
	return &ChannelProvider{channels: channels}
}
