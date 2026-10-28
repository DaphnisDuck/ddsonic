package model

// ddsonic: cliamp's own provider search ("Search Spotify for …") plays as the
// library does: Enter replaces the queue; a and q still append and queue
// next; Stop drops an album still loading.

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// spotSearchModel is a library model with Q1, Q2 queued and Q1 playing,
// showing the search overlay's results.
func spotSearchModel(results ...playlist.Track) Model {
	m := newLibraryModel(library.Menu("Music"))
	m.replacePlaylist(tracksOf("Q1", "Q2"))
	m.playlist.SetIndex(0)
	m.spotSearch = spotSearchState{visible: true, screen: spotSearchResults, results: results}
	return m
}

func TestSpotSearchEnterOnTrackReplacesQueue(t *testing.T) {
	m := spotSearchModel(playlist.Track{Path: "T", Title: "T"})
	m.handleSpotSearchResultsKey(libKey("enter"))
	if got := queuePaths(m); !slices.Equal(got, []string{"T"}) {
		t.Errorf("queue = %v, want [T]", got)
	}
	if m.playlist.Index() != 0 || m.spotSearch.visible {
		t.Errorf("index %d, overlay visible %v; want 0 and closed", m.playlist.Index(), m.spotSearch.visible)
	}
}

func TestSpotSearchAlbumActions(t *testing.T) {
	tests := []struct {
		action spotAlbumAction
		want   []string
	}{
		{spotAlbumPlay, []string{"A1", "A2"}},
		{spotAlbumAppend, []string{"Q1", "Q2", "A1", "A2"}},
		{spotAlbumQueueNext, []string{"Q1", "Q2", "A1", "A2"}},
	}
	for _, tt := range tests {
		m := spotSearchModel()
		const gen = 3
		m.requests.spotAlbum = gen
		m.spotSearch.albumLoading = true
		updated, _ := m.Update(spotAlbumTracksMsg{gen: gen, action: tt.action, album: albumResult("A"), tracks: tracksOf("A1", "A2")})
		m = updated.(Model)
		if got := queuePaths(m); !slices.Equal(got, tt.want) {
			t.Errorf("action %d: queue = %v, want %v", tt.action, got, tt.want)
		}
		if tt.action == spotAlbumPlay && m.playlist.Index() != 0 {
			t.Errorf("play: index %d, want 0", m.playlist.Index())
		}
	}
}

func TestSpotSearchTrackKeysStillAppend(t *testing.T) {
	for _, key := range []string{"a", "q"} {
		m := spotSearchModel(playlist.Track{Path: "T", Title: "T"})
		m.handleSpotSearchResultsKey(libKey(key))
		if got := queuePaths(m); !slices.Equal(got, []string{"Q1", "Q2", "T"}) {
			t.Errorf("%s: queue = %v, want [Q1 Q2 T]", key, got)
		}
	}
}

func TestStopDropsSpotSearchAlbumLoad(t *testing.T) {
	for _, action := range []spotAlbumAction{spotAlbumPlay, spotAlbumAppend, spotAlbumQueueNext} {
		m := spotSearchModel(albumResult("A"))
		canceled := false
		const gen = 5
		m.requests.spotAlbum = gen
		m.spotSearch.albumLoading = true
		m.spotSearch.cancel = func() { canceled = true }

		updated, _ := m.Update(playback.StopMsg{})
		m = updated.(Model)
		if !canceled || m.spotSearch.albumLoading {
			t.Errorf("action %d: Stop left the album load running (canceled %v, loading %v)", action, canceled, m.spotSearch.albumLoading)
		}
		updated, cmd := m.Update(spotAlbumTracksMsg{gen: gen, action: action, album: albumResult("A"), tracks: tracksOf("A1", "A2")})
		m = updated.(Model)
		if got := queuePaths(m); !slices.Equal(got, []string{"Q1", "Q2"}) || cmd != nil {
			t.Errorf("action %d: the album landed after Stop: queue %v", action, got)
		}
	}
}

// TestStopKeepsSpotSearchRequests: the overlay's other requests (a text
// search, a playlist add) share its cancel slot; Stop must leave them be.
func TestStopKeepsSpotSearchRequests(t *testing.T) {
	m := spotSearchModel()
	prov := commandsTestProvider{name: "Spotify"}
	m.spotSearch.prov, m.spotSearch.screen = prov, spotSearchInput
	m.spotSearch.query, m.spotSearch.loading = "nofx", true
	canceled := false
	m.spotSearch.cancel = func() { canceled = true }
	const gen = 9
	m.requests.spotSearch = gen

	updated, _ := m.Update(playback.StopMsg{})
	m = updated.(Model)
	if canceled {
		t.Fatal("Stop canceled a text search")
	}
	updated, _ = m.Update(spotSearchResultsMsg{gen: gen, providerName: prov.Name(), query: "nofx", tracks: tracksOf("T")})
	m = updated.(Model)
	if m.spotSearch.screen != spotSearchResults || len(m.spotSearch.results) != 1 {
		t.Errorf("the search's results were dropped after Stop (screen %v, %d results)", m.spotSearch.screen, len(m.spotSearch.results))
	}
}
