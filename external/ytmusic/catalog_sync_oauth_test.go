// ddsonic: tests for the OAuth-mode catalog fetchers against a fake Data API.

package ytmusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// fakeDataAPI serves playlists, paged playlist items, and videos.
type fakeDataAPI struct {
	mu        sync.Mutex
	playlists []map[string]any
	items     map[string][]map[string]any // playlist ID → items
	category  map[string]string           // video ID → category ID
	duration  map[string]string           // video ID → ISO 8601
	totalBump map[string]int              // playlist ID → extra reported total
	refuse    map[string]string           // playlist ID → 403 reason for its lookup
	calls     []string
}

func item(videoID, title, channel, channelID string) map[string]any {
	return map[string]any{
		"snippet": map[string]any{"title": title, "videoOwnerChannelTitle": channel,
			"videoOwnerChannelId": channelID, "publishedAt": "2025-01-02T03:04:05Z"},
		"contentDetails": map[string]any{"videoId": videoID},
	}
}

func (f *fakeDataAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := r.URL.Query()
	f.calls = append(f.calls, r.URL.Path+"?"+q.Get("playlistId")+strings.Join(q["id"], ","))
	write := func(v any) { json.NewEncoder(w).Encode(v) }
	switch {
	case strings.HasSuffix(r.URL.Path, "/playlists"):
		if id := q.Get("id"); id != "" {
			if reason, ok := f.refuse[id]; ok {
				w.WriteHeader(http.StatusForbidden)
				write(map[string]any{"error": map[string]any{"code": 403, "message": "refused",
					"errors": []map[string]any{{"reason": reason, "message": "refused"}}}})
				return
			}
			var found []map[string]any
			for _, p := range f.playlists {
				if p["id"] == id {
					found = append(found, p)
				}
			}
			write(map[string]any{"items": found})
			return
		}
		write(map[string]any{"items": f.playlists})
	case strings.HasSuffix(r.URL.Path, "/playlistItems"):
		id := q.Get("playlistId")
		items, ok := f.items[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			write(map[string]any{"error": map[string]any{"code": 404, "message": "playlistNotFound"}})
			return
		}
		size, _ := strconv.Atoi(q.Get("maxResults"))
		start, _ := strconv.Atoi(q.Get("pageToken"))
		end := min(start+size, len(items))
		resp := map[string]any{"items": items[start:end],
			"pageInfo": map[string]any{"totalResults": len(items) + f.totalBump[id]}}
		if end < len(items) {
			resp["nextPageToken"] = strconv.Itoa(end)
		}
		write(resp)
	case strings.HasSuffix(r.URL.Path, "/videos"):
		var out []map[string]any
		for _, id := range strings.Split(strings.Join(q["id"], ","), ",") {
			if strings.Contains(q.Get("part"), "snippet") {
				out = append(out, map[string]any{"id": id, "snippet": map[string]any{"categoryId": f.category[id]}})
			} else {
				out = append(out, map[string]any{"id": id, "contentDetails": map[string]any{"duration": f.duration[id]}})
			}
		}
		write(map[string]any{"items": out})
	default:
		http.NotFound(w, r)
	}
}

