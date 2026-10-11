package player

// ddsonic: playback's yt-dlp runs leave no copy of the browser's cookies.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestProbeYTDLDurationLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "212.0\n")
	if got := probeYTDLDuration(context.Background(), "https://www.youtube.com/watch?v=abc"); got != 212*time.Second {
		t.Errorf("probeYTDLDuration = %v, want 3m32s", got)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

func installPassThroughFFmpeg(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "ffmpeg"), "#!/bin/sh\ncat\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDecodeYTDLPipePlayedThroughLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, strings.Repeat("\x00", 1024))
	installPassThroughFFmpeg(t)

	y, _, err := decodeYTDLPipe(context.Background(), "https://www.youtube.com/watch?v=abc", beep.SampleRate(44100), 16, 0)
	if err != nil {
		t.Fatalf("decodeYTDLPipe: %v", err)
	}
	pcm, err := io.ReadAll(y.reader)
	if err != nil || len(pcm) != 1024 {
		t.Errorf("read %d bytes of audio, err %v; want the 1024 yt-dlp wrote", len(pcm), err)
	}
	if err := y.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

// Skipping a track that has just started stops yt-dlp at once, which is when
// it is reading the browser's cookies. Stopping stays immediate.
func TestDecodeYTDLPipeStoppedEarlyLeavesNoCookieCopy(t *testing.T) {
	for _, mode := range []string{ytdlptest.Hang, ytdlptest.Stubborn} {
		t.Run(mode, func(t *testing.T) {
			fake := ytdlptest.InstallCopier(t, mode, "")
			installPassThroughFFmpeg(t)

			y, _, err := decodeYTDLPipe(context.Background(), "https://www.youtube.com/watch?v=abc", beep.SampleRate(44100), 16, 0)
			if err != nil {
				t.Fatalf("decodeYTDLPipe: %v", err)
			}
			fake.WaitCopying(t)
			start := time.Now()
			if err := y.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
			if took := time.Since(start); took > time.Second {
				t.Errorf("Close took %v, want it immediate", took)
			}
			fake.RequireNoCopy(t)
		})
	}
}
