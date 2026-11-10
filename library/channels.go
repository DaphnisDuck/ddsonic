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
// Stations. Enter plays that channel alone (Play), as it always has. A live
// channel's row also names its stream as a station for f (FavoriteTrack),
// so f stars it and Favorites can play it again (#135); that changes
// nothing about how the row plays. A song channel's row names none: it
// opens as its songs, which no station stands for, so f leaves it alone.
func channelEntry(channels playlist.Provider) func(playlist.PlaylistInfo) Entry {
	live, _ := channels.(liveChannels)
	return func(info playlist.PlaylistInfo) Entry {
		e := Entry{Title: info.Name, Play: func(context.Context) ([]playlist.Track, error) {
			return channels.Tracks(info.ID)
		}}
		if live != nil {
			if track, ok := live.LiveStationTrack(info.ID); ok {
				e.FavoriteTrack = &track
			}
		}
		return e
	}
}
