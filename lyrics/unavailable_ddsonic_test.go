package lyrics

// ddsonic: a busy or unreachable LRCLIB is ErrUnavailable, not "no lyrics" (#82).

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// fakeLRCLIB answers LRCLIB with a fixed status and body, or fails like the
// network with err; NetEase, when asked, answers with neteaseBody.
type fakeLRCLIB struct {
	status      int
	body        string
	err         error
	hang        bool // block until the client gives up: a timeout
	neteaseBody string
}

func (f fakeLRCLIB) RoundTrip(req *http.Request) (*http.Response, error) {
	reply := func(code int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
	}
	switch {
	case req.URL.Host == "music.163.com" && req.URL.Path == "/api/search/get/web":
		if f.neteaseBody == "" {
			return reply(200, `{"result":{"songs":[]}}`)
		}
		return reply(200, `{"result":{"songs":[{"id":7}]}}`)
	case req.URL.Host == "music.163.com":
		return reply(200, f.neteaseBody)
	case f.hang:
		<-req.Context().Done()
		return nil, req.Context().Err()
	case f.err != nil:
		return nil, f.err
	}
	return reply(f.status, f.body)
}

func TestFetchTellsBusyLRCLIBFromNoLyrics(t *testing.T) {
	const synced = `[{"syncedLyrics":"[00:01.00]Found"}]`
	tests := []struct {
		name    string
		lrclib  fakeLRCLIB
		netease bool
		want    error // nil: lyrics found
	}{
		{"429 too many requests", fakeLRCLIB{status: 429}, false, ErrUnavailable},
		{"503 unavailable", fakeLRCLIB{status: 503}, false, ErrUnavailable},
		{"500 server error", fakeLRCLIB{status: 500}, false, ErrUnavailable},
		{"timeout", fakeLRCLIB{hang: true}, false, ErrUnavailable},
		{"connection refused", fakeLRCLIB{err: errors.New("dial tcp: connection refused")}, false, ErrUnavailable},
		{"404", fakeLRCLIB{status: 404}, false, ErrNotFound},
		{"403 is not busy", fakeLRCLIB{status: 403}, false, ErrNotFound},
		{"empty array", fakeLRCLIB{status: 200, body: `[]`}, false, ErrNotFound},
		{"no usable lyrics", fakeLRCLIB{status: 200, body: `[{"syncedLyrics":"","plainLyrics":""}]`}, false, ErrNotFound},
		{"unreadable answer", fakeLRCLIB{status: 200, body: `<html>`}, false, ErrNotFound},
		{"success", fakeLRCLIB{status: 200, body: synced}, false, nil},
		{"503, NetEase has none", fakeLRCLIB{status: 503}, true, ErrUnavailable},
		{"503, NetEase has lyrics", fakeLRCLIB{status: 503, neteaseBody: `{"lrc":{"lyric":"[00:02.00]NetEase"}}`}, true, nil},
		{"empty array, NetEase has none", fakeLRCLIB{status: 200, body: `[]`}, true, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.netease {
				withNetEase(t)
			}
			old := httpClient
			httpClient = &http.Client{Transport: tt.lrclib, Timeout: 50 * time.Millisecond}
			t.Cleanup(func() { httpClient = old })

			lines, err := Fetch("Artist", "Song")
			if tt.want == nil {
				if err != nil || len(lines) == 0 {
					t.Fatalf("Fetch = %v, %v; want lyrics", lines, err)
				}
				return
			}
			if err != tt.want {
				t.Fatalf("Fetch error = %#v, want %#v", err, tt.want)
			}
		})
	}
}

// ErrUnavailable is still ErrNotFound to callers that know only that one,
// and reads the same (the IPC lyrics reply sends err.Error()).
func TestErrUnavailableIsStillNotFound(t *testing.T) {
	if !errors.Is(ErrUnavailable, ErrNotFound) {
		t.Error("errors.Is(ErrUnavailable, ErrNotFound) = false")
	}
	if errors.Is(ErrNotFound, ErrUnavailable) {
		t.Error("errors.Is(ErrNotFound, ErrUnavailable) = true: every not-found would read as busy")
	}
	if ErrUnavailable.Error() != ErrNotFound.Error() {
		t.Errorf("ErrUnavailable text = %q, want ErrNotFound's %q", ErrUnavailable.Error(), ErrNotFound.Error())
	}
	if got := newLRCLIBStatusError(&http.Response{StatusCode: 503, Status: "503 Service Unavailable"}).Error(); got != "lrclib: 503 Service Unavailable" {
		t.Errorf("status error text = %q, want upstream's", got)
	}
}
