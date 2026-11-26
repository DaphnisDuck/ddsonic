// ddsonic: tests for the OAuth-mode catalog fetchers against a fake Data API.

package ytmusic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
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
	fail      map[string]apiFail          // endpoint (see endpoint) → its failure
	calls     []string
}

// apiFail is an endpoint's scripted failure: a status and, when set, a
// googleapi reason.
type apiFail struct {
	code   int
	reason string
}

// endpoint names a request for fakeDataAPI.fail: "playlists" (yours),
// "playlistItems", "videos:snippet" (categories), "videos:contentDetails"
// (durations).
func endpoint(r *http.Request) string {
	name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if name == "videos" {
		return name + ":" + r.URL.Query().Get("part")
	}
	if name == "playlists" && r.URL.Query().Get("id") != "" {
		return "playlists:id"
	}
	return name
}

func writeFail(w http.ResponseWriter, f apiFail) {
	body := map[string]any{"code": f.code, "message": "refused"}
	if f.reason != "" {
		body["errors"] = []map[string]any{{"reason": f.reason, "message": "refused"}}
	}
	w.WriteHeader(f.code)
	json.NewEncoder(w).Encode(map[string]any{"error": body})
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
	if fail, ok := f.fail[endpoint(r)]; ok {
		writeFail(w, fail)
		return
	}
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
// ddsonic (#170): it fails it as a held limit, which stops the sync.
func TestOAuthPlaylistRecordQuota(t *testing.T) {
	c, f := newOAuthFixture(t)
	f.refuse = map[string]string{"PLroad": "quotaExceeded", "PLshows": "rateLimitExceeded", "PLprivate": "forbidden"}
	for _, id := range []string{"PLroad", "PLshows"} {
		_, err := c.PlaylistRecord(context.Background(), id)
		if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Quota || errors.Is(err, catalog.ErrForbidden) {
			t.Errorf("%s over quota = %v, want a held limit", id, err)
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

// ddsonic (#170): the Data API's rate-limit and quota answers are held
// limits, each saying which; ordinary refusals and server errors are not.
func TestDataAPILimits(t *testing.T) {
	gerr := func(code int, reason string) error {
		e := &googleapi.Error{Code: code, Message: "x"}
		if reason != "" {
			e.Errors = []googleapi.ErrorItem{{Reason: reason}}
		}
		return fmt.Errorf("call: %w", e)
	}
	tests := []struct {
		err       error
		reason    string // "" when not a limit
		forbidden bool
	}{
		{gerr(403, "quotaExceeded"), "YouTube Data API: quotaExceeded", false},
		{gerr(403, "dailyLimitExceeded"), "YouTube Data API: dailyLimitExceeded", false},
		{gerr(403, "rateLimitExceeded"), "YouTube Data API: rateLimitExceeded", false},
		{gerr(403, "userRateLimitExceeded"), "YouTube Data API: userRateLimitExceeded", false},
		{gerr(429, ""), "YouTube Data API: HTTP 429", false},
		{gerr(429, "rateLimitExceeded"), "YouTube Data API: rateLimitExceeded", false},
		{gerr(403, "forbidden"), "", true},
		{gerr(403, ""), "", true},
		{gerr(404, "playlistNotFound"), "", true},
		{gerr(500, "backendError"), "", false},
		{gerr(503, ""), "", false},
		{errors.New("dial tcp: refused"), "", false},
	}
	for _, tt := range tests {
		err := apiError(tt.err)
		rl, limited := errors.AsType[*catalog.RateLimitError](err)
		switch {
		case tt.reason != "" && (!limited || !rl.Quota || rl.Reason != tt.reason || !rl.Until.IsZero() || rl.RetryAfter != 0):
			t.Errorf("apiError(%v) = %v, want a held limit %q with no reset time", tt.err, err, tt.reason)
		case tt.reason == "" && limited:
			t.Errorf("apiError(%v) = %v, want no limit", tt.err, err)
		case errors.Is(err, catalog.ErrForbidden) != tt.forbidden:
			t.Errorf("apiError(%v) = %v, forbidden want %t", tt.err, err, tt.forbidden)
		case !errors.Is(err, tt.err):
			t.Errorf("apiError(%v) = %v lost the original", tt.err, err)
		}
	}
}

// ddsonic (#170): every OAuth catalog read fails with a held limit when the
// API answers one: listing playlists, classifying them, reading a
// playlist's items, and its durations (no longer swallowed). An ordinary
// duration failure still only leaves durations unknown.
func TestOAuthReadsStopAtALimit(t *testing.T) {
	limit := apiFail{403, "quotaExceeded"}
	reads := map[string]func(c *OAuthCatalog) error{
		"playlists": func(c *OAuthCatalog) error { _, err := c.PlaylistRecords(context.Background(), nil); return err },
		"videos:snippet": func(c *OAuthCatalog) error {
			_, err := c.PlaylistRecords(context.Background(), nil)
			return err
		},
		"playlistItems": func(c *OAuthCatalog) error { _, err := c.LikedTrackRecords(context.Background()); return err },
		"videos:contentDetails": func(c *OAuthCatalog) error {
			_, err := c.PlaylistTrackRecords(context.Background(), "PLroad")
			return err
		},
	}
	for ep, read := range reads {
		t.Run(ep, func(t *testing.T) {
			c, f := newOAuthFixture(t)
			f.fail = map[string]apiFail{ep: limit}
			err := read(c)
			if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Quota {
				t.Errorf("= %v, want a held limit", err)
			}
		})
	}
	t.Run("ordinary duration failure", func(t *testing.T) {
		c, f := newOAuthFixture(t)
		f.fail = map[string]apiFail{"videos:contentDetails": {500, "backendError"}}
		tracks, err := c.PlaylistTrackRecords(context.Background(), "PLroad")
		if err != nil || len(tracks) != 60 || tracks[0].Duration != 0 {
			t.Errorf("= %d tracks, %v; want 60 with unknown durations", len(tracks), err)
		}
	})
}

// durationAPI is an in-memory Data API for duration batches: the first
// batch answers with quota after five batches are in flight; the others
// wait for their context (or, if never cancelled, answer after a while).
type durationAPI struct{ sends atomic.Int32 }

func (d *durationAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	n := d.sends.Add(1)
	if n == 1 {
		for deadline := time.Now().Add(2 * time.Second); d.sends.Load() < 5 && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
		}
		body := `{"error":{"code":403,"message":"quota","errors":[{"reason":"quotaExceeded","message":"quota"}]}}`
		return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}
	select {
	case <-r.Context().Done():
		return nil, r.Context().Err()
	case <-time.After(2 * time.Second):
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"items":[]}`)), Request: r}, nil
}

// ddsonic (#170): concurrent duration batches keep a quota answer. Once
// one batch is answered with quota, no further batch is sent (those in
// flight, five here, cannot be recalled and are cancelled), and the quota
// is returned rather than swallowed.
func TestDurationsStopAtALimit(t *testing.T) {
	api := &durationAPI{}
	svc, err := youtube.NewService(context.Background(), option.WithHTTPClient(&http.Client{Transport: api}), option.WithEndpoint("http://data.invalid/"))
	if err != nil {
		t.Fatal(err)
	}
	var items []itemInfo
	for i := range 12 * youtubeAPIBatchSize {
		items = append(items, itemInfo{videoID: "v" + strconv.Itoa(i)})
	}
	_, err = (*baseProvider)(nil).fetchDurations(context.Background(), svc, items)
	if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Quota {
		t.Errorf("fetchDurations = %v, want the quota", err)
	}
	if n := api.sends.Load(); n != 5 {
		t.Errorf("%d batches sent; want 5 (those in flight when the quota came), not 12", n)
	}
}
