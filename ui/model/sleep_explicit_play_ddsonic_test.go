package model

// ddsonic: the sleep timer (#35) and explicit playback that does not come
// from the library's own rows: Enter in the provider search overlay (a
// track, an album) and the IPC provider loads (both protocols). Accepting
// such a command during the fade, or while a pause waits at the deadline,
// cancels the fade and the timer, as Enter on a library row does. What was
// accepted before the fade, what is refused, a stale result, browsing and
// enqueueing change nothing.

import (
	"context"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// sleepAlbumProv is a provider that opens albums.
type sleepAlbumProv struct{ commandsTestProvider }

func (sleepAlbumProv) AlbumTracks(string) ([]playlist.Track, error) { return tracksOf("A1", "A2"), nil }

// sleepPlayRig is a library model playing Q2 of Q1-Q3, with a "spotify"
// provider that opens albums and a "plain" one that does not.
func sleepPlayRig(t *testing.T) *sleepRig {
	t.Helper()
	m := newLibraryModel(library.Menu("Music"))
	m.replacePlaylist(tracksOf("Q1", "Q2", "Q3"))
	m.playlist.SetIndex(1)
	m.providers = []ProviderEntry{
		{Key: "spotify", Name: "Spotify", Provider: sleepAlbumProv{commandsTestProvider{name: "Spotify"}}},
		{Key: "plain", Name: "Plain", Provider: commandsTestProvider{name: "Plain"}},
	}
	return rigFor(t, m, 30*time.Second)
}

// openSpotSearch shows the overlay's results, as "Search Spotify for …"
// leaves them.
func (r *sleepRig) openSpotSearch(prov playlist.Provider, results ...playlist.Track) {
	r.m.spotSearch = spotSearchState{visible: true, screen: spotSearchResults, results: results, prov: prov}
}

// pending is the deadline passing while a track starts: the output is held
// silent and the pause waits for the start to finish.
func (r *sleepRig) pending() {
	r.t.Helper()
	r.fading()
	r.m.buffering = true
	r.tick(15*time.Minute + time.Second)
	if !r.m.sleep.pausePending || !r.m.sleep.fading || !slices.Equal(r.e.fades, []string{"until 15m0s"}) {
		r.t.Fatalf("setup: pending %v, fading %v, fades %q", r.m.sleep.pausePending, r.m.sleep.fading, r.e.fades)
	}
}

// update delivers msg without running what it returns: the command is
// accepted, its result has not arrived.
func (r *sleepRig) update(msg tea.Msg) tea.Cmd {
	r.t.Helper()
	updated, cmd := r.m.Update(msg)
	r.m = updated.(Model)
	return cmd
}

func (r *sleepRig) cancelled() bool {
	return !r.armed() && !r.m.sleep.fading && !r.m.sleep.pausePending
}

func libraryLoad(op, prov, id string) ipc.LibraryRequestMsg {
	return ipc.LibraryRequestMsg{Op: op, Provider: prov, Playlist: id, Album: id, Reply: make(chan ipc.Response, 1)}
}

func TestSleepExplicitPlayCancelsDuringTheFade(t *testing.T) {
	spot := sleepAlbumProv{commandsTestProvider{name: "Spotify"}}
	tests := []struct {
		name string
		do   func(r *sleepRig)
	}{
		{"Spotify search: Enter on a track", func(r *sleepRig) {
			r.openSpotSearch(spot, playlist.Track{Path: "T", Title: "T"})
			r.update(libKey("enter"))
		}},
		{"Spotify search: Enter on an album", func(r *sleepRig) {
			r.openSpotSearch(spot, albumResult("A"))
			r.update(libKey("enter")) // the album's tracks have not arrived
		}},
		{"IPC provider.load", func(r *sleepRig) {
			r.update(v2(r.t, "provider.load", `{"provider":"spotify","playlist":"p1"}`))
		}},
		{"IPC provider.load_album", func(r *sleepRig) {
			r.update(v2(r.t, "provider.load_album", `{"provider":"spotify","album":"a1"}`))
		}},
		{"older IPC provider.load", func(r *sleepRig) { r.update(libraryLoad("provider.load", "spotify", "p1")) }},
		{"older IPC provider.load_album", func(r *sleepRig) { r.update(libraryLoad("provider.load_album", "spotify", "a1")) }},
	}
	for _, state := range []string{"fading", "pause pending"} {
		for _, tt := range tests {
			t.Run(state+"/"+tt.name, func(t *testing.T) {
				r := sleepPlayRig(t)
				if state == "fading" {
					r.fading()
				} else {
					r.pending()
				}
				tt.do(r)
				if !r.cancelled() {
					t.Fatalf("not cancelled: armed %v, fading %v, pending %v (fades %q)", r.armed(), r.m.sleep.fading, r.m.sleep.pausePending, r.e.fades)
				}
				if !slices.Equal(r.e.fades, []string{"until 15m0s", "cancel"}) {
					t.Errorf("fades %q, want the fade cancelled", r.e.fades)
				}
			})
		}
	}
}

// Commands that are refused, results that are stale, browsing and
// enqueueing, during the fade: the fade and the timer go on.
func TestSleepExplicitPlayLeavesTheFade(t *testing.T) {
	spot := sleepAlbumProv{commandsTestProvider{name: "Spotify"}}
	tests := []struct {
		name string
		do   func(r *sleepRig)
	}{
		{"search: a and q on a track", func(r *sleepRig) {
			r.openSpotSearch(spot, playlist.Track{Path: "T", Title: "T"})
			r.update(libKey("a"))
			r.openSpotSearch(spot, playlist.Track{Path: "T", Title: "T"})
			r.update(libKey("q"))
		}},
		{"search: a and q on an album", func(r *sleepRig) {
			r.openSpotSearch(spot, albumResult("A"))
			r.update(libKey("a"))
			r.openSpotSearch(spot, albumResult("A"))
			r.update(libKey("q"))
		}},
		{"search: moving the cursor", func(r *sleepRig) {
			r.openSpotSearch(spot, albumResult("A"), albumResult("B"))
			r.update(libKey("j"))
			r.update(libKey("k"))
		}},
		{"search: Enter on an album, refused", func(r *sleepRig) {
			r.openSpotSearch(commandsTestProvider{name: "Plain"}, albumResult("A"))
			r.update(libKey("enter")) // "This provider cannot open albums."
		}},
		{"search: a stale album result", func(r *sleepRig) {
			r.m.requests.spotAlbum = 7
			r.update(spotAlbumTracksMsg{gen: 6, action: spotAlbumPlay, album: albumResult("A"), tracks: tracksOf("A1")})
		}},
		{"IPC provider.load from an unknown provider", func(r *sleepRig) {
			r.update(v2(r.t, "provider.load", `{"provider":"nope","playlist":"p1"}`))
			r.update(libraryLoad("provider.load", "nope", "p1"))
		}},
		{"IPC provider.load_album from a provider without albums", func(r *sleepRig) {
			r.update(v2(r.t, "provider.load_album", `{"provider":"plain","album":"a1"}`))
			r.update(libraryLoad("provider.load_album", "plain", "a1"))
		}},
		{"IPC browsing", func(r *sleepRig) {
			for _, op := range []string{"provider.list", "provider.playlists", "provider.tracks", "provider.album_tracks", "provider.search"} {
				r.update(v2(r.t, op, `{"provider":"spotify","playlist":"p1","album":"a1","query":"x"}`))
				r.update(libraryLoad(op, "spotify", "p1"))
			}
		}},
		{"IPC provider load result, its request gone", func(r *sleepRig) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := libraryLoad("provider.load", "spotify", "p1")
			req.Context = ctx
			r.update(ipcProviderLoadResult{request: req, tracks: tracksOf("P1"), loaded: "p1"})
		}},
		{"IPC provider load result, failed", func(r *sleepRig) {
			r.update(ipcProviderLoadResult{request: libraryLoad("provider.load", "spotify", "p1"), err: context.DeadlineExceeded})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sleepPlayRig(t)
			r.fading()
			tt.do(r)
			if !r.armed() || !r.m.sleep.fading || len(r.e.fades) != 1 {
				t.Fatalf("the fade changed: armed %v, fading %v (fades %q, status %q)", r.armed(), r.m.sleep.fading, r.e.fades, r.m.status.text)
			}
		})
	}
}

