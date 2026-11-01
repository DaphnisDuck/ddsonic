package player

// ddsonic: what the designated review found in the first form of the
// loading-track cancellation.

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// A track that yt-dlp cannot load fails with yt-dlp's reason, whichever way
// it was asked for. Ending the load's context to clean up after the failure
// must not turn the failure into "superseded", which is reported as success
// to a caller with a generation and as "no longer wanted" to one without.
func TestAYTDLLoadThatFailsReportsYTDLPsReason(t *testing.T) {
	tests := []struct {
		name string
		load func(*Player) error
	}{
		{"PlayYTDL", func(p *Player) error { return p.PlayYTDL(loadURL, time.Minute) }},
		{"PlayYTDL, duration unknown", func(p *Player) error { return p.PlayYTDL(loadURL, 0) }},
		{"PlayYTDLForGeneration", func(p *Player) error {
			p.SetPlaybackGeneration(1)
			return p.PlayYTDLForGeneration(loadURL, time.Minute, 1)
		}},
		{"PlayYTDLForGeneration, duration unknown", func(p *Player) error {
			p.SetPlaybackGeneration(1)
			return p.PlayYTDLForGeneration(loadURL, 0, 1)
		}},
		{"PreloadYTDL", func(p *Player) error { return p.PreloadYTDL(loadURL, time.Minute) }},
		{"PreloadYTDLForGeneration", func(p *Player) error {
			return p.PreloadYTDLForGeneration(loadURL, time.Minute, p.BeginPreload())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Fail, "")
			installPassThroughFFmpeg(t)
			p := newLoadPlayer()

			err := tt.load(p)
			if err == nil {
				t.Fatal("a load that failed reported success: the UI would show the track as playing, or preload it again on every tick")
			}
			if !strings.Contains(err.Error(), "Video unavailable") {
				t.Errorf("error = %q, want yt-dlp's reason", err)
			}
			if strings.Contains(err.Error(), errYTDLLoadCancelled.Error()) {
				t.Errorf("error = %q: a failure is not a track that is no longer wanted", err)
			}
			if p.IsPlaying() {
				t.Error("the player is playing")
			}
			fake.WaitLive(t, 0, stopsWithin)
			fake.WaitNoCopy(t, stopsWithin)
		})
	}
}

// A preloaded track that the audio thread has already made the playing one
// is no longer a preload: dropping the preload must not end it. The caller
// that loaded it may not have returned yet.
func TestAPreloadThatBecameThePlayingTrackIsNotCancelledAsAPreload(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, strings.Repeat("\x00", 4096))
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	gen := p.BeginPreload()

	// What PreloadYTDLForGeneration does, stopped just after the install.
	load := p.beginYTDLLoad(ytdlPreloadLoad, gen, true)
	tp, err := p.buildYTDLPipeline(load, loadURL, 0)
	if err != nil {
		t.Fatalf("buildYTDLPipeline: %v", err)
	}
	if err := p.preloadPipelineForGeneration(tp, gen); err != nil {
		t.Fatalf("preloadPipelineForGeneration: %v", err)
	}
	// The playing track ends; the audio thread moves on to the preloaded one.
	p.handleGaplessSwap(tp.gaplessToken)
	p.mu.Lock()
	promoted := p.current == tp
	p.mu.Unlock()
	if !promoted {
		t.Fatal("the preloaded track was not promoted")
	}
	// The UI starts the new track, which drops whatever was preloaded.
	p.ClearPreload()

	if err := tp.load.ctx.Err(); err != nil {
		t.Errorf("the playing track's load was ended as a preload: %v", err)
	}
	p.mu.Lock()
	still := p.current == tp
	p.mu.Unlock()
	if !still {
		t.Error("the playing track was replaced")
	}
	p.endYTDLLoad(load)
	p.Stop()
	fake.WaitLive(t, 0, stopsWithin)
	fake.WaitNoCopy(t, stopsWithin)
}

// A request made, then Stop, then the request's goroutine gets to run: it
// must start nothing. Stop cannot cancel a load that has not registered yet,
// so the load has to see that Stop came after its request.
func TestStopBeforeAYTDLLoadRegistersStillStopsIt(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(1) // the UI asks for the track ...
	p.Stop()                   // ... and stops, without declaring another request

	done := make(chan error, 1)
	go func() { done <- p.PlayYTDLForGeneration(loadURL, time.Minute, 1) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the stopped request = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		p.Stop()
		<-done
		t.Error("a request made before Stop started loading after it")
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was started for a request that Stop had ended: %q", runs)
	}
	if p.IsPlaying() {
		t.Error("the player is playing after Stop")
	}

	// A request declared after the Stop loads as usual.
	p.SetPlaybackGeneration(2)
	go func() { done <- p.PlayYTDLForGeneration(loadURL, time.Minute, 2) }()
	fake.WaitRuns(t, 1)
	p.Stop()
	finished(t, "the load", done)
	fake.WaitLive(t, 0, stopsWithin)
}

