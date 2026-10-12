package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// daemonActionWriter is a provider whose writes belong to an action, as
// Spotify's do (see ui/model's actionWriter).
type daemonActionWriter struct {
	actions int
	adds    []any
	failAt  int
}

type daemonActionKey struct{}

func (w *daemonActionWriter) Name() string                                { return "Spotify" }
func (w *daemonActionWriter) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (w *daemonActionWriter) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (w *daemonActionWriter) WithSignIn(ctx context.Context) context.Context {
	w.actions++
	return context.WithValue(ctx, daemonActionKey{}, w.actions)
}
func (w *daemonActionWriter) AddTrackToPlaylist(ctx context.Context, _ string, _ playlist.Track) error {
	w.adds = append(w.adds, ctx.Value(daemonActionKey{}))
	if len(w.adds) == w.failAt {
		return errors.New("the Spotify session changed, so the request was not sent; try again")
	}
	return nil
}

// The daemon's playlist.add_many is one action too: one action context for
// every add, and an error that says how far it got without promising to undo.
func TestDaemonAddManyIsOneAction(t *testing.T) {
	tracks := []ipc.TrackInfo{{Path: "spotify:track:1"}, {Path: "spotify:track:2"}, {Path: "spotify:track:3"}}
	w := &daemonActionWriter{}
	d := &daemon{providers: []model.ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: w}}}
	reply := make(chan ipc.Response, 1)
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "playlist.add_many", Provider: "spotify", Playlist: "pl", Tracks: tracks, Reply: reply})
	if resp := <-reply; !resp.OK || resp.Total != 3 {
		t.Fatalf("playlist.add_many = %+v", resp)
	}
	if w.actions != 1 || len(w.adds) != 3 {
		t.Fatalf("action contexts obtained = %d for %d adds, want 1 and 3", w.actions, len(w.adds))
	}
	for i, a := range w.adds {
		if a != 1 {
			t.Errorf("add %d carried action %v, want the request's one action", i+1, a)
		}
	}

	w = &daemonActionWriter{failAt: 2}
	d = &daemon{providers: []model.ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: w}}}
	d.handleLibrary(ipc.LibraryRequestMsg{Op: "playlist.add_many", Provider: "spotify", Playlist: "pl", Tracks: tracks, Reply: reply})
	resp := <-reply
	if resp.OK || len(w.adds) != 2 {
		t.Fatalf("playlist.add_many = %+v after %d adds, want an error after the second", resp, len(w.adds))
	}
	if !strings.Contains(resp.Error, "1 of 3") || !strings.Contains(resp.Error, "session changed") {
		t.Errorf("the error %q does not say that 1 of 3 tracks was added, and why it stopped", resp.Error)
	}
}
