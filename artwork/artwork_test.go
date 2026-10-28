package artwork

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

func TestResolve(t *testing.T) {
	for _, tt := range []struct {
		name  string
		track playlist.Track
		want  Ref
	}{
		{"spotify cover", playlist.Track{Path: "spotify:track:x", AlbumArtURL: "https://i.scdn.co/image/ab"},
			Ref{Key: "https://i.scdn.co/image/ab", URL: "https://i.scdn.co/image/ab"}},
		{"extracted embedded art", playlist.Track{Path: "/m/a.flac", AlbumArtURL: "file:///home/u/.local/share/ddsonic/album-art/f.jpg"},
			Ref{Key: "file:/home/u/.local/share/ddsonic/album-art/f.jpg", File: "/home/u/.local/share/ddsonic/album-art/f.jpg"}},
		{"local file", playlist.Track{Path: "/m/Album/01.flac"}, Ref{Key: "audio:/m/Album/01.flac", Audio: "/m/Album/01.flac"}},
		{"local file with % and :", playlist.Track{Path: "/m/100% Hits: Vol 1/01.mp3"}, Ref{Key: "audio:/m/100% Hits: Vol 1/01.mp3", Audio: "/m/100% Hits: Vol 1/01.mp3"}},
		{"relative local file", playlist.Track{Path: "Album/01.flac"}, Ref{Key: "audio:Album/01.flac", Audio: "Album/01.flac"}},
		{"another provider's URI", playlist.Track{Path: "tidal:track:1"}, Ref{}},
		{"spotify without art", playlist.Track{Path: "spotify:track:x"}, Ref{}},
		{"youtube", playlist.Track{Path: "https://music.youtube.com/watch?v=abc"}, Ref{}},
		{"radio", playlist.Track{Path: "http://stream.example/live", Stream: true}, Ref{}},
		{"nothing", playlist.Track{}, Ref{}},
	} {
		if got := Resolve(tt.track); got != tt.want {
			t.Errorf("%s: Resolve = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A remote image is fetched once: the second load comes from the cache,
// under a hashed name, without a request.
func TestRemoteLoadCaches(t *testing.T) {
	var requests atomic.Int32
	body := pngBytes(t, 40, 20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(body)
	}))
	defer srv.Close()
	l := &Loader{Dir: t.TempDir(), Client: srv.Client()}
	ref := Ref{Key: srv.URL + "/cover?size=300&x=../../etc", URL: srv.URL + "/cover?size=300&x=../../etc"}
	for range 2 {
		img, err := l.Load(context.Background(), ref)
		if err != nil || img.Bounds().Dx() != 40 {
			t.Fatalf("Load = %v, %v", img, err)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
	entries, _ := os.ReadDir(l.Dir)
	if len(entries) != 1 || filepath.Dir(l.cachePath(ref.URL)) != l.Dir || len(entries[0].Name()) != 64 {
		t.Errorf("cache holds %v, want one file named by the URL's hash", entries)
	}
}

// A failed or non-image download is an error and caches nothing.
func TestRemoteLoadFailures(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"404":       func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"not image": func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) },
	} {
		srv := httptest.NewServer(handler)
		l := &Loader{Dir: t.TempDir(), Client: srv.Client()}
		if _, err := l.Load(context.Background(), Ref{Key: srv.URL, URL: srv.URL}); err == nil {
			t.Errorf("%s: no error", name)
		}
		if entries, _ := os.ReadDir(l.Dir); len(entries) != 0 {
			t.Errorf("%s: cached %v", name, entries)
		}
		srv.Close()
	}
	l := &Loader{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Load(ctx, Ref{Key: "https://example.invalid/x", URL: "https://example.invalid/x"}); err == nil {
		t.Error("a cancelled load succeeded")
	}
}

// A local audio file without an embedded picture shows its folder's cover
// file; a folder without one has no artwork.
func TestLocalCoverFile(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "01 Track.flac")
	os.WriteFile(audio, []byte("not really audio"), 0o644)
	l := &Loader{}
	if _, err := l.Load(context.Background(), Ref{Key: "audio:" + audio, Audio: audio}); !errors.Is(err, ErrNone) {
		t.Errorf("no cover: %v, want ErrNone", err)
	}
	os.WriteFile(filepath.Join(dir, "Folder.JPG.txt"), []byte("x"), 0o644) // not an image name
	os.WriteFile(filepath.Join(dir, "Cover.PNG"), pngBytes(t, 30, 30), 0o644)
	img, err := l.Load(context.Background(), Ref{Key: "audio:" + audio, Audio: audio})
	if err != nil || img.Bounds().Dx() != 30 {
		t.Errorf("cover file: %v, %v", img, err)
	}
}

// An image declaring huge dimensions is refused before it is decoded.
func TestHugeImageRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cover.png")
	os.WriteFile(path, pngBytes(t, maxPixels+1, 1), 0o644)
	if _, err := (&Loader{}).Load(context.Background(), Ref{Key: "file:" + path, File: path}); err == nil {
		t.Error("a 8001-pixel-wide image loaded")
	}
}

