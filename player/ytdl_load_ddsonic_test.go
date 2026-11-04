package player

// ddsonic: a YouTube track that is still loading (yt-dlp has produced no
// audio yet) is stopped when it is no longer wanted: by Stop, by the next
// request, by a newer preload or seek.

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

const loadURL = "https://www.youtube.com/watch?v=loading"

// stopsWithin is how long an unwanted load's yt-dlp may stay alive. Stopping
// it takes a kill and a reap; the load used to run on for 30 s.
const stopsWithin = time.Second

// actsWithin is how long Stop, or declaring a new request, may take: they
// run on the UI's goroutine.
const actsWithin = 250 * time.Millisecond

func newLoadPlayer() *Player {
	return &Player{gapless: &gaplessStreamer{}, sr: beep.SampleRate(44100), bitDepth: 16, suspended: true}
}

// timed runs f and fails the test when it takes longer than limit.
func timed(t *testing.T, what string, limit time.Duration, f func()) time.Duration {
	t.Helper()
	start := time.Now()
	f()
	took := time.Since(start)
	if took > limit {
		t.Errorf("%s took %v, want under %v", what, took, limit)
	}
	return took
}

// finished waits for a load to return.
func finished(t *testing.T, what string, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not return within 5 s of being stopped", what)
		return nil
	}
}

func TestStopCancelsAYTDLLoadBeforeItsFirstAudio(t *testing.T) {
	tests := []struct {
		name string
		play func(*Player) error
	}{
		{"a request with a generation", func(p *Player) error { return p.PlayYTDLForGeneration(loadURL, time.Minute, 1) }},
		{"a plain request", func(p *Player) error { return p.PlayYTDL(loadURL, time.Minute) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
			installPassThroughFFmpeg(t)
			p := newLoadPlayer()
			p.SetPlaybackGeneration(1)
			done := make(chan error, 1)
			go func() { done <- tt.play(p) }()
			fake.WaitRuns(t, 1)

			stop := timed(t, "Stop", actsWithin, p.Stop)
			gone := fake.WaitLive(t, 0, stopsWithin)
			t.Logf("Stop returned in %v; yt-dlp was gone %v after it", stop, gone)
			if err := finished(t, "the load", done); err == nil && tt.name == "a plain request" {
				t.Error("a stopped load reported that it is playing")
			}
			if p.IsPlaying() {
				t.Error("the player is playing after Stop")
			}
			fake.RequireNoCopy(t)
		})
	}
}

// Skipping: the UI declares the next request, which makes the one still
// loading obsolete.
func TestANewRequestCancelsTheYTDLLoadItSupersedes(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(1)
	done := make(chan error, 1)
	go func() { done <- p.PlayYTDLForGeneration(loadURL, time.Minute, 1) }()
	fake.WaitRuns(t, 1)

	next := timed(t, "declaring the next request", actsWithin, func() { p.SetPlaybackGeneration(2) })
	gone := fake.WaitLive(t, 0, stopsWithin)
	t.Logf("the next request was declared in %v; the obsolete yt-dlp was gone %v after it", next, gone)
	if err := finished(t, "the obsolete load", done); err != nil {
		t.Errorf("the obsolete load = %v, want nil: it is not an error the user should see", err)
	}
	if p.IsPlaying() {
		t.Error("the obsolete load started playing")
	}
	fake.RequireNoCopy(t)
}

// Holding the skip key: each request makes the last obsolete. The yt-dlp
// runs must not pile up.
func TestRepeatedSkipsDoNotAccumulateYTDLRuns(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	const skips = 6
	done := make(chan error, skips)
	for gen := uint64(1); gen <= skips; gen++ {
		p.SetPlaybackGeneration(gen)
		go func() { done <- p.PlayYTDLForGeneration(loadURL, time.Minute, gen) }()
		fake.WaitRuns(t, int(gen))
		// Only the newest request's yt-dlp may be running.
		fake.WaitLive(t, 1, stopsWithin)
	}
	timed(t, "Stop", actsWithin, p.Stop)
	fake.WaitLive(t, 0, stopsWithin)
	for range skips {
		finished(t, "a load", done)
	}
	if got := len(fake.Runs(t)); got != skips {
		t.Errorf("yt-dlp was started %d times, want %d", got, skips)
	}
	if p.IsPlaying() {
		t.Error("the player is playing after Stop")
	}
	fake.RequireNoCopy(t)
}

// A request that was already obsolete when its load would have begun starts
// no yt-dlp at all.
func TestAnObsoleteRequestStartsNoYTDLRun(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(2)
	if err := p.PlayYTDLForGeneration(loadURL, time.Minute, 1); err != nil {
		t.Errorf("the obsolete request = %v, want nil", err)
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was started for an obsolete request: %q", runs)
	}
}

