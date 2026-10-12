package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// actionWriter is a provider whose writes belong to an action, as Spotify's
// do: it hands out a numbered action context and records which one each add
// was made with.
type actionWriter struct {
	actions int
	adds    []any // the action each add carried, nil for none
	failAt  int   // the add that fails, 1-based; 0 for none
}

type actionKey struct{}

func (w *actionWriter) Name() string                                { return "Spotify" }
func (w *actionWriter) Playlists() ([]playlist.PlaylistInfo, error) { return nil, nil }
func (w *actionWriter) Tracks(string) ([]playlist.Track, error)     { return nil, nil }
func (w *actionWriter) WithSignIn(ctx context.Context) context.Context {
	w.actions++
	return context.WithValue(ctx, actionKey{}, w.actions)
}
func (w *actionWriter) AddTrackToPlaylist(ctx context.Context, _ string, _ playlist.Track) error {
	w.adds = append(w.adds, ctx.Value(actionKey{}))
	if len(w.adds) == w.failAt {
		return fmt.Errorf("%w: the Spotify session changed, so the request was not sent; try again", provider.ErrNotApplied)
	}
	return nil
}

func threeTracks() []ipc.TrackInfo {
	return []ipc.TrackInfo{{Path: "spotify:track:1"}, {Path: "spotify:track:2"}, {Path: "spotify:track:3"}}
}

// playlist.add_many over IPC is one action: every add is made with the one
// action context obtained before the first, so that a provider can refuse to
// continue it under a sign-in that replaced the one it began on.
func TestIPCAddManyIsOneAction(t *testing.T) {
	w := &actionWriter{}
	m := Model{providers: []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: w}}}
	reply := make(chan ipc.Response, 1)
	cmd := m.handleIPCLibrary(ipc.LibraryRequestMsg{Op: "playlist.add_many", Provider: "spotify", Playlist: "pl", Tracks: threeTracks(), Reply: reply})
	if cmd == nil {
		t.Fatal("playlist.add_many returned no command")
	}
	cmd()
	if resp := <-reply; !resp.OK {
		t.Fatalf("playlist.add_many = %+v", resp)
	}
	if w.actions != 1 {
		t.Errorf("action contexts obtained = %d, want 1 for the whole request", w.actions)
	}
	for i, a := range w.adds {
		if a != 1 {
			t.Errorf("add %d carried action %v, want the request's one action", i+1, a)
		}
	}
	if len(w.adds) != 3 {
		t.Errorf("adds = %d, want 3", len(w.adds))
	}
}

// When the action stops part-way, what was added stays added and is not
// added again; the error says how far it got and promises no undoing.
func TestIPCAddManyStopsWithoutReplayOrRollback(t *testing.T) {
	w := &actionWriter{failAt: 2}
	m := Model{providers: []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: w}}}
	reply := make(chan ipc.Response, 1)
	m.handleIPCLibrary(ipc.LibraryRequestMsg{Op: "playlist.add_many", Provider: "spotify", Playlist: "pl", Tracks: threeTracks(), Reply: reply})()
	resp := <-reply
	if resp.OK || len(w.adds) != 2 {
		t.Fatalf("playlist.add_many = %+v after %d adds, want an error after the second", resp, len(w.adds))
	}
	if !strings.Contains(resp.Error, "1 of 3") || !strings.Contains(resp.Error, "session changed") {
		t.Errorf("the error %q does not say that 1 of 3 tracks was added, and why it stopped", resp.Error)
	}
	for _, word := range []string{"rolled back", "undone", "reverted"} {
		if strings.Contains(resp.Error, word) {
			t.Errorf("the error %q implies the added track was taken back", resp.Error)
		}
	}
}

func (w *actionWriter) CreatePlaylist(ctx context.Context, _ string) (string, error) {
	w.adds = append(w.adds, ctx.Value(actionKey{}))
	if len(w.adds) == w.failAt {
		return "", errors.New("create refused")
	}
	return "pl-new", nil
}

// Creating a playlist and adding the track to it, from Spotify's search, is
// one action: both requests carry the one action context. When the add does
// not happen, the message says the playlist was created and stays.
func TestCreateAndAddIsOneAction(t *testing.T) {
	w := &actionWriter{}
	msg := createSpotPlaylistCmd(context.Background(), w, w, "Spotify", "New", playlist.Track{Path: "spotify:track:1"}, 1)()
	if created, ok := msg.(spotCreatedMsg); !ok || created.err != nil || !created.created {
		t.Fatalf("create-and-add = %+v", msg)
	}
	if w.actions != 1 || len(w.adds) != 2 || w.adds[0] != 1 || w.adds[1] != 1 {
		t.Errorf("action contexts obtained = %d, carried by the two requests as %v; want one, on both", w.actions, w.adds)
	}

	w = &actionWriter{failAt: 2} // the add stops: the sign-in changed
	msg = createSpotPlaylistCmd(context.Background(), w, w, "Spotify", "New", playlist.Track{Path: "spotify:track:1"}, 7)()
	m := Model{}
	m.spotSearch = spotSearchState{prov: w, visible: true, screen: spotSearchNewName}
	m.requests.spotMutation = 7
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if got := m.spotSearch.err; !strings.Contains(got, `Created "New"`) || !strings.Contains(got, "not added") || strings.Contains(got, "Create failed") {
		t.Errorf("after a create that worked and an add that did not, the overlay says %q", got)
	}
}

