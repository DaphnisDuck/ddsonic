package resolve

// ddsonic: the resolver's yt-dlp runs against ambient yt-dlp configuration.

import (
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

func requireYTDLRun(t *testing.T, fake *ytdlptest.Fake, wants ...string) {
	t.Helper()
	runs := fake.RequireIsolated(t)
	if len(runs) != 1 {
		t.Fatalf("yt-dlp ran %d times, want 1: %q", len(runs), runs)
	}
	for _, want := range wants {
		if !strings.Contains(runs[0], want) {
			t.Errorf("yt-dlp ran with %q, want it to contain %q", runs[0], want)
		}
	}
}

func TestResolveYTDLBatchIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	fake := ytdlptest.Install(t, `{"url":"https://example.com/a","title":"A","duration":61}`+"\n")

	tracks, err := ResolveYTDLBatch("https://example.com/playlist", 20, 10, "chrome")
	if err != nil || len(tracks) != 1 || tracks[0].Title != "A" {
		t.Fatalf("ResolveYTDLBatch = %+v, %v; want the one track A", tracks, err)
	}
	requireYTDLRun(t, fake,
		"--flat-playlist -j --socket-timeout 15",
		"--cookies-from-browser chrome",
		"--playlist-start 21 --playlist-end 30 -- https://example.com/playlist")
}

func TestFetchUserPlaylistsIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	fake := ytdlptest.Install(t, `{"id":"PL1","title":"Mine","_type":"url"}`+"\n")

	lists, err := FetchUserPlaylists("firefox")
	if err != nil || len(lists) != 1 || lists[0].Name != "Mine" {
		t.Fatalf("FetchUserPlaylists = %+v, %v; want the one playlist Mine", lists, err)
	}
	requireYTDLRun(t, fake,
		"--flat-playlist -j --socket-timeout 15",
		"--cookies-from-browser firefox",
		"-- https://www.youtube.com/feed/playlists")
}

// A download writes where ddsonic says (-o); ambient configuration could
// move it, convert it, or run a command on it.
func TestDownloadYTDLIgnoresAmbientYTDLPConfiguration(t *testing.T) {
	SetYTDLCookiesForHost("example.com", "firefox")
	t.Cleanup(func() { SetYTDLCookiesForHost("example.com", "") })
	fake := ytdlptest.Install(t, `{"title":"A","_filename":"/music/A.opus"}`)

	path, err := DownloadYTDL("https://example.com/a", "/music")
	if err != nil || path != "/music/A.opus" {
		t.Fatalf("DownloadYTDL = %q, %v; want /music/A.opus", path, err)
	}
	requireYTDLRun(t, fake,
		"-f bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio --no-playlist --print-json -o /music/%(artist,uploader)s - %(title)s.%(ext)s",
		"--cookies-from-browser firefox",
		"-- https://example.com/a")
}
