package netease

// ddsonic: the cookie export's yt-dlp run against ambient yt-dlp
// configuration.

import (
	"context"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// The export names its own browser and cookie file. Ambient configuration
// naming others would export another account's cookies, or to another place.
func TestExtractBrowserCookieHeaderIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	fake := ytdlptest.Install(t, "")

	// The fake exports nothing, so there is no cookie file to read.
	if _, err := extractBrowserCookieHeader(context.Background(), "firefox"); err == nil {
		t.Fatal("extractBrowserCookieHeader succeeded without a cookie file")
	}

	runs := fake.RequireIsolated(t)
	for _, want := range []string{
		"--cookies-from-browser firefox --cookies ",
		"--flat-playlist --playlist-end 1 --socket-timeout 15 --print title -- " + probeURL,
	} {
		if !strings.Contains(runs[0], want) {
			t.Errorf("yt-dlp ran with %q, want it to contain %q", runs[0], want)
		}
	}
}