// After a playlist was created and its track's add did not complete, the
// screen says what is known: the playlist exists; and of the track, that it
// was not added when that is certain (never sent, or sign-in required), or
// that it may have been when the request's outcome is not known. A sign-in
// that is needed is offered beside the news that the playlist exists.
func TestCreateAndAddReportsWhatIsKnown(t *testing.T) {
	overlay := func(t *testing.T, err error) (Model, *signInProv) {
		t.Helper()
		l := newSignInLib(t)
		m := l.model()
		m.spotSearch = spotSearchState{prov: l.prov, visible: true, screen: spotSearchNewName}
		m.requests.spotMutation = 7
		return sendAll(t, m, spotCreatedMsg{name: "New", err: err, created: true, providerName: "Spotify", gen: 7}), l.prov
	}
	t.Run("sign-in required for the add", func(t *testing.T) {
		m, prov := overlay(t, fmt.Errorf("spotify: add track: %w", playlist.ErrNeedsAuth))
		wantView(t, "the overlay", m, []string{`Created "New"`, signInPrompt}, "Create failed")
		if !m.libSyncState(catalog.Spotify).needsAuth {
			t.Error("Spotify is not marked sign-in required")
		}
		m = pressAll(t, m, "enter")
		if prov.auths != 1 || prov.adds != 0 {
			t.Errorf("after Enter: sign-ins = %d, adds = %d; want the sign-in and no add", prov.auths, prov.adds)
		}
		wantView(t, "the overlay after signing in", m, []string{`Created "New"`, "was not added"}, signInPrompt)
	})
	t.Run("the add was not sent", func(t *testing.T) {
		m, _ := overlay(t, fmt.Errorf("spotify: add track: %w", provider.ErrNotApplied))
		wantView(t, "the overlay", m, []string{`Created "New"`, "was not added"}, "may or may not", "Create failed")
	})
	t.Run("the add timed out", func(t *testing.T) {
		m, _ := overlay(t, fmt.Errorf("spotify: add track: %w", context.DeadlineExceeded))
		wantView(t, "the overlay", m, []string{`Created "New"`, "may or may not have been added", "check the playlist"}, "was not added", "Create failed")
	})
}

// Every way an action on the account can begin fixes its action context when
// the request arrives, before the work is run: the single playlist.create
// and playlist.add over IPC, the search overlay's add, and its
// create-and-add. A sign-in that completes between the request and its first
// write then finds the action already begun.
func TestActionContextIsFixedWhenTheRequestArrives(t *testing.T) {
	track := &ipc.TrackInfo{Path: "spotify:track:1"}
	for _, op := range []string{"playlist.create", "playlist.add", "playlist.add_many"} {
		t.Run("IPC "+op, func(t *testing.T) {
			w := &actionWriter{}
			m := Model{providers: []ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: w}}}
			reply := make(chan ipc.Response, 1)
			cmd := m.handleIPCLibrary(ipc.LibraryRequestMsg{Op: op, Provider: "spotify", Playlist: "pl", Track: track, Tracks: threeTracks(), Reply: reply})
			if cmd == nil {
				t.Fatalf("%s returned no command", op)
			}
			if w.actions != 1 {
				t.Fatalf("action contexts obtained before the work ran = %d, want 1", w.actions)
			}
			cmd()
			if resp := <-reply; !resp.OK {
				t.Fatalf("%s = %+v", op, resp)
			}
			if w.actions != 1 {
				t.Errorf("action contexts obtained in all = %d, want the one", w.actions)
			}
			for i, a := range w.adds {
				if a != 1 {
					t.Errorf("write %d carried action %v, want the request's", i+1, a)
				}
			}
		})
	}
	t.Run("the overlay's add", func(t *testing.T) {
		w := &actionWriter{}
		cmd := addToSpotPlaylistCmd(context.Background(), w, "pl", playlist.Track{Path: "spotify:track:1"}, "Spotify", "Mine", 1)
		if w.actions != 1 {
			t.Fatalf("action contexts obtained before the work ran = %d, want 1", w.actions)
		}
		cmd()
		if w.actions != 1 || len(w.adds) != 1 || w.adds[0] != 1 {
			t.Errorf("action contexts = %d, carried as %v; want the one", w.actions, w.adds)
		}
	})
	t.Run("the overlay's create-and-add", func(t *testing.T) {
		w := &actionWriter{}
		cmd := createSpotPlaylistCmd(context.Background(), w, w, "Spotify", "New", playlist.Track{Path: "spotify:track:1"}, 1)
		if w.actions != 1 {
			t.Fatalf("action contexts obtained before the work ran = %d, want 1", w.actions)
		}
		cmd()
		if w.actions != 1 || len(w.adds) != 2 {
			t.Errorf("action contexts = %d for %d writes; want the one for both", w.actions, len(w.adds))
		}
	})
}