func TestScaleKeepsAspect(t *testing.T) {
	for _, tt := range []struct{ w, h, ww, wh int }{
		{1000, 1000, 512, 512}, {1200, 600, 512, 256}, {300, 900, 170, 512}, {300, 300, 300, 300},
	} {
		got := scale(image.NewRGBA(image.Rect(0, 0, tt.w, tt.h)), maxSide).Bounds()
		if got.Dx() != tt.ww || got.Dy() != tt.wh {
			t.Errorf("scale(%dx%d) = %dx%d, want %dx%d", tt.w, tt.h, got.Dx(), got.Dy(), tt.ww, tt.wh)
		}
	}
}

func TestTrimRemovesOldest(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"old", "mid", "new"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, make([]byte, 100), 0o600)
		mt := testTime.Add(timeStep * time.Duration(i))
		os.Chtimes(p, mt, mt)
	}
	trim(dir, 200)
	if _, err := os.Stat(filepath.Join(dir, "old")); err == nil {
		t.Error("the oldest file stayed")
	}
	if _, err := os.Stat(filepath.Join(dir, "new")); err != nil {
		t.Error("the newest file went")
	}
}

var (
	testTime = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	timeStep = time.Hour
)

// A cached file that no longer decodes is dropped and fetched again.
func TestCorruptCacheRefetched(t *testing.T) {
	var requests atomic.Int32
	body := pngBytes(t, 10, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Write(body)
	}))
	defer srv.Close()
	l := &Loader{Dir: t.TempDir(), Client: srv.Client()}
	ref := Ref{Key: srv.URL, URL: srv.URL}
	os.WriteFile(l.cachePath(ref.URL), []byte("damaged"), 0o600)
	if _, err := l.Load(context.Background(), ref); err == nil {
		t.Fatal("a damaged cache file decoded")
	}
	if _, err := l.Load(context.Background(), ref); err != nil || requests.Load() != 1 {
		t.Errorf("after eviction: %v, %d requests; want one fetch", err, requests.Load())
	}
}

// A symlinked cover file counts; a FIFO in place of an audio file is not
// opened (it would block).
func TestLocalSymlinkAndFIFO(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(t.TempDir(), "art.png")
	os.WriteFile(real, pngBytes(t, 20, 20), 0o644)
	os.Symlink(real, filepath.Join(dir, "cover.png"))
	audio := filepath.Join(dir, "01.flac")
	os.WriteFile(audio, []byte("x"), 0o644)
	if img, err := (&Loader{}).Load(context.Background(), Ref{Key: "audio:" + audio, Audio: audio}); err != nil || img.Bounds().Dx() != 20 {
		t.Errorf("symlinked cover: %v, %v", img, err)
	}
	fifo := filepath.Join(t.TempDir(), "pipe.flac")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := (&Loader{}).Load(context.Background(), Ref{Key: "audio:" + fifo, Audio: fifo})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNone) {
			t.Errorf("FIFO: %v, want ErrNone", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("loading a FIFO blocked")
	}
}

func TestNoDowngrade(t *testing.T) {
	https, _ := http.NewRequest("GET", "https://a.example/x", nil)
	for target, ok := range map[string]bool{"https://b.example/y": true, "http://b.example/y": false} {
		req, _ := http.NewRequest("GET", target, nil)
		if err := NoDowngrade(req, []*http.Request{https}); (err == nil) != ok {
			t.Errorf("redirect to %s: %v", target, err)
		}
	}
}