func newOAuthFixture(t *testing.T) (*OAuthCatalog, *fakeDataAPI) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir()) // the classification cache
	var road []map[string]any
	for i := range 60 { // two pages
		road = append(road, item("r"+strconv.Itoa(i), "Song "+strconv.Itoa(i), "Rana Park - Topic", "UCrana"))
	}
	road = append(road, item("gone", "Deleted video", "", ""))
	f := &fakeDataAPI{
		playlists: []map[string]any{
			{"id": "PLroad", "etag": "e1", "snippet": map[string]any{"title": "Road Trip"}, "contentDetails": map[string]any{"itemCount": len(road)}},
			{"id": "PLshows", "etag": "e2", "snippet": map[string]any{"title": "Game Shows"}, "contentDetails": map[string]any{"itemCount": 1}},
			{"id": "WL", "etag": "e3", "snippet": map[string]any{"title": "Watch later"}, "contentDetails": map[string]any{"itemCount": 3}},
		},
		items: map[string][]map[string]any{
			"PLroad":  road,
			"PLshows": {item("s1", "Episode 1", "TV", "UCtv")},
			"LM":      {item("r1", "Song 1", "Rana Park - Topic", "UCrana")},
		},
		category: map[string]string{"r0": "10", "s1": "24"},
		duration: map[string]string{"r0": "PT4M6S", "r1": "PT25M45S"},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	svc, err := youtube.NewService(context.Background(), option.WithHTTPClient(srv.Client()), option.WithEndpoint(srv.URL+"/"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewOAuthCatalog("id", "secret")
	c.service = func(context.Context) (*youtube.Service, string, error) { return svc, "oauth:test", nil }
	return c, f
}

func TestOAuthPlaylistRecords(t *testing.T) {
	c, _ := newOAuthFixture(t)
	lists, err := c.PlaylistRecords(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 || lists[0].Name != "Road Trip" || lists[0].TrackCount != 61 || lists[0].Snapshot != "61:e1" {
		t.Fatalf("playlists = %+v, want only the music playlist with its marker", lists)
	}
}

// A failed sample fails the listing and caches nothing; an empty playlist
// is left out uncached, so a later sync tries it again.
func TestOAuthClassificationFailures(t *testing.T) {
	c, f := newOAuthFixture(t)
	f.playlists = append(f.playlists,
		map[string]any{"id": "PLbroken", "etag": "e4", "snippet": map[string]any{"title": "Broken"}},
		map[string]any{"id": "PLempty", "etag": "e5", "snippet": map[string]any{"title": "Nothing Yet"}})
	f.items["PLempty"] = nil
	if _, err := c.PlaylistRecords(context.Background(), nil); err == nil {
		t.Fatal("PlaylistRecords succeeded with an unreadable playlist")
	}
	if cached := loadClassification("oauth:test"); cached != nil {
		t.Errorf("cached %v after a failed listing", cached)
	}
	f.playlists = f.playlists[:len(f.playlists)-2]
	f.playlists = append(f.playlists, map[string]any{"id": "PLempty", "etag": "e5", "snippet": map[string]any{"title": "Nothing Yet"}})
	lists, err := c.PlaylistRecords(context.Background(), nil)
	if err != nil || len(lists) != 1 || lists[0].Name != "Road Trip" {
		t.Fatalf("playlists = %+v, %v", lists, err)
	}
	cached := loadClassification("oauth:test")
	if _, ok := cached["PLempty"]; ok || !cached["PLroad"] || cached["PLshows"] {
		t.Errorf("cache = %v, want Road Trip music, Game Shows not, Nothing Yet unknown", cached)
	}
	// Already synced, it stays listed while it cannot be told, uncached.
	lists, err = c.PlaylistRecords(context.Background(), map[string]string{"PLempty": "0:e5", "PLshows": ""})
	if err != nil || len(lists) != 2 || lists[1].Name != "Nothing Yet" {
		t.Errorf("with Nothing Yet synced: %+v, %v", lists, err)
	}
	if _, ok := loadClassification("oauth:test")["PLempty"]; ok {
		t.Error("Nothing Yet cached")
	}
}

func TestOAuthPlaylistTracks(t *testing.T) {
	c, _ := newOAuthFixture(t)
	tracks, err := c.PlaylistTrackRecords(context.Background(), "PLroad")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 60 {
		t.Fatalf("tracks = %d, want 60 across two pages without the deleted video", len(tracks))
	}
	first := tracks[0]
	if first.Ref.ProviderID != "r0" || first.PlayableURI != "https://music.youtube.com/watch?v=r0" ||
		first.Duration != 4*time.Minute+6*time.Second || first.Artists[0].Name != "Rana Park" ||
		!first.AddedAt.Equal(time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("first track = %+v", first)
	}
	liked, err := c.LikedTrackRecords(context.Background())
	if err != nil || len(liked) != 1 || liked[0].Duration != 25*time.Minute+45*time.Second {
		t.Errorf("liked = %+v, %v", liked, err)
	}
}

func TestOAuthReadFailures(t *testing.T) {
	c, f := newOAuthFixture(t)
	f.totalBump = map[string]int{"PLroad": 1}
	if _, err := c.PlaylistTrackRecords(context.Background(), "PLroad"); !errors.Is(err, ErrIncomplete) {
		t.Errorf("short read = %v, want ErrIncomplete", err)
	}
	if _, err := c.PlaylistTrackRecords(context.Background(), "PLmissing"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("missing playlist = %v, want ErrForbidden", err)
	}
	c.service = func(context.Context) (*youtube.Service, string, error) {
		return nil, "", errors.Join(errors.New("no stored credentials"), playlist.ErrNeedsAuth)
	}
	if _, err := c.PlaylistRecords(context.Background(), nil); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("signed out = %v, want ErrNeedsAuth", err)
	}
}

func TestOAuthPlaylistRecord(t *testing.T) {
	c, _ := newOAuthFixture(t)
	p, err := c.PlaylistRecord(context.Background(), "PLroad")
	if err != nil || p.Name != "Road Trip" || p.TrackCount != 61 || p.Snapshot != "61:e1" {
		t.Errorf("PlaylistRecord = %+v, %v", p, err)
	}
	if _, err := c.PlaylistRecord(context.Background(), "PLmissing"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("missing = %v, want ErrForbidden", err)
	}
}

// Running out of quota is not a playlist that has gone: it must fail the
// read, not mark the playlist forbidden, or a sync drops a followed playlist.
func TestOAuthPlaylistRecordQuota(t *testing.T) {
	c, f := newOAuthFixture(t)
	f.refuse = map[string]string{"PLroad": "quotaExceeded", "PLshows": "rateLimitExceeded", "PLprivate": "forbidden"}
	for _, id := range []string{"PLroad", "PLshows"} {
		if _, err := c.PlaylistRecord(context.Background(), id); err == nil || errors.Is(err, catalog.ErrForbidden) {
			t.Errorf("%s over quota = %v, want a plain error", id, err)
		}
	}
	if _, err := c.PlaylistRecord(context.Background(), "PLprivate"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("private playlist = %v, want ErrForbidden", err)
	}
}

// Two new playlists that start with the same video both get classified.
func TestOAuthClassifiesPlaylistsSharingAVideo(t *testing.T) {
	c, f := newOAuthFixture(t)
	f.playlists = append(f.playlists,
		map[string]any{"id": "PLroad2", "etag": "e6", "snippet": map[string]any{"title": "Road Trip II"}})
	f.items["PLroad2"] = f.items["PLroad"][:1]
	lists, err := c.PlaylistRecords(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range lists {
		names = append(names, l.Name)
	}
	if len(names) != 2 || names[0] != "Road Trip" || names[1] != "Road Trip II" {
		t.Errorf("playlists = %q, want both playlists that start with the music video", names)
	}
	if videos := strings.Count(strings.Join(f.calls, " "), "/videos?r0"); videos != 1 {
		t.Errorf("the shared video was looked up %d times, want once", videos)
	}
}

// Only a refused grant or client asks for signing in again; being offline
// keeps its cause.
func TestSignInError(t *testing.T) {
	offline := &url.Error{Op: "Post", URL: "https://oauth2.googleapis.com/token", Err: errors.New("dial tcp: network is unreachable")}
	tests := []struct {
		err       error
		needsAuth bool
	}{
		{fmt.Errorf("ytmusic: silent refresh: %w", &oauth2.RetrieveError{ErrorCode: "invalid_grant"}), true},
		{&oauth2.RetrieveError{ErrorCode: "unauthorized_client"}, true},
		{&oauth2.RetrieveError{ErrorCode: "temporarily_unavailable"}, false},
		{fmt.Errorf("ytmusic: silent refresh: %w", offline), false},
	}
	for _, tt := range tests {
		got := signInError(tt.err)
		if errors.Is(got, playlist.ErrNeedsAuth) != tt.needsAuth || !errors.Is(got, tt.err) {
			t.Errorf("signInError(%v) = %v, want needs auth %v", tt.err, got, tt.needsAuth)
		}
	}
}