// A provider load refused before it starts (its job or its context already
// cancelled when the UI reaches it) is not a command: the fade, the timer
// and the queue stay, and nothing is loaded.
func TestSleepRefusedProviderLoadLeavesTheFade(t *testing.T) {
	cancelledJob := func(op, params string) func(r *sleepRig) {
		return func(r *sleepRig) {
			msg := v2(r.t, op, params)
			if err := msg.Jobs.Cancel(msg.JobID); err != nil {
				r.t.Fatal(err)
			}
			r.update(msg)
		}
	}
	cancelledRequest := func(op, id string) func(r *sleepRig) {
		return func(r *sleepRig) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := libraryLoad(op, "spotify", id)
			req.Context = ctx
			r.update(req)
		}
	}
	tests := []struct {
		name string
		do   func(r *sleepRig)
	}{
		{"IPC provider.load, job cancelled", cancelledJob("provider.load", `{"provider":"spotify","playlist":"p1"}`)},
		{"IPC provider.load_album, job cancelled", cancelledJob("provider.load_album", `{"provider":"spotify","album":"a1"}`)},
		{"older IPC provider.load, context cancelled", cancelledRequest("provider.load", "p1")},
		{"older IPC provider.load_album, context cancelled", cancelledRequest("provider.load_album", "a1")},
	}
	for _, state := range []string{"fading", "pause pending"} {
		for _, tt := range tests {
			t.Run(state+"/"+tt.name, func(t *testing.T) {
				r := sleepPlayRig(t)
				if state == "fading" {
					r.fading()
				} else {
					r.pending()
				}
				tt.do(r)
				if !r.m.sleep.fading || len(r.e.fades) != 1 {
					t.Fatalf("the refused load cancelled the fade: armed %v, fading %v, pending %v (fades %q)", r.armed(), r.m.sleep.fading, r.m.sleep.pausePending, r.e.fades)
				}
				if state == "fading" && !r.armed() || state == "pause pending" && !r.m.sleep.pausePending {
					t.Errorf("the timer changed: armed %v, pending %v", r.armed(), r.m.sleep.pausePending)
				}
				if got := queuePaths(r.m); !slices.Equal(got, []string{"Q1", "Q2", "Q3"}) {
					t.Errorf("queue %v, want it unchanged", got)
				}
			})
		}
	}
}

