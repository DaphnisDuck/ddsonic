package model

// ddsonic: f stars and unstars a live cliamp radio channel, as it does a
// directory station; a song channel stays out (#135). The fixture is
// newFavModel's, over the real channel provider.

import (
	"slices"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/external/radio"
)

// libOpenChannels opens Radio > Browse Stations > cliamp radio, the way a
// listener reaches it.
func libOpenChannels(t *testing.T, m Model) Model {
	t.Helper()
	for _, title := range []string{"Radio", "Browse Stations", "cliamp radio"} {
		m = libEnter(t, m, title)
	}
	return m
}

// The places f finds the live channel Groove.
var channelFavContexts = []struct {
	name string
	open func(t *testing.T, m Model) Model
}{
	{"channel row", func(t *testing.T, m Model) Model {
		m = libOpenChannels(t, m)
		libSelect(t, &m, "Groove · live")
		return m
	}},
	{"channel stream in a list", func(t *testing.T, m Model) Model {
		m = libEnter(t, m, "Mixed")
		libSelect(t, &m, "Groove")
		return m
	}},
	{"queued channel", func(t *testing.T, m Model) Model {
		m = libOpenChannels(t, m)
		m = libEnter(t, m, "Groove · live")
		return libPress(t, m, "tab")
	}},
}

func TestFavoriteKeyTogglesALiveChannel(t *testing.T) {
	for _, tt := range channelFavContexts {
		t.Run(tt.name, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = tt.open(t, m)
			queued := m.playlist.Len()
			if got := favKeyHint(m); got != "Favorite" {
				t.Errorf("key bar hint for f = %q, want Favorite", got)
			}

			m = libPress(t, m, "f")
			want := []radio.CatalogStation{fx.groove}
			if got := fx.favs.Stations(); !slices.Equal(got, want) {
				t.Fatalf("after f, favorites = %+v, want %+v", got, want)
			}
			if got := favSaved(); !slices.Equal(got, want) {
				t.Errorf("after f, saved favorites = %+v, want %+v", got, want)
			}
			if got := m.status.text; got != "Favorited station: Groove" {
				t.Errorf("status = %q, want Favorited station: Groove", got)
			}
			if got := favKeyHint(m); got != "Unfavorite" {
				t.Errorf("key bar hint for f on a favorite = %q, want Unfavorite", got)
			}
			if m.playlist.Len() != queued {
				t.Errorf("f changed the queue: %d tracks, was %d", m.playlist.Len(), queued)
			}

			m = libPress(t, m, "f")
			if got := fx.favs.Stations(); len(got) != 0 {
				t.Fatalf("after a second f, favorites = %+v, want none", got)
			}
			if got := favSaved(); len(got) != 0 {
				t.Errorf("after a second f, saved favorites = %+v, want none", got)
			}
			if got := m.status.text; got != "Removed station: Groove" {
				t.Errorf("status = %q, want Removed station: Groove", got)
			}
			if got := favKeyHint(m); got != "Favorite" {
				t.Errorf("key bar hint for f after unstarring = %q, want Favorite", got)
			}
		})
	}
}

// A song channel opens as its songs, so it is no station: f is not offered,
// stars nothing and says nothing, as before #135.
func TestFavoriteKeyIgnoresASongChannel(t *testing.T) {
	m, fx := newFavModel(t)
	m = libOpenChannels(t, m)
	libSelect(t, &m, "Mixtape")
	cursor := m.libTop().cursor
	if got := favKeyHint(m); got != "" {
		t.Errorf("key bar offers f (%s) on a song channel", got)
	}
	m = libPress(t, m, "f")
	if got := fx.favs.Stations(); len(got) != 0 {
		t.Errorf("favorites = %+v, want none", got)
	}
	if got := favSaved(); len(got) != 0 {
		t.Errorf("saved favorites = %+v, want none", got)
	}
	if m.status.text != "" || m.playlist.Len() != 0 || m.libTop().cursor != cursor {
		t.Errorf("f on a song channel: status %q, queue %d, cursor %d; want nothing changed",
			m.status.text, m.playlist.Len(), m.libTop().cursor)
	}
}