// With no known duration a load runs two yt-dlp: the stream and a probe for
// the duration. Both belong to the request and go with it.
func TestStopCancelsTheDurationProbeOfAYTDLLoad(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(1)
	done := make(chan error, 1)
	go func() { done <- p.PlayYTDLForGeneration(loadURL, 0, 1) }()
	fake.WaitRuns(t, 2)
	var probe bool
	for _, run := range fake.Runs(t) {
		probe = probe || strings.Contains(run, "--print duration")
	}
	if !probe {
		t.Fatalf("no duration probe among the runs: %q", fake.Runs(t))
	}

	timed(t, "Stop", actsWithin, p.Stop)
	fake.WaitLive(t, 0, stopsWithin)
	finished(t, "the load", done)
	// The probe runs, and is released, on a goroutine of its own that the
	// load does not wait for: its directory may outlive the load by a moment.
	fake.WaitNoCopy(t, stopsWithin)
	fake.RequireNoCopy(t)
}

// A preload of the next track that is still loading goes when the preload is
// dropped or replaced, and with Stop.
func TestAYTDLPreloadStillLoadingIsCancelledWithItsRequest(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*Player)
	}{
		{"ClearPreload", func(p *Player) { p.ClearPreload() }},
		{"a newer preload", func(p *Player) { p.BeginPreload() }},
		{"Stop", func(p *Player) { p.Stop() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
			installPassThroughFFmpeg(t)
			p := newLoadPlayer()
			gen := p.BeginPreload()
			done := make(chan error, 1)
			go func() { done <- p.PreloadYTDLForGeneration(loadURL, time.Minute, gen) }()
			fake.WaitRuns(t, 1)

			timed(t, tt.name, actsWithin, func() { tt.cancel(p) })
			fake.WaitLive(t, 0, stopsWithin)
			if err := finished(t, "the preload", done); err != nil {
				t.Errorf("the obsolete preload = %v, want nil", err)
			}
			fake.RequireNoCopy(t)
		})
	}
}

// Stopping playback is not quitting: a yt-dlp run that is not this player's
// load (a sync, a list being read, a track being saved) carries on.
func TestStopLeavesOtherYTDLRunsAlone(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	other, release, err := ytdlp.Command(context.Background(), "--flat-playlist", "--", "https://example.invalid/other")
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	fake.WaitRuns(t, 1)

	p := newLoadPlayer()
	p.SetPlaybackGeneration(1)
	done := make(chan error, 1)
	go func() { done <- p.PlayYTDLForGeneration(loadURL, time.Minute, 1) }()
	fake.WaitRuns(t, 2)

	p.SetPlaybackGeneration(2)
	p.Stop()
	fake.WaitLive(t, 1, stopsWithin)
	finished(t, "the load", done)
	// Still there a moment later: nothing reached it.
	time.Sleep(200 * time.Millisecond)
	if live := fake.Live(t); live != 1 {
		t.Errorf("%d yt-dlp run(s) alive after Stop, want the one that is not the player's", live)
	}
	if other.ProcessState != nil {
		t.Error("the other run has exited")
	}

	_ = other.Process.Kill()
	_ = other.Wait()
	if err := release(); err != nil {
		t.Errorf("release: %v", err)
	}
	fake.RequireNoCopy(t)
}

// A seek in a YouTube track restarts yt-dlp at the new position. While that
// one is loading, Stop and a newer seek make it unwanted.
func TestAYTDLSeekStillLoadingIsCancelledWithItsRequest(t *testing.T) {
	tests := []struct {
		name   string
		cancel func(*Player)
	}{
		{"a newer seek", func(p *Player) { p.CancelSeekYTDL() }},
		{"Stop", func(p *Player) { p.Stop() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
			installPassThroughFFmpeg(t)
			p := newLoadPlayer()
			// What is playing: a yt-dlp track 3 s in, with no processes of
			// its own to stop.
			decoder := &ytdlPipeStreamer{
				state:     newPipeStreamState(300),
				ytdlCmd:   exec.Command("true"),
				ffmpegCmd: exec.Command("true"),
				pipe:      io.NopCloser(bytes.NewReader(nil)),
			}
			playing := &trackPipeline{
				decoder: decoder, stream: decoder, path: loadURL, ytdlSeek: true, knownDuration: time.Hour,
				format: beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
			}
			p.current = playing
			done := make(chan error, 1)
			go func() { done <- p.SeekYTDL(time.Minute) }()
			fake.WaitRuns(t, 1)

			timed(t, tt.name, actsWithin, func() { tt.cancel(p) })
			fake.WaitLive(t, 0, stopsWithin)
			finished(t, "the seek", done)
			p.mu.Lock()
			current := p.current
			p.mu.Unlock()
			if current != nil && current != playing {
				t.Error("the cancelled seek replaced what was playing")
			}
			fake.RequireNoCopy(t)
		})
	}
}