// The same window in a seek: it has noted what it seeks in, and before it
// registers its load, Stop or a newer seek arrives.
func TestStopOrANewerSeekBeforeAYTDLSeekRegistersStillStopsIt(t *testing.T) {
	tests := []struct {
		name string
		then func(*Player)
	}{
		{"Stop", func(p *Player) { p.Stop() }},
		{"a newer seek", func(p *Player) { p.CancelSeekYTDL() }},
		{"the next track, then Stop", func(p *Player) { p.SetPlaybackGeneration(7); p.Stop() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
			installPassThroughFFmpeg(t)
			p := newLoadPlayer()
			decoder := &ytdlPipeStreamer{
				state:     newPipeStreamState(300),
				ytdlCmd:   exec.Command("true"),
				ffmpegCmd: exec.Command("true"),
				pipe:      io.NopCloser(bytes.NewReader(nil)),
			}
			p.current = &trackPipeline{
				decoder: decoder, stream: decoder, path: loadURL, ytdlSeek: true, knownDuration: time.Hour,
				format: beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
			}
			ytdlSeekCaptured = func() { tt.then(p) }
			t.Cleanup(func() { ytdlSeekCaptured = func() {} })

			done := make(chan error, 1)
			go func() { done <- p.SeekYTDL(time.Minute) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				p.Stop()
				<-done
				t.Error("the seek started loading after it had been made unwanted")
			}
			if runs := fake.Runs(t); len(runs) != 0 {
				t.Errorf("yt-dlp was started for a seek that was no longer wanted: %q", runs)
			}
		})
	}
}

// stopHeldDecoder is what is playing in the test below: its interrupt, which
// Stop calls part-way through, waits until the test lets it go on.
type stopHeldDecoder struct {
	*playbackTestDecoder
	reached, resume chan struct{}
}

func (d *stopHeldDecoder) interrupt() {
	close(d.reached)
	<-d.resume
}

// A seek that begins while Stop is under way: Stop has counted itself and
// has not yet cleared what is playing. The seek must not see the new count
// together with the old track, or it passes for a seek made after the Stop
// in the track that Stop is ending, and loads once Stop has returned.
func TestASeekThatBeginsDuringStopDoesNotLoadAfterIt(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(1)
	inStop, letStopGoOn := make(chan struct{}), make(chan struct{})
	decoder := &stopHeldDecoder{newPlaybackTestDecoder(), inStop, letStopGoOn}
	p.current = &trackPipeline{
		decoder: decoder, stream: decoder, path: loadURL, ytdlSeek: true, knownDuration: time.Hour,
		format: beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
	}
	// The seek, once it has noted what it seeks in, waits for Stop to be
	// over before it registers its load.
	stopped := make(chan struct{})
	ytdlSeekCaptured = func() { <-stopped }
	t.Cleanup(func() { ytdlSeekCaptured = func() {} })

	go func() {
		p.Stop()
		close(stopped)
	}()
	<-inStop // Stop has counted itself; the track is still the current one
	seek := make(chan error, 1)
	go func() { seek <- p.SeekYTDL(time.Minute) }()
	// Give the seek time to note the track and the count, if it can.
	time.Sleep(100 * time.Millisecond)
	close(letStopGoOn)
	<-stopped

	select {
	case err := <-seek:
		if err != nil {
			t.Errorf("the seek = %v, want nil: it was overtaken by Stop", err)
		}
	case <-time.After(2 * time.Second):
		p.Stop()
		<-seek
		t.Error("a seek that began during Stop started loading after Stop had returned")
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was started for a seek that Stop had overtaken: %q", runs)
	}
	if p.IsPlaying() {
		t.Error("the player is playing after Stop")
	}
	fake.WaitLive(t, 0, stopsWithin)
}

// A seek has noted the track it seeks in; before it mutes it, that track is
// stopped and another one starts. The seek must not mute the new track: its
// load is refused and nothing would ever restore the sound, while the player
// goes on saying it plays.
func TestAnOvertakenSeekDoesNotMuteTheTrackThatReplacedItsOwn(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.started, p.ctrl = true, &beep.Ctrl{}
	format := beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2}
	old := newPlaybackTestDecoder()
	p.current = &trackPipeline{decoder: old, stream: old, path: loadURL, ytdlSeek: true, knownDuration: time.Hour, format: format}
	p.SetPlaybackGeneration(1)

	noted, goOn := make(chan struct{}), make(chan struct{})
	ytdlSeekNoted = func() {
		close(noted)
		<-goOn
	}
	t.Cleanup(func() { ytdlSeekNoted = func() {} })
	seek := make(chan error, 1)
	go func() { seek <- p.SeekYTDL(time.Minute) }()
	<-noted

	// What the UI does to play another track: stop, drop the seek, declare
	// the request, and the new track is installed.
	p.Stop()
	p.CancelSeekYTDL()
	p.SetPlaybackGeneration(2)
	fresh := newPlaybackTestDecoder()
	replacement := &trackPipeline{decoder: fresh, stream: fresh, path: "replacement.wav", format: format}
	p.suspended = false // as after the first Play; nothing here opens an audio device
	if err := p.playPipelineForGeneration(replacement, 2); err != nil {
		t.Fatalf("playPipelineForGeneration: %v", err)
	}

	close(goOn)
	if err := finished(t, "the overtaken seek", seek); err != nil {
		t.Errorf("the overtaken seek = %v, want nil", err)
	}
	p.gapless.mu.Lock()
	sounding := p.gapless.current
	p.gapless.mu.Unlock()
	if sounding != replacement.stream {
		t.Errorf("the speaker's source is %v, want the track that is playing: the overtaken seek muted it", sounding)
	}
	if !p.IsPlaying() {
		t.Error("the player is not playing the replacement")
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was started for the overtaken seek: %q", runs)
	}
	p.suspended = true
	p.Stop()
}
