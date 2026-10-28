package player

// ddsonic: what playback does when yt-dlp's temporary directory cannot be
// made, or cannot be removed.

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestDecodeYTDLPipeIsNotStartedWithoutATemporaryDirectoryOfItsOwn(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	installPassThroughFFmpeg(t)
	fake.DenyTemp(t)

	y, _, err := decodeYTDLPipe(context.Background(), "https://www.youtube.com/watch?v=abc", beep.SampleRate(44100), 16, 0)
	if err == nil {
		_ = y.Close()
		t.Fatal("decodeYTDLPipe started without a temporary directory for yt-dlp")
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was run without a temporary directory of its own: %q", runs)
	}
	if got := probeYTDLDuration(context.Background(), "https://www.youtube.com/watch?v=abc"); got != 0 || len(fake.Runs(t)) != 0 {
		t.Errorf("probeYTDLDuration = %v after %d runs, want 0 and no run", got, len(fake.Runs(t)))
	}
}

// A track that played, whose yt-dlp left something that cannot be removed:
// Close says so, and so does the footer, since most callers drop Close's
// error.
func TestDecodeYTDLPipeReportsATemporaryDirectoryItCouldNotRemove(t *testing.T) {
	ytdlptest.InstallCopier(t, ytdlptest.Unremovable, strings.Repeat("\x00", 1024))
	installPassThroughFFmpeg(t)
	applog.Drain()

	y, _, err := decodeYTDLPipe(context.Background(), "https://www.youtube.com/watch?v=abc", beep.SampleRate(44100), 16, 0)
	if err != nil {
		t.Fatalf("decodeYTDLPipe: %v", err)
	}
	if pcm, err := io.ReadAll(y.reader); err != nil || len(pcm) != 1024 {
		t.Errorf("read %d bytes of audio, err %v; want the 1024 yt-dlp wrote", len(pcm), err)
	}
	err = y.Close()
	if err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Errorf("Close = %v, want it to report the directory that is left", err)
	}
	var told bool
	for _, e := range applog.Drain() {
		told = told || strings.Contains(e.Text, "could not be removed")
	}
	if !told {
		t.Error("the user was not told of the directory that is left")
	}
}
