package library

import (
	"context"

	"github.com/bjarneo/cliamp/playlist"
)

// liveChannels is a cliamp radio channel list that can say which of its
// channels are live stations (radio.ChannelProvider).
type liveChannels interface {
	LiveStationTrack(id string) (playlist.Track, bool)
}

// channelEntry makes the row of a cliamp radio channel under Browse
// Stations. Enter plays that channel alone, as it always has. A live
// channel's row also holds its stream as a station, so f stars it and
// Favorites can play it again (#135). A song channel's row holds none: it
// opens as its songs, which no station stands for, so f leaves it alone.
func channelEntry(channels playlist.Provider) func(playlist.PlaylistInfo) Entry {
	live, _ := channels.(liveChannels)
	return func(info playlist.PlaylistInfo) Entry {
		if live != nil {
			if track, ok := live.LiveStationTrack(info.ID); ok {
				return Entry{Title: info.Name, Track: &track, PlayFrom: func(context.Context) ([]playlist.Track, int, error) {
					tracks, err := channels.Tracks(info.ID)
					return tracks, 0, err
				}}
			}
		}
		return Entry{Title: info.Name, Play: func(context.Context) ([]playlist.Track, error) {
			return channels.Tracks(info.ID)
		}}
	}
}