// A play accepted before the fade whose result arrives during it is not a
// command given during the fade: the timer goes on, as for a library row
// whose tracks load late.
func TestSleepPlayAcceptedBeforeTheFade(t *testing.T) {
	spot := sleepAlbumProv{commandsTestProvider{name: "Spotify"}}
	tests := []struct {
		name   string
		accept func(r *sleepRig) tea.Msg // returns the result, delivered later
	}{
		{"Spotify search album", func(r *sleepRig) tea.Msg {
			r.openSpotSearch(spot, albumResult("A"))
			return r.update(libKey("enter"))()
		}},
		{"IPC provider.load", func(r *sleepRig) tea.Msg {
			return r.update(libraryLoad("provider.load", "spotify", "p1"))()
		}},
		{"IPC provider.load_album", func(r *sleepRig) tea.Msg {
			return r.update(libraryLoad("provider.load_album", "spotify", "a1"))()
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sleepPlayRig(t)
			r.press(1)
			result := tt.accept(r)
			if !r.armed() || r.m.sleep.fading {
				t.Fatalf("setup: armed %v, fading %v", r.armed(), r.m.sleep.fading)
			}
			r.m.spotSearch.visible = false
			r.tick(14*time.Minute + 40*time.Second)
			if !r.m.sleep.fading {
				t.Fatalf("setup: no fade (fades %q)", r.e.fades)
			}
			r.update(result)
			if !r.armed() || !r.m.sleep.fading || len(r.e.fades) != 1 {
				t.Fatalf("the late result cancelled the fade: armed %v, fading %v (fades %q)", r.armed(), r.m.sleep.fading, r.e.fades)
			}
		})
	}
}
