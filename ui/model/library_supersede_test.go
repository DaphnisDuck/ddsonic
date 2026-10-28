package model

// ddsonic: a library play still resolving must not land after a newer intent.

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/internal/playback"
	"github.com/bjarneo/cliamp/ipc"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

func queuePaths(m Model) []string {
	var out []string
	for _, tr := range m.playlist.Tracks() {
		out = append(out, tr.Path)
	}
	return out
}

func TestLibraryPlayRetiredByNewerIntent(t *testing.T) {
	b := playlist.Track{Path: "B", Title: "B"}
	root := library.Menu("Music",
		library.Entry{Title: "Album", Play: func(context.Context) ([]playlist.Track, error) {
			return tracksOf("A1", "A2"), nil
		}},
		library.Entry{Title: "B", Track: &b},
	)
	tests := []struct {
		name      string
		intervene func(t *testing.T, m Model) Model
		want      []string
	}{
		{"a track chosen in the library", func(t *testing.T, m Model) Model {
			m = libPress(t, m, "j")
			return libPress(t, m, "enter")
		}, []string{"B"}},
		{"Stop", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(playback.StopMsg{})
			return updated.(Model)
		}, []string{"Q1", "Q2"}},
		{"the queue replaced elsewhere (IPC, a provider)", func(t *testing.T, m Model) Model {
			m.replacePlaylist(tracksOf("X"))
			return m
		}, []string{"X"}},
		{"IPC queue.play", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(ipc.QueueRequestMsg{Op: "queue.play", Index: 1, Reply: make(chan ipc.Response, 1)})
			return updated.(Model)
		}, []string{"Q1", "Q2"}},
		{"IPC v2 queue.play", func(t *testing.T, m Model) Model {
			jobs := ipc.NewJobStore()
			job, err := jobs.Create("queue.play")
			if err != nil {
				t.Fatal(err)
			}
			updated, _ := m.Update(V2RequestMsg{Request: ipc.V2Request{Operation: "queue.play",
				Params: json.RawMessage(`{"index":1}`)}, Jobs: jobs, JobID: job.ID})
			return updated.(Model)
		}, []string{"Q1", "Q2"}},
		{"IPC track.play", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(ipc.QueueRequestMsg{Op: "track.play", Track: &ipc.TrackInfo{Path: "T"},
				Reply: make(chan ipc.Response, 1)})
			return updated.(Model)
		}, []string{"Q1", "Q2", "T"}},
		{"a plugin's jump", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(PluginQueueMsg{Op: "jump", Index: 1})
			return updated.(Model)
		}, []string{"Q1", "Q2"}},
		{"n (next) in the queue", func(t *testing.T, m Model) Model {
			m = libPress(t, m, "tab")
			return libPress(t, m, "n")
		}, []string{"Q1", "Q2"}},
		{"Next from outside (MPRIS)", func(t *testing.T, m Model) Model {
			updated, _ := m.Update(playback.NextMsg{})
			return updated.(Model)
		}, []string{"Q1", "Q2"}},
		{"Enter in the queue filter", func(t *testing.T, m Model) Model {
			m = libPress(t, m, "tab")
			m.search.active, m.search.results, m.search.cursor = true, []int{1}, 0
			return libPress(t, m, "enter")
		}, []string{"Q1", "Q2"}},
		{"IPC play of a URL", func(t *testing.T, m Model) Model {
			m.handleIPCURLResult(ipcURLLoadResult{request: ipc.URLRequestMsg{Play: true, Reply: make(chan ipc.Response, 1)},
				tracks: tracksOf("U")})
			return m
		}, []string{"Q1", "Q2", "U"}},
		{"a podcast's episodes loaded to play", func(t *testing.T, m Model) Model {
			m.appendSubscriptionTracks(tracksOf("E"), subsLoadPlay, "Show")
			return m
		}, []string{"Q1", "Q2", "E"}},
		// Not an intent: the album still lands after the current track ends.
		{"the automatic advance at a track's end", func(t *testing.T, m Model) Model {
			m.nextTrack()
			return m
		}, []string{"A1", "A2"}},
		{"a track chosen in the queue", func(t *testing.T, m Model) Model {
			m = libPress(t, m, "tab")
			m.focus, m.plCursor = focusPlaylist, 1
			return libPress(t, m, "enter")
		}, []string{"Q1", "Q2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newLibraryModel(root)
			m.replacePlaylist(tracksOf("Q1", "Q2"))
			// Enter on Album starts resolving it; hold the result.
			updated, held := m.Update(libKey("enter"))
			m = updated.(Model)
			if held == nil {
				t.Fatal("no play command for Album")
			}
			m = tt.intervene(t, m)
			late := held()
			if _, ok := late.(libraryPlayMsg); !ok {
				t.Fatalf("held command returned %T, want libraryPlayMsg", late)
			}
			updated, _ = m.Update(late)
			m = updated.(Model)
			if got := queuePaths(m); !slices.Equal(got, tt.want) {
				t.Errorf("queue after the late album = %q, want %q", got, tt.want)
			}
		})
	}
}

var _ tea.Msg = libraryPlayMsg{}
