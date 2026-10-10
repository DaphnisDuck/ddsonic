// ddsonic: the catalog's yt-dlp reads against ambient yt-dlp configuration.

package ytmusic

import (
	"context"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// A catalog read runs yt-dlp signed in to the account. A yt-dlp.conf that
// marks videos watched must not make the read write to the account's history.
func TestCookieCatalogReadIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	fake := ytdlptest.Install(t, `{"track":"Song","artists":["Artist"],"album":"Album","release_year":2020}`)

	meta, err := NewCookieCatalog("brave+gnomekeyring").TrackMetadata(context.Background(), catalog.Ref{Provider: catalog.YouTube, ProviderID: "abc123"})
	if err != nil {
		t.Fatalf("TrackMetadata: %v", err)
	}
	if meta.Title != "Song" || meta.Album == nil || meta.Album.Title != "Album" || len(meta.Artists) != 1 {
		t.Errorf("TrackMetadata = %+v, want Song by Artist on Album", meta)
	}

	runs := fake.RequireIsolated(t)
	if len(runs) != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1: %q", len(runs), runs)
	}
	for _, want := range []string{
		"--socket-timeout 15 --no-warnings",
		"--cookies-from-browser brave+gnomekeyring",
		"-j --skip-download -- https://music.youtube.com/watch?v=abc123",
	} {
		if !strings.Contains(runs[0], want) {
			t.Errorf("yt-dlp ran with %q, want it to contain %q", runs[0], want)
		}
	}
}

// Without a browser the read is anonymous, and still its own.
func TestCookieCatalogReadWithoutBrowserIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	fake := ytdlptest.Install(t, `{}`)
	if _, err := NewCookieCatalog("").TrackMetadata(context.Background(), catalog.Ref{Provider: catalog.YouTube, ProviderID: "abc123"}); err != nil {
		t.Fatalf("TrackMetadata: %v", err)
	}
	if runs := fake.RequireIsolated(t); strings.Contains(runs[0], "--cookies") {
		t.Errorf("yt-dlp ran with %q, want no cookie option", runs[0])
	}
}
