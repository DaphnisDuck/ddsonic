package lyrics

// ddsonic: the plain-HTTP NetEase lookup is off unless turned on (#136).

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// withNetEase turns the NetEase lookup on for one test.
func withNetEase(t *testing.T) {
	t.Helper()
	SetNetEaseFallback(true)
	t.Cleanup(func() { SetNetEaseFallback(false) })
}

// hostLog is a transport that answers every request without the network:
// LRCLIB with no results, NetEase with one song and its lyrics. It notes
// each request's URL as sent.
type hostLog struct {
	mu   sync.Mutex
	urls []string
}

func (h *hostLog) RoundTrip(req *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.urls = append(h.urls, req.URL.String())
	h.mu.Unlock()
	body := `[]`
	switch {
	case req.URL.Path == "/api/search/get/web":
		body = `{"result":{"songs":[{"id":42}]}}`
	case strings.HasPrefix(req.URL.Path, "/api/song/lyric"):
		body = `{"lrc":{"lyric":"[00:10.00]From NetEase"}}`
	}
	return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
}

func logHosts(t *testing.T) *hostLog {
	t.Helper()
	log := &hostLog{}
	old := httpClient
	httpClient = &http.Client{Transport: log}
	t.Cleanup(func() { httpClient = old })
	return log
}

func TestFetchNetEaseOffByDefault(t *testing.T) {
	if netEaseFallback.Load() {
		t.Fatal("the NetEase lookup is on before anything turned it on")
	}
	log := logHosts(t)
	if _, err := Fetch("Artist", "Song"); err != ErrNotFound {
		t.Errorf("Fetch = %v, want ErrNotFound when LRCLIB has nothing", err)
	}
	if len(log.urls) != 1 || !strings.HasPrefix(log.urls[0], "https://lrclib.net/") {
		t.Errorf("requests %q, want only the one to LRCLIB over HTTPS", log.urls)
	}
	for _, u := range log.urls {
		if strings.Contains(u, "163.com") || strings.HasPrefix(u, "http://") {
			t.Errorf("a request went to %s", u)
		}
	}
}

// Turned on, the lookup is the plain-HTTP one the docs warn of.
func TestFetchNetEaseWhenTurnedOn(t *testing.T) {
	withNetEase(t)
	log := logHosts(t)
	lines, err := Fetch("Artist", "Song")
	if err != nil || len(lines) != 1 || lines[0].Text != "From NetEase" {
		t.Fatalf("Fetch = %+v, %v; want the NetEase line", lines, err)
	}
	if len(log.urls) != 3 || !strings.HasPrefix(log.urls[1], "http://music.163.com/") || !strings.HasPrefix(log.urls[2], "http://music.163.com/") {
		t.Errorf("requests %q, want LRCLIB then two to http://music.163.com", log.urls)
	}
}
