package resolve

// ddsonic: the resolver's yt-dlp runs leave no copy of the browser's cookies.

import (
	"context"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestResolveYTDLBatchLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, `{"url":"https://example.com/a","title":"A","duration":61}`+"\n")
	tracks, err := ResolveYTDLBatch("https://example.com/playlist", 0, 0, "chrome")
	if err != nil || len(tracks) != 1 {
		t.Fatalf("ResolveYTDLBatch = %+v, %v; want one track", tracks, err)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

// Stop, or leaving a list that is still loading, cancels the read.
func TestResolveYTDLCancelledLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := resolveYTDLRangeContext(ctx, "https://example.com/playlist", 0, 0, "chrome")
		done <- err
	}()
	fake.WaitCopying(t)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a cancelled read reported no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled read did not return")
	}
	if !fake.Interrupted(t) {
		t.Error("yt-dlp was killed outright, want it interrupted first so that it cleans up after itself")
	}
	fake.RequireNoCopy(t)
}

func TestFetchUserPlaylistsLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, `{"id":"PL1","title":"Mine","_type":"url"}`+"\n")
	lists, err := FetchUserPlaylists("firefox")
	if err != nil || len(lists) != 1 {
		t.Fatalf("FetchUserPlaylists = %+v, %v; want one playlist", lists, err)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

func TestDownloadYTDLLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, `{"title":"A","_filename":"/music/A.opus"}`)
	path, err := DownloadYTDL("https://example.com/a", "/music")
	if err != nil || path != "/music/A.opus" {
		t.Fatalf("DownloadYTDL = %q, %v; want /music/A.opus", path, err)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}
