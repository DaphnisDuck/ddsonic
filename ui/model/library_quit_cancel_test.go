package model

// ddsonic: quitting cancels foreground library work in flight (review R7).

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// waitsForCancel loads nothing until its context ends, and reports why.
func waitsForCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("never cancelled")
	}
}

func TestQuitCancelsForegroundWork(t *testing.T) {
	slow := library.NewLevel("Slow", func(ctx context.Context) ([]library.Entry, error) {
		return nil, waitsForCancel(ctx)
	})
	album := library.Entry{Title: "Album", Play: func(ctx context.Context) ([]playlist.Track, error) {
		return nil, waitsForCancel(ctx)
	}}
	for _, tt := range []struct {
		name   string
		cursor int
		errOf  func(tea.Msg) error
	}{
		{"a level loading", 0, func(msg tea.Msg) error { return msg.(libraryLoadedMsg).err }},
		{"an album resolving to play", 1, func(msg tea.Msg) error { return msg.(libraryPlayMsg).err }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Slow", Open: slow}, album))
			m.libTop().cursor = tt.cursor
			updated, held := m.Update(libKey("enter"))
			m = updated.(Model)
			updated, _ = m.Update(libKey("q"))
			if !updated.(Model).quitting {
				t.Fatal("q did not quit")
			}
			done := make(chan error, 1)
			go func() { done <- tt.errOf(held()) }()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("work ended with %v, want it cancelled by quitting", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("work still running after quit")
			}
		})
	}
}

// A signal ends the program without the model's quit; main's StopLibrary,
// called on the model it started with, still cancels the work in flight.
func TestStopLibraryCancelsWithoutUpdate(t *testing.T) {
	slow := library.NewLevel("Slow", func(ctx context.Context) ([]library.Entry, error) {
		return nil, waitsForCancel(ctx)
	})
	start := newLibraryModel(library.Menu("Music", library.Entry{Title: "Slow", Open: slow}))
	updated, held := start.Update(libKey("enter"))
	_ = updated // the program's later copies are gone; main holds start
	start.StopLibrary()
	done := make(chan error, 1)
	go func() { done <- held().(libraryLoadedMsg).err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("work ended with %v, want it cancelled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("work still running after StopLibrary")
	}
}
