package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

type addWriter struct {
	adds   int
	failAt int
	err    error
}

func (w *addWriter) AddTrackToPlaylist(context.Context, string, playlist.Track) error {
	w.adds++
	if w.adds == w.failAt {
		return w.err
	}
	return nil
}

// An add of several tracks that stops says what is known and no more: how
// many were added, and whether the one it stopped at was certainly not added
// (never sent, or refused for the sign-in) or may have been (a timeout, a
// dropped connection: Spotify may have applied it). It never says anything
// was undone.
func TestAddTracksSaysWhatIsKnown(t *testing.T) {
	tracks := make([]playlist.Track, 3)
	tests := []struct {
		name    string
		failAt  int
		err     error
		want    []string
		notWant []string
	}{
		{"not sent", 2, fmt.Errorf("spotify: session changed: %w", ErrNotApplied), []string{"added 1 of 3", "stay added", "was not added"}, []string{"may or may not"}},
		{"sign-in required", 2, fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth), []string{"added 1 of 3", "was not added"}, []string{"may or may not"}},
		{"timed out", 2, context.DeadlineExceeded, []string{"added 1 of 3", "stay added", "may or may not have been added", "check the playlist"}, []string{"was not added"}},
		{"timed out on the first", 1, context.DeadlineExceeded, []string{"added 0 of 3", "may or may not have been added"}, []string{"was not added"}},
		{"connection lost", 3, errors.New("read tcp: connection reset by peer"), []string{"added 2 of 3", "may or may not have been added"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &addWriter{failAt: tt.failAt, err: tt.err}
			err := AddTracks(context.Background(), w, "pl", tracks)
			if err == nil || !errors.Is(err, tt.err) || w.adds != tt.failAt {
				t.Fatalf("AddTracks() = %v after %d adds, want the failure after %d", err, w.adds, tt.failAt)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error %q lacks %q", err, want)
				}
			}
			for _, word := range append(tt.notWant, "rolled back", "undone", "reverted") {
				if strings.Contains(err.Error(), word) {
					t.Errorf("the error %q says %q", err, word)
				}
			}
		})
	}
	// A first add that certainly did not happen is the plain error.
	w := &addWriter{failAt: 1, err: ErrNotApplied}
	if err := AddTracks(context.Background(), w, "pl", tracks); !errors.Is(err, ErrNotApplied) || strings.Contains(err.Error(), "of 3") {
		t.Errorf("AddTracks() = %v, want the plain error when nothing was added and nothing may have been", err)
	}
}
