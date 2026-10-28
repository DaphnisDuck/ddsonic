package netease

// ddsonic: the cookie export's yt-dlp run leaves no copy of the browser's
// cookies behind.

import (
	"context"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func TestExtractBrowserCookieHeaderLeavesNoCookieCopy(t *testing.T) {
	fake := ytdlptest.InstallCopier(t, ytdlptest.Finish, "")
	// The fake exports nothing, so there is no cookie file to read.
	if _, err := extractBrowserCookieHeader(context.Background(), "firefox"); err == nil {
		t.Fatal("extractBrowserCookieHeader succeeded without a cookie file")
	}
	fake.RequireNoCopy(t)
	fake.RequireOwnTemp(t)
}
