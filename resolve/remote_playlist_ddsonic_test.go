package resolve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// serveRemotePlaylist serves body at /final/<name>, reachable only through a
// redirect from /start, so a test can tell the final URL from the first one.
func serveRemotePlaylist(t *testing.T, name, body string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final/"+name, http.StatusFound)
	})
	mux.HandleFunc("/final/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	old := httpClient
	httpClient = srv.Client()
	t.Cleanup(func() { httpClient = old })
	return srv.URL
}

func trackPaths(tracks []playlist.Track) []string {
	paths := make([]string, len(tracks))
	for i, tr := range tracks {
		paths[i] = tr.Path
	}
	return paths
}

func assertPaths(t *testing.T, got []playlist.Track, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d tracks %q, want %d %q", len(got), trackPaths(got), len(want), want)
	}
	for i, w := range want {
		if got[i].Path != w {
			t.Errorf("track %d path = %q, want %q", i, got[i].Path, w)
		}
	}
}

// A remote playlist must not make playback open a local file: every track it
// yields is an http(s) URL, relative entries are resolved against the URL the
// playlist was finally served from, and other entries are dropped.
func TestRemoteM3UEntriesNeverSelectLocalPaths(t *testing.T) {
	base := serveRemotePlaylist(t, "list.m3u", "#EXTM3U\n"+
		"#EXTINF:10,Relative\nsong.mp3\n"+
		"#EXTINF:10,Absolute\n/etc/passwd\n"+
		"#EXTINF:10,Parent\n../up.mp3\n"+
		"#EXTINF:10,Drive\nC:\\music\\x.mp3\n"+
		"#EXTINF:10,Elsewhere\nhttps://elsewhere.example/ok.mp3\n"+
		"#EXTINF:10,SSH\nssh://evil/x\n"+
		"#EXTINF:10,File\nfile:///etc/passwd\n"+
		"#EXTINF:10,Upper\nHTTPS://elsewhere.example/upper.mp3\n"+
		"#EXTINF:10,Opaque\nhttp:relative.mp3\n"+
		"#EXTINF:10,Hostless\nhttp:/etc/passwd\n"+
		"#EXTINF:10,EmptyHost\nhttps:///etc/shadow\n")

	tracks, err := resolveM3U(base + "/start")
	if err != nil {
		t.Fatalf("resolveM3U: %v", err)
	}
	assertPaths(t, tracks, []string{
		base + "/final/song.mp3",
		base + "/etc/passwd",
		base + "/up.mp3",
		"https://elsewhere.example/ok.mp3",
		"https://elsewhere.example/upper.mp3",
	})
	for _, tr := range tracks {
		if !playlist.IsURL(tr.Path) || !tr.Stream {
			t.Errorf("track %q: IsURL=%v Stream=%v, want an http(s) stream", tr.Path, playlist.IsURL(tr.Path), tr.Stream)
		}
	}
	if len(tracks) > 0 && tracks[0].Title != "Relative" {
		t.Errorf("title = %q, want the EXTINF title kept", tracks[0].Title)
	}
}

func TestRemotePLSEntriesNeverSelectLocalPaths(t *testing.T) {
	base := serveRemotePlaylist(t, "list.pls", "[playlist]\n"+
		"File1=song.mp3\nTitle1=Relative\nLength1=10\n"+
		"File2=/home/user/x.mp3\nTitle2=Absolute\nLength2=10\n"+
		"File3=C:\\music\\x.mp3\nTitle3=Drive\nLength3=10\n"+
		"File4=http://elsewhere.example/ok.mp3\nTitle4=Elsewhere\nLength4=10\n"+
		"File5=ssh://evil/x\nTitle5=SSH\nLength5=10\n"+
		"File6=HTTP://elsewhere.example/upper.mp3\nTitle6=Upper\nLength6=10\n"+
		"File7=http:relative.mp3\nTitle7=Opaque\nLength7=10\n"+
		"File8=http:///home/user/x.mp3\nTitle8=EmptyHost\nLength8=10\n"+
		"NumberOfEntries=8\nVersion=2\n")

	tracks, err := resolvePLS(base + "/start")
	if err != nil {
		t.Fatalf("resolvePLS: %v", err)
	}
	assertPaths(t, tracks, []string{
		base + "/final/song.mp3",
		base + "/home/user/x.mp3",
		"http://elsewhere.example/ok.mp3",
		"http://elsewhere.example/upper.mp3",
	})
	for _, tr := range tracks {
		if !playlist.IsURL(tr.Path) || !tr.Stream {
			t.Errorf("track %q: IsURL=%v Stream=%v, want an http(s) stream", tr.Path, playlist.IsURL(tr.Path), tr.Stream)
		}
	}
}

// Local playlists keep resolving their entries against the playlist file's
// directory; the remote rule does not apply to them.
func TestLocalPlaylistEntriesStayLocal(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "abs.flac")

	m3u := filepath.Join(dir, "list.m3u")
	body := "#EXTM3U\n#EXTINF:10,A\na.mp3\nsub/b.mp3\n" + abs + "\nhttps://example.com/c.mp3\n"
	if err := os.WriteFile(m3u, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tracks, err := LocalPlaylist(m3u)
	if err != nil {
		t.Fatalf("LocalPlaylist(m3u): %v", err)
	}
	assertPaths(t, tracks, []string{
		filepath.Join(dir, "a.mp3"),
		filepath.Join(dir, "sub", "b.mp3"),
		abs,
		"https://example.com/c.mp3",
	})

	pls := filepath.Join(dir, "list.pls")
	body = "[playlist]\nFile1=a.mp3\nFile2=" + abs + "\nFile3=https://example.com/c.mp3\nNumberOfEntries=3\nVersion=2\n"
	if err := os.WriteFile(pls, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	tracks, err = LocalPlaylist(pls)
	if err != nil {
		t.Fatalf("LocalPlaylist(pls): %v", err)
	}
	assertPaths(t, tracks, []string{"a.mp3", abs, "https://example.com/c.mp3"})
}
