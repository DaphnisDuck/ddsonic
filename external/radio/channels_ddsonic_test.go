package radio

// ddsonic: a live cliamp radio channel is a station the favorites can hold;
// a song channel is not (#135).

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

func TestLiveChannelIsAStation(t *testing.T) {
	srv, _ := channelServer(t)
	installCatalogClient(t, srv.URL)
	p := NewChannels()

	// Before the list has loaded, nothing is known and nothing downloads.
	if _, ok := p.LiveStationTrack("lofi"); ok {
		t.Fatal("LiveStationTrack answered before the channel list loaded")
	}
	if _, err := p.Playlists(); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	lofi := CatalogStation{Name: "Lofi", URL: "https://radio.cliamp.stream/lofi/stream"}

	tests := []struct {
		id   string
		want bool
	}{
		{"lofi", true},
		{"omarchy", false}, // a song channel
		{"missing", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			track, ok := p.LiveStationTrack(tt.id)
			if ok != tt.want {
				t.Fatalf("LiveStationTrack(%q) ok = %v, want %v", tt.id, ok, tt.want)
			}
			if !ok {
				return
			}
			if station, isStation := StationFromTrack(track); !isStation || station != lofi {
				t.Errorf("station = %+v, %v; want %+v", station, isStation, lofi)
			}
		})
	}

	// What Enter queues is the same station, so the queue's f and the
	// Favorites list agree on it.
	queued, err := p.Tracks("lofi")
	if err != nil || len(queued) != 1 {
		t.Fatalf("Tracks(lofi) = %+v, %v", queued, err)
	}
	if station, ok := StationFromTrack(queued[0]); !ok || station != lofi {
		t.Errorf("queued live channel: station %+v, %v; want %+v", station, ok, lofi)
	}
	if q := queued[0]; q.Path != lofi.URL || q.Title != "Lofi" || !q.Stream || !q.Realtime {
		t.Errorf("queued live channel = %+v, want the Lofi stream, live", q)
	}

	// A song channel's songs are files, never stations.
	songs, err := p.Tracks("omarchy")
	if err != nil || len(songs) == 0 {
		t.Fatalf("Tracks(omarchy) = %+v, %v", songs, err)
	}
	for _, s := range songs {
		if _, ok := StationFromTrack(s); ok {
			t.Errorf("song %q is a station", s.Title)
		}
	}
}

// A favorite starred from a live channel plays the channel's stream again,
// from Favorites, across a restart (a real save and load).
func TestLiveChannelFavoriteRoundTrip(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	p := NewChannelsFrom([]Channel{{ID: "lofi", Name: "Lofi", Stream: "https://radio.cliamp.stream/lofi/stream"}})
	track, ok := p.LiveStationTrack("lofi")
	if !ok {
		t.Fatal("no live station for lofi")
	}
	station, _ := StationFromTrack(track)
	if added, err := LoadFavorites().Toggle(station); err != nil || !added {
		t.Fatalf("Toggle = %v, %v; want added", added, err)
	}

	again := New(Options{Favorites: LoadFavorites(), Country: CountryDeclined})
	played := again.FavoriteTracks()
	if len(played) != 1 {
		t.Fatalf("favorites after a reload = %+v, want the channel", played)
	}
	want := playlist.Track{Path: track.Path, Title: "Lofi", Stream: true, Realtime: true}
	if got := played[0]; got.Path != want.Path || got.Title != want.Title || !got.Stream || !got.Realtime {
		t.Errorf("Favorites plays %+v, want %+v", got, want)
	}
	if s, ok := StationFromTrack(played[0]); !ok || s != station {
		t.Errorf("replayed favorite: station %+v, %v; want %+v", s, ok, station)
	}
}
