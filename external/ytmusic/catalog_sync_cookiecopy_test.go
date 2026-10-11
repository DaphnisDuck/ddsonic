// ddsonic: the catalog's yt-dlp reads leave no copy of the browser's cookies.

package ytmusic

import (
	"context"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

var copyRef = catalog.Ref{Provider: catalog.YouTube, ProviderID: "abc123"}

func TestCookieCatalogReadLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, `{"track":"Song","album":"Album"}`)
	meta, err := NewCookieCatalog("firefox").TrackMetadata(context.Background(), copyRef)
	if err != nil || meta.Title != "Song" {
		t.Fatalf("TrackMetadata = %+v, %v; want Song", meta, err)
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}

// A sync is cancelled when ddsonic quits or the source is refreshed. yt-dlp
// may then be in the middle of reading the browser's cookies.
func TestCookieCatalogReadCancelledLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Hang, "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewCookieCatalog("firefox").TrackMetadata(ctx, copyRef)
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
