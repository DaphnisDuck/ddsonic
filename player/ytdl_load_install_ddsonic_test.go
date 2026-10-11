package player

// ddsonic: the last guard of a cancelled YouTube load.

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// A load that got its audio at the very moment it was cancelled must not
// become the playing track.
func TestACancelledYTDLLoadThatIsReadyIsNotInstalled(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, strings.Repeat("\x00", 4096))
	installPassThroughFFmpeg(t)
	p := newLoadPlayer()
	p.SetPlaybackGeneration(1)

	load := p.beginYTDLLoad(ytdlPlayLoad, 1, true)
	tp, err := p.buildYTDLPipeline(load, loadURL, 0)
	if err != nil {
		t.Fatalf("buildYTDLPipeline: %v", err)
	}
	// Ready, and now no longer wanted. The generation is still current, so
	// only the load's own cancellation can refuse it.
	p.Stop()
	if err := p.playPipelineForGeneration(tp, 1); err != nil {
		t.Errorf("installing a cancelled load = %v", err)
	}
	p.mu.Lock()
	current := p.current
	p.mu.Unlock()
	if current != nil || p.IsPlaying() {
		t.Error("a cancelled load became the playing track")
	}
	// The refused pipeline is closed on a goroutine of its own.
	fake.WaitLive(t, 0, stopsWithin)
	fake.WaitNoCopy(t, stopsWithin)
	fake.RequireNoCopy(t)
}
