package model

// ddsonic: the lyrics view says when LRCLIB was busy rather than that the
// track has no lyrics (#82).

import (
	"errors"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/lyrics"
)

func TestLyricsViewTellsBusyFromNotFound(t *testing.T) {
	const (
		busy     = "Lyrics service unavailable, try again later."
		notFound = "No lyrics found for this track."
	)
	tests := []struct {
		name      string
		err       error
		want, not string
	}{
		{"not found", lyrics.ErrNotFound, notFound, busy},
		{"LRCLIB busy", lyrics.ErrUnavailable, busy, notFound},
		{"LRCLIB busy, wrapped", errors.Join(errors.New("context"), lyrics.ErrUnavailable), busy, notFound},
		{"other failure", errors.New("boom"), "Lyrics fetch failed: boom", notFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := keybindingTestModel()
			m.plVisible = 10
			m.lyrics.visible = true
			m.lyrics.loading = true
			m.lyrics.query = "Artist\nSong"
			gen := nextRequest(&m.requests.lyrics)
			next, _ := m.Update(lyricsLoadedMsg{err: tt.err, query: m.lyrics.query, gen: gen})
			m = next.(Model)

			body := m.renderLyricsBody()
			if !strings.Contains(body, tt.want) {
				t.Errorf("lyrics view %q, want %q", body, tt.want)
			}
			if strings.Contains(body, tt.not) {
				t.Errorf("lyrics view %q, must not say %q", body, tt.not)
			}
		})
	}
}
