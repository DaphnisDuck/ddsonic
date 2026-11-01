package player

// ddsonic: playback's yt-dlp runs against ambient yt-dlp configuration.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestProbeYTDLDurationIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	ytdlcookies.SetForHost("youtube.com", "firefox")
	t.Cleanup(func() { ytdlcookies.SetForHost("youtube.com", "") })
	fake := ytdlptest.Install(t, "212.0\n")

	if got := probeYTDLDuration(context.Background(), "https://www.youtube.com/watch?v=abc"); got != 212*time.Second {
		t.Errorf("probeYTDLDuration = %v, want 3m32s", got)
	}

	runs := fake.RequireIsolated(t)
	for _, want := range []string{
		"--skip-download --no-playlist --socket-timeout 10 --print duration",
		"--cookies-from-browser firefox",
		"-- https://www.youtube.com/watch?v=abc",
	} {
		if !strings.Contains(runs[0], want) {
			t.Errorf("yt-dlp ran with %q, want it to contain %q", runs[0], want)
		}
	}
}

// Playback asks yt-dlp for one stream on stdout. Ambient configuration could
// send it elsewhere (-o), pick another format, or mark the video watched.
func TestDecodeYTDLPipeIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	ytdlcookies.SetForHost("youtube.com", "firefox")
	t.Cleanup(func() { ytdlcookies.SetForHost("youtube.com", "") })
	fake := ytdlptest.Install(t, strings.Repeat("\x00", 1024))
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "ffmpeg"), "#!/bin/sh\ncat\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	y, _, err := decodeYTDLPipe(context.Background(), "https://www.youtube.com/watch?v=abc", beep.SampleRate(44100), 16, 0)
	if err != nil {
		t.Fatalf("decodeYTDLPipe: %v", err)
	}
	pcm, err := io.ReadAll(y.reader)
	if cerr := y.Close(); cerr != nil {
		t.Errorf("Close: %v", cerr)
	}
	if err != nil || len(pcm) != 1024 {
		t.Errorf("read %d bytes of audio, err %v; want the 1024 yt-dlp wrote", len(pcm), err)
	}

	runs := fake.RequireIsolated(t)
	for _, want := range []string{
		"-f bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio[protocol!=m3u8_native][protocol!=m3u8]/bestaudio/best",
		"--no-playlist --quiet --no-warnings --socket-timeout 15 -o -",
		"--cookies-from-browser firefox",
		"-- https://www.youtube.com/watch?v=abc",
	} {
		if !strings.Contains(runs[0], want) {
			t.Errorf("yt-dlp ran with %q, want it to contain %q", runs[0], want)
		}
	}
}
