package resolve

// ddsonic: what a yt-dlp run does when its temporary directory cannot be
// made, or cannot be removed.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlp"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// Without a directory of its own, a run would copy the cookies into
// whatever temporary directory yt-dlp falls back to, where a kill leaves
// them. It is not started.
func TestResolveYTDLIsNotRunWithoutATemporaryDirectoryOfItsOwn(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, `{"url":"https://example.com/a","title":"A"}`+"\n")
	fake.DenyTemp(t)

	tracks, err := ResolveYTDLBatch("https://example.com/playlist", 0, 0, "chrome")
	if err == nil {
		t.Errorf("ResolveYTDLBatch = %+v, nil; want an error", tracks)
	}
	if runs := fake.Runs(t); len(runs) != 0 {
		t.Errorf("yt-dlp was run without a temporary directory of its own: %q", runs)
	}
}

// A run whose directory cannot be removed has left something behind. That
// is not reported as a read that went well.
func TestResolveYTDLReportsATemporaryDirectoryItCouldNotRemove(t *testing.T) {
	ytdlptest.InstallCopier(t, ytdlptest.Unremovable, `{"url":"https://example.com/a","title":"A"}`+"\n")

	tracks, err := ResolveYTDLBatch("https://example.com/playlist", 0, 0, "chrome")
	if err == nil {
		t.Errorf("ResolveYTDLBatch = %+v, nil; want an error naming the directory that is left", tracks)
	}
}

// Quitting while a track is being saved: nothing else would stop that run,
// which has no context of its own. The save is interrupted and says so, and
// no copy of the cookies is left.
func TestQuittingDuringADownloadStopsItAndLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	t.Cleanup(ytdlp.Reopen)
	done := make(chan error, 1)
	go func() {
		_, err := DownloadYTDL("https://example.com/a", t.TempDir())
		done <- err
	}()
	fake.WaitCopying(t)

	start := time.Now()
	if err := ytdlp.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("quitting waited %v for the download to stop", took)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the interrupted download reported no error")
		}
	default:
		t.Error("Shutdown returned before the download had stopped")
	}
	if !fake.Interrupted(t) {
		t.Error("yt-dlp was not interrupted")
	}
	fake.RequireNoCopy(t)

	// And none is started once ddsonic is quitting.
	if _, err := DownloadYTDL("https://example.com/b", t.TempDir()); !errors.Is(err, ytdlp.ErrShutdown) {
		t.Errorf("DownloadYTDL while quitting = %v, want ErrShutdown", err)
	}
}

// Quitting while a list is still loading: its read has a timeout of its own,
// minutes away.
func TestQuittingDuringAReadStopsItAndLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	t.Cleanup(ytdlp.Reopen)
	done := make(chan error, 1)
	go func() {
		_, err := ResolveYTDLBatch("https://example.com/playlist", 0, 0, "chrome")
		done <- err
	}()
	fake.WaitCopying(t)

	if err := ytdlp.Shutdown(); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the interrupted read reported no error")
		}
	default:
		t.Error("Shutdown returned before the read had stopped")
	}
	if !fake.Interrupted(t) {
		t.Error("yt-dlp was not interrupted")
	}
	fake.RequireNoCopy(t)
}

// Quitting during a save whose directory cannot then be removed. The save's
// own error goes to a screen that is no longer there, so quitting reports it.
func TestQuittingReportsADownloadsDirectoryThatCouldNotBeRemoved(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.HangUnremovable, "")
	t.Cleanup(ytdlp.Reopen)
	done := make(chan error, 1)
	go func() {
		_, err := DownloadYTDL("https://example.com/a", t.TempDir())
		done <- err
	}()
	fake.WaitCopying(t)

	err := ytdlp.Shutdown()
	if err == nil || !strings.Contains(err.Error(), "could not be removed") {
		t.Errorf("Shutdown = %v, want it to report the directory that is left", err)
	}
	select {
	case derr := <-done:
		if derr == nil || !strings.Contains(derr.Error(), "could not be removed") {
			t.Errorf("DownloadYTDL = %v, want it to report the directory too", derr)
		}
	default:
		t.Error("Shutdown returned before the download had stopped and tried to clean up")
	}
}
