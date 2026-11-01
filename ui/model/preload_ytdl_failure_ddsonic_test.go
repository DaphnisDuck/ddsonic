package model

// ddsonic: a YouTube track whose preload fails is not preloaded again on
// every tick, with the real player behind the UI.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

// nearTheEnd is the real player, said to be playing a track in its last ten
// seconds, which is when the UI preloads the next one.
type nearTheEnd struct{ *player.Player }

func (nearTheEnd) IsPlaying() bool         { return true }
func (nearTheEnd) Duration() time.Duration { return time.Minute }
func (nearTheEnd) Position() time.Duration { return 50 * time.Second }

func TestAFailedYTDLPreloadIsNotRetriedOnEveryTick(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Fail, "")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ffmpeg"), []byte("#!/bin/sh\ncat\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	const next = "https://www.youtube.com/watch?v=unavailable"
	pl := playlist.New()
	pl.Replace([]playlist.Track{
		{Title: "Current", Path: "https://www.youtube.com/watch?v=current", DurationSecs: 60},
		{Title: "Next", Path: next, DurationSecs: 60},
	})
	pl.SetIndex(0)
	m := Model{player: nearTheEnd{&player.Player{}}, playlist: pl}

	// The tick loop asks for a preload whenever none is in flight.
	for tick := range 5 {
		cmd := m.preloadNext()
		if cmd == nil {
			continue
		}
		if tick > 0 {
			t.Errorf("tick %d: the failed preload was asked for again", tick)
		}
		updated, _ := m.Update(cmd())
		m = updated.(Model)
	}

	if m.preloadFailed != next {
		t.Errorf("preloadFailed = %q, want %q: yt-dlp's failure did not reach the UI", m.preloadFailed, next)
	}
	if runs := fake.Runs(t); len(runs) != 1 {
		t.Errorf("yt-dlp was started %d times for a track it cannot load, want once", len(runs))
	}
}
