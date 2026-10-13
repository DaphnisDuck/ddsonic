//go:build !windows

package spotify

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// fakeSpotifyAPI installs a transport that answers with handler's payload, or
// with the given HTTP status when handler returns a non-zero one.
func fakeSpotifyAPI(t *testing.T, handler func(req *http.Request) (any, int)) *SpotifyProvider {
	t.Helper()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		payload, status := handler(req)
		if status == 0 {
			status = http.StatusOK
		}
		body, _ := json.Marshal(payload)
		return &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	sess := staticSession()
	return New(sess, "client", 320)
}

func TestArtistsFollowsCursorAndSortsByName(t *testing.T) {
	pages := map[string]map[string]any{
		"": {"artists": map[string]any{
			"total":   4,
			"items":   []map[string]any{{"id": "a1", "name": "Ravel"}, {"id": "a2", "name": "mahler"}},
			"next":    "https://api.spotify.com/v1/me/following?after=a2",
			"cursors": map[string]any{"after": "a2"},
		}},
		"a2": {"artists": map[string]any{
			"total":   4,
			"items":   []map[string]any{{"id": "a3", "name": "Beethoven"}, {"id": "", "name": "unavailable"}},
			"next":    nil,
			"cursors": map[string]any{"after": nil},
		}},
	}
	var calls int
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Path != "/v1/me/following" || req.URL.Query().Get("type") != "artist" {
			t.Errorf("unexpected request %s", req.URL)
		}
		calls++
		return pages[req.URL.Query().Get("after")], 0
	})

	got, err := p.Artists()
	if err != nil {
		t.Fatalf("Artists() error = %v", err)
	}
	if calls != 2 {
		t.Errorf("requests = %d, want 2", calls)
	}
	var names []string
	for _, a := range got {
		names = append(names, a.Name)
	}
	if want := "Beethoven,mahler,Ravel"; strings.Join(names, ",") != want {
		t.Errorf("artists = %v, want %s", names, want)
	}
}

func TestArtistAlbums(t *testing.T) {
	album := func(i int, date string) map[string]any {
		return map[string]any{
			"id": fmt.Sprintf("al%d", i), "name": fmt.Sprintf("Album %d", i),
			"release_date": date, "total_tracks": 9,
			"artists": []map[string]any{{"name": "Mahler"}},
		}
	}
	tests := []struct {
		name         string
		rejectLimit  bool // reject the full page size as Development Mode would
		total        int
		wantRequests int
		wantLimit    string
	}{
		{name: "single page", total: 3, wantRequests: 1, wantLimit: "50"},
		{name: "dev mode limit fallback", rejectLimit: true, total: 12, wantRequests: 3, wantLimit: "10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests int
			var lastLimit string
			p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
				if req.URL.Path != "/v1/artists/art1/albums" {
					t.Errorf("unexpected path %s", req.URL.Path)
				}
				q := req.URL.Query()
				if q.Get("include_groups") != artistAlbumGroups {
					t.Errorf("include_groups = %q", q.Get("include_groups"))
				}
				requests++
				lastLimit = q.Get("limit")
				limit, _ := strconv.Atoi(lastLimit)
				if tt.rejectLimit && limit > devModeSearchLimit {
					return map[string]any{"error": map[string]any{"status": 400, "message": "Invalid limit"}}, http.StatusBadRequest
				}
				offset, _ := strconv.Atoi(q.Get("offset"))
				items := []map[string]any{}
				for i := offset; i < offset+limit && i < tt.total; i++ {
					items = append(items, album(i, fmt.Sprintf("%d-01-01", 1980+i)))
				}
				return map[string]any{"items": items, "total": tt.total}, 0
			})

			got, err := p.ArtistAlbums("art1")
			if err != nil {
				t.Fatalf("ArtistAlbums() error = %v", err)
			}
			if len(got) != tt.total {
				t.Fatalf("albums = %d, want %d", len(got), tt.total)
			}
			if requests != tt.wantRequests || lastLimit != tt.wantLimit {
				t.Errorf("requests = %d (last limit %s), want %d (limit %s)", requests, lastLimit, tt.wantRequests, tt.wantLimit)
			}
			first := got[0]
			if first.Year != 1980+tt.total-1 || first.ArtistID != "art1" || first.Artist != "Mahler" || first.TrackCount != 9 {
				t.Errorf("first album = %+v, want newest with artist metadata", first)
			}
		})
	}
}
