package lyrics

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// hostRewriteTransport redirects outbound requests so live URLs resolve to
// the test server, letting us exercise fetchLRCLIB / fetchNetEase with
// controlled responses.
type hostRewriteTransport struct {
	target *url.URL
	rt     http.RoundTripper
}

func (t hostRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	clone.Host = t.target.Host
	return t.rt.RoundTrip(clone)
}

// installTestClient swaps the package httpClient to a transport that redirects
// every request to the given test server URL.
func installTestClient(t *testing.T, serverURL string) {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	old := httpClient
	httpClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: hostRewriteTransport{target: u, rt: http.DefaultTransport},
	}
	t.Cleanup(func() { httpClient = old })
}

func TestFetchBothEmptyReturnsNotFound(t *testing.T) {
	_, err := Fetch("", "")
	if err != ErrNotFound {
		t.Errorf("Fetch(\"\", \"\") = %v, want ErrNotFound", err)
	}
}

func TestFetchPrefersSyncedLyrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"syncedLyrics":"[00:05.00]Hello","plainLyrics":"Plain text"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	lines, err := Fetch("Artist", "Song")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].Text != "Hello" {
		t.Errorf("Text = %q, want Hello", lines[0].Text)
	}
	if lines[0].Start != 5*time.Second {
		t.Errorf("Start = %v, want 5s", lines[0].Start)
	}
}

func TestFetchFallsBackToPlainLyrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"syncedLyrics":"","plainLyrics":"Line one\nLine two"}]`))
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	lines, err := Fetch("Artist", "Song")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if lines[0].Text != "Line one" || lines[1].Text != "Line two" {
		t.Errorf("lines = %+v, want [Line one, Line two]", lines)
	}
	// Plain lyrics all share timestamp zero.
	if lines[0].Start != 0 {
		t.Errorf("Start[0] = %v, want 0", lines[0].Start)
	}
}

func TestFetchReturnsNotFoundWhenBothSourcesEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/search") && r.URL.Host == "" || strings.HasSuffix(r.Host, "lrclib.net") || r.URL.Path == "/api/search" {
			// LRCLIB: return empty list so fallback kicks in.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/search/get/web") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"songs":[]}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	_, err := Fetch("Artist", "Song")
	if err != ErrNotFound {
		t.Errorf("Fetch = %v, want ErrNotFound", err)
	}
}

func TestFetchNetEaseFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/search":
			// LRCLIB returns empty list, forcing fallback to NetEase.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/api/search/get/web":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"songs":[{"id":42}]}}`))
		case strings.HasPrefix(r.URL.Path, "/api/song/lyric"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"lrc":{"lyric":"[00:10.00]From NetEase"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	lines, err := Fetch("Artist", "Song")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(lines) != 1 || lines[0].Text != "From NetEase" {
		t.Errorf("lines = %+v, want one 'From NetEase' line", lines)
	}
	if lines[0].Start != 10*time.Second {
		t.Errorf("Start = %v, want 10s", lines[0].Start)
	}
}

func TestFetchSplitsTitleOnDash(t *testing.T) {
	// When the title contains " - ", the artist is rewritten from the title's
	// left half. Verify via the query the handler sees.
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/search" {
			gotQuery = r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"syncedLyrics":"[00:01.00]ok"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	_, _ = Fetch("Uploader Channel", "Real Artist - Real Song (Official Video)")
	if !strings.Contains(gotQuery, "Real Artist") {
		t.Errorf("query = %q, want to contain 'Real Artist' (title-split)", gotQuery)
	}
	if !strings.Contains(gotQuery, "Real Song") {
		t.Errorf("query = %q, want to contain 'Real Song'", gotQuery)
	}
	if strings.Contains(gotQuery, "Official") {
		t.Errorf("query = %q, should not contain 'Official' — cleanQuery failed", gotQuery)
	}
}

func TestFetchLRCLIBHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/search":
			http.Error(w, "server down", http.StatusInternalServerError)
		case "/api/search/get/web":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"songs":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	installTestClient(t, srv.URL)

	// LRCLIB returns 500; should fall through to NetEase which returns no songs → ErrNotFound.
	_, err := Fetch("a", "b")
	if err != ErrNotFound {
		t.Errorf("Fetch after 500 → NetEase empty = %v, want ErrNotFound", err)
	}
}
