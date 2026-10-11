package player

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
)

// The speaker reads a yt-dlp page under its lock. A stalled download must
// not hold that read, or every control waits until yt-dlp gets data or gives
// up. (Upstream 70e5f79e.)
func TestBuildYTDLPipelineStreamDoesNotWaitForAStalledDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	dir := t.TempDir()
	// yt-dlp sends one frame and then stalls.
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nprintf '\\000\\100\\000\\300'\nexec sleep 30\n")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nexec cat\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := &Player{sr: beep.SampleRate(100), bitDepth: 16}

	tp, err := p.buildYTDLPipeline(p.beginYTDLLoad(ytdlPlayLoad, 0, false), "https://www.youtube.com/watch?v=stall", 0)
	if err != nil {
		t.Fatalf("buildYTDLPipeline() error = %v", err)
	}
	defer tp.close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// More than one frame: a direct pipe read waits for the second one.
		out := make([][2]float64, 8)
		if n, ok := tp.stream.Stream(out); !ok || n != len(out) {
			t.Errorf("Stream() = (%d, %v), want (%d, true)", n, ok, len(out))
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		tp.close()
		<-done
		t.Fatal("Stream waited for the stalled download")
	}
	if pos := ytdlPlayedPosition(tp); pos != 0 {
		t.Fatalf("position = %v after silence, want 0", pos)
	}
}

// A seek by restart starts from the audio that played, not from what the
// prefetch has read ahead. (Upstream 70e5f79e.)
func TestYTDLPlayedPosition(t *testing.T) {
	tests := []struct {
		name     string
		prefetch *livePrefetchStreamer
		want     time.Duration
	}{
		{name: "direct decoder", want: 3*time.Second + time.Minute},
		{name: "prefetched decoder", prefetch: &livePrefetchStreamer{sampleRate: 100, consumed: 100}, want: time.Second + time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The decoder has read 3 s of audio.
			decoder := &ytdlPipeStreamer{state: newPipeStreamState(300)}
			cur := &trackPipeline{
				decoder:      decoder,
				stream:       decoder,
				format:       beep.Format{SampleRate: 100, NumChannels: 2, Precision: 2},
				ytdlSeek:     true,
				streamOffset: time.Minute,
				livePrefetch: tt.prefetch,
			}
			if got := ytdlPlayedPosition(cur); got != tt.want {
				t.Fatalf("ytdlPlayedPosition() = %v, want %v", got, tt.want)
			}
			p := &Player{current: cur}
			if got := p.Position(); got != tt.want {
				t.Fatalf("Position() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A prefetched yt-dlp track that ends cleanly returns a short last read, so
// the gapless next track fills the rest with no silence between them.
// (Upstream bcb4629e, needed once yt-dlp pages are prefetched.)
func TestPrefetchedYTDLTrackEndsWithShortRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	dir := t.TempDir()
	// 100 stereo 16-bit frames, then a clean exit.
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nhead -c 400 /dev/zero\n")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nexec cat\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	p := &Player{sr: beep.SampleRate(100), bitDepth: 16}

	tp, err := p.buildYTDLPipeline(p.beginYTDLLoad(ytdlPlayLoad, 0, false), "https://www.youtube.com/watch?v=short", 0)
	if err != nil {
		t.Fatalf("buildYTDLPipeline() error = %v", err)
	}
	defer tp.close()
	// Let the prefetch read the whole track.
	deadline := time.Now().Add(5 * time.Second)
	for {
		tp.livePrefetch.mu.Lock()
		done := tp.livePrefetch.done
		tp.livePrefetch.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the prefetch never saw the end of the track")
		}
		time.Sleep(5 * time.Millisecond)
	}
	out := make([][2]float64, 512)
	n, ok := tp.stream.Stream(out)
	if n != 100 || !ok {
		t.Errorf("last read = (%d, %v), want (100, true): the rest would be silence before the next track", n, ok)
	}
}
