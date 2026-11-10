package library

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// fakeLiveChannels says which of its channels are live stations, as the
// cliamp radio channel list does.
type fakeLiveChannels struct {
	fakeProvider
	live map[string]playlist.Track
}

func (f *fakeLiveChannels) LiveStationTrack(id string) (playlist.Track, bool) {
	t, ok := f.live[id]
	return t, ok
}

// A live channel's row holds its station, for f; a song channel's row holds
// none. Either way Enter plays that channel alone (#135).
func TestCliampRadioChannelRows(t *testing.T) {
	groove := st("Groove", "https://groove")
	channels := &fakeLiveChannels{
		fakeProvider: fakeProvider{name: "cliamp radio",
			lists: []playlist.PlaylistInfo{{ID: "g", Name: "Groove · live"}, {ID: "m", Name: "Mixtape", TrackCount: 2}},
			tracks: map[string][]playlist.Track{
				"g": {groove},
				"m": {{Path: "https://m/1", Stream: true}, {Path: "https://m/2", Stream: true}},
			}},
		live: map[string]playlist.Track{"g": groove},
	}
	rows := load(t, child(t, child(t, radio(&fakeRadio{fakeProvider{name: "Radio"}}, channels), "Browse Stations"), "cliamp radio"))
	if got := titles(rows); len(got) != 2 || got[0] != "Groove · live" || got[1] != "Mixtape" {
		t.Fatalf("channels = %q", got)
	}

	live := rows[0]
	if live.Track == nil || live.Track.Path != groove.Path || live.PlayFrom == nil || live.Open != nil {
		t.Fatalf("live channel row = %+v, want its station and a play of the channel", live)
	}
	played, at, err := live.PlayFrom(t.Context())
	if err != nil || at != 0 || len(played) != 1 || played[0].Path != groove.Path {
		t.Errorf("live row plays %+v from %d, %v; want the channel alone", played, at, err)
	}

	songs := rows[1]
	if songs.Track != nil || songs.Play == nil {
		t.Fatalf("song channel row = %+v, want a play-only row with no station", songs)
	}
	if played, err := songs.Play(t.Context()); err != nil || len(played) != 2 {
		t.Errorf("song row plays %+v, %v; want its songs", played, err)
	}
}