// Enter on a live channel's row plays that channel alone, as it did when
// the row named no station.
func TestLiveChannelRowPlaysTheChannelAlone(t *testing.T) {
	m, fx := newFavModel(t)
	m = libOpenChannels(t, m)
	m = libEnter(t, m, "Groove · live")
	cur, _ := m.playlist.Current()
	if m.playlist.Len() != 1 || cur.Path != fx.groove.URL || !cur.Realtime {
		t.Fatalf("Enter queued %d tracks, current %+v; want the Groove stream alone", m.playlist.Len(), cur)
	}
}

// Enter on a channel row takes the row's Play, live or of songs, as before
// #135: it says it is loading the channel, then queues only that channel.
// The live row's station is for f alone.
func TestChannelRowEnterSaysLoading(t *testing.T) {
	for _, tt := range []struct{ row, status string }{
		{"Groove · live", "Loading Groove · live…"},
		{"Mixtape", "Loading Mixtape…"},
	} {
		t.Run(tt.row, func(t *testing.T) {
			m, fx := newFavModel(t)
			m = libOpenChannels(t, m)
			libSelect(t, &m, tt.row)
			updated, cmd := m.Update(libKey("enter"))
			m = updated.(Model)
			if m.status.text != tt.status || cmd == nil {
				t.Fatalf("Enter: status %q, command %v; want %q and a play", m.status.text, cmd != nil, tt.status)
			}
			if tt.row != "Groove · live" {
				return // a song channel's play downloads its songs
			}
			msg, ok := cmd().(libraryPlayMsg)
			if !ok {
				t.Fatal("Enter on the live row is not the row's Play")
			}
			updated, next := m.Update(msg)
			m = libRun(t, updated.(Model), next)
			if cur, _ := m.playlist.Current(); m.playlist.Len() != 1 || cur.Path != fx.groove.URL {
				t.Errorf("Enter queued %d tracks, current %q; want the Groove stream alone", m.playlist.Len(), cur.Path)
			}
		})
	}
}

// A starred channel is still starred after a restart (the store read back
// from disk), is listed in Radio > Favorites, and plays from there.
func TestFavoritedChannelAfterARestart(t *testing.T) {
	m, fx := newFavModel(t)
	m = channelFavContexts[0].open(t, m)
	libPress(t, m, "f") // saved by the time it returns
	if got := fx.favs.Stations(); !slices.Equal(got, []radio.CatalogStation{fx.groove}) {
		t.Fatalf("favorites = %+v, want Groove", got)
	}

	m, fx = favModelOver(t, radio.LoadFavorites()) // a fresh start
	m = channelFavContexts[0].open(t, m)
	if got := favKeyHint(m); got != "Unfavorite" {
		t.Errorf("after a restart, f on the channel = %q, want Unfavorite", got)
	}
	for range 2 { // back to Radio
		m = libPress(t, m, "esc")
	}
	m = libEnter(t, m, "Favorites")
	if got := libRowTitles(m); !slices.Equal(got, []string{"Groove"}) {
		t.Fatalf("Favorites = %v, want the channel", got)
	}
	m = libEnter(t, m, "Groove")
	cur, _ := m.playlist.Current()
	if m.playlist.Len() != 1 || cur.Path != fx.groove.URL || !cur.Stream || !cur.Realtime {
		t.Fatalf("Favorites played %+v (%d queued), want the Groove stream", cur, m.playlist.Len())
	}

	// Played from Favorites, it is the same station in the queue.
	m = libPress(t, m, "tab")
	if got := favKeyHint(m); got != "Unfavorite" {
		t.Errorf("queued favorite: f = %q, want Unfavorite", got)
	}
	m = libPress(t, m, "f")
	m = libPress(t, m, "tab")
	if got := libRowTitles(m); len(got) != 0 || len(favSaved()) != 0 {
		t.Errorf("after unstarring: Favorites %v, saved %+v; want empty", got, favSaved())
	}
	if !strings.Contains(m.status.text, "Groove") {
		t.Errorf("status = %q, want it to name the channel", m.status.text)
	}
}
