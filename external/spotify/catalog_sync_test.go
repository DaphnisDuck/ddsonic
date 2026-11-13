//go:build !windows

// ddsonic: tests for the whole-collection catalog fetchers.

package spotify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync/spotifysrc"
	"github.com/bjarneo/cliamp/playlist"
)

var _ spotifysrc.Client = (*SpotifyProvider)(nil)

func offsetOf(req *http.Request) int {
	n, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	return n
}

func TestSavedAlbumRecordsPagesEverything(t *testing.T) {
	const total = 60
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Path != "/v1/me/albums" {
			t.Errorf("unexpected path %s", req.URL.Path)
		}
		items := []map[string]any{}
		for i := offsetOf(req); i < min(offsetOf(req)+catalogPageSize, total); i++ {
			items = append(items, map[string]any{
				"added_at": "2024-05-06T07:08:09Z",
				"album": map[string]any{
					"id": fmt.Sprintf("al%d", i), "name": fmt.Sprintf("Album %d", i), "release_date": "1990-01-01",
					"total_tracks": 9, "artists": []map[string]any{{"id": "ar1", "name": "Ozawa"}},
					"images": []map[string]any{{"url": "https://img", "width": 300}},
				},
			})
		}
		return map[string]any{"items": items, "total": total}, 0
	})
	got, err := p.SavedAlbumRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != total {
		t.Fatalf("albums = %d, want %d", len(got), total)
	}
	a := got[0]
	if a.Ref != (catalog.Ref{Provider: catalog.Spotify, ProviderID: "al0"}) || a.Year != 1990 || a.TrackCount != 9 ||
		a.ArtworkURL != "https://img" || len(a.Artists) != 1 || a.Artists[0].Ref.ProviderID != "ar1" ||
		!a.AddedAt.Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Errorf("first album = %+v", a)
	}
}

// A library edited mid-read reports a different total; applying the result
// could drop items, so the read fails.
func TestPagedReadFailsWhenTheLibraryChanges(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		total := 60
		if offsetOf(req) > 0 {
			total = 59 // an album was unsaved between pages
		}
		items := make([]map[string]any, min(catalogPageSize, total-offsetOf(req)))
		for i := range items {
			items[i] = map[string]any{"album": map[string]any{"id": fmt.Sprintf("al%d", offsetOf(req)+i)}}
		}
		return map[string]any{"items": items, "total": total}, 0
	})
	if _, err := p.SavedAlbumRecords(context.Background()); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("SavedAlbumRecords() = %v, want ErrIncomplete", err)
	}
}

func TestLikedTrackRecordsMapsAndSkips(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		return map[string]any{"total": 4, "items": []map[string]any{
			{"added_at": "2024-01-01T00:00:00Z", "track": map[string]any{
				"id": "t1", "name": "I. Trauermarsch", "type": "track", "duration_ms": 780000,
				"track_number": 1, "disc_number": 1, "artists": []map[string]any{{"id": "ar1", "name": "Ozawa"}},
				"album": map[string]any{"id": "al1", "name": "Mahler 5", "release_date": "1990"},
			}},
			{"track": map[string]any{"id": "t2", "name": "Local", "is_local": true}},
			{"track": map[string]any{"id": "e1", "name": "Episode", "type": "episode"}},
			{"track": nil},
		}}, 0
	})
	got, err := p.LikedTrackRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("tracks = %+v, want only the catalogable track", got)
	}
	tr := got[0]
	if tr.PlayableURI != "spotify:track:t1" || tr.Duration != 13*time.Minute || tr.Album == nil ||
		tr.Album.Ref.ProviderID != "al1" || tr.Year != 1990 || len(tr.Artists) != 1 {
		t.Errorf("track = %+v", tr)
	}
}

func TestPlaylistRecordsAndForbiddenItems(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		switch req.URL.Path {
		case "/v1/me":
			return map[string]any{"id": "me"}, 0
		case "/v1/me/playlists":
			return map[string]any{"total": 2, "items": []map[string]any{
				{"id": "mine", "name": "Mine", "snapshot_id": "s1", "owner": map[string]any{"id": "me"}, "items": map[string]any{"total": 3}},
				{"id": "theirs", "name": "Theirs", "snapshot_id": "s2", "owner": map[string]any{"id": "other"}},
			}}, 0
		case "/v1/playlists/theirs/items":
			return map[string]any{"error": map[string]any{"status": 403}}, http.StatusForbidden
		case "/v1/playlists/mine/items":
			return map[string]any{"total": 1, "items": []map[string]any{{"item": map[string]any{"id": "t1", "name": "T", "type": "track"}}}}, 0
		}
		t.Errorf("unexpected path %s", req.URL.Path)
		return nil, http.StatusNotFound
	})
	ctx := context.Background()
	lists, err := p.PlaylistRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 2 || !lists[0].Own || lists[1].Own || lists[0].Snapshot != "s1" || lists[0].TrackCount != 3 {
		t.Fatalf("playlists = %+v", lists)
	}
	if tracks, err := p.PlaylistTrackRecords(ctx, "mine"); err != nil || len(tracks) != 1 {
		t.Errorf("mine tracks = %+v, %v", tracks, err)
	}
	if _, err := p.PlaylistTrackRecords(ctx, "theirs"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("theirs error = %v, want catalog.ErrForbidden", err)
	}
}

func TestFollowedArtistRecords(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Query().Get("after") == "" {
			return map[string]any{"artists": map[string]any{"total": 3, "next": "more", "cursors": map[string]any{"after": "a2"},
				"items": []map[string]any{{"id": "a1", "name": "One"}, {"id": "a2", "name": "Two"}}}}, 0
		}
		return map[string]any{"artists": map[string]any{"total": 3,
			"items": []map[string]any{{"id": "a3", "name": "Three"}}}}, 0
	})
	got, err := p.FollowedArtistRecords(context.Background())
	if err != nil || len(got) != 3 || got[2].Ref.ProviderID != "a3" {
		t.Fatalf("FollowedArtistRecords() = %+v, %v", got, err)
	}
}

func TestPlaylistItems404IsUnreadable(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		return map[string]any{"error": map[string]any{"status": 404}}, http.StatusNotFound
	})
	if _, err := p.PlaylistTrackRecords(context.Background(), "gone"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("error = %v, want catalog.ErrForbidden", err)
	}
}

// Ownership decides Your vs Followed playlists; if the user cannot be
// identified the sync must fail rather than mark every playlist followed.
func TestPlaylistRecordsFailWithoutUserID(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Path == "/v1/me" {
			return map[string]any{"error": "busy"}, http.StatusServiceUnavailable
		}
		return map[string]any{"total": 0, "items": []any{}}, 0
	})
	if _, err := p.PlaylistRecords(context.Background()); err == nil {
		t.Error("PlaylistRecords() succeeded without a user ID")
	}
}

func TestClosedSessionReturnsNeedsAuth(t *testing.T) {
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) { return map[string]any{}, 0 })
	p.mu.Lock()
	p.session = nil
	p.mu.Unlock()
	if _, err := p.webAPI(context.Background(), "GET", "/v1/me", nil); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("webAPI() after Close = %v, want ErrNeedsAuth", err)
	}
}

func TestAlbumTrackRecordsPagesSimplifiedTracks(t *testing.T) {
	const total = 55
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Path != "/v1/albums/al1/tracks" {
			t.Errorf("unexpected path %s", req.URL.Path)
		}
		items := []map[string]any{}
		for i := offsetOf(req); i < min(offsetOf(req)+catalogPageSize, total); i++ {
			items = append(items, map[string]any{
				"id": fmt.Sprintf("t%d", i), "name": fmt.Sprintf("Track %d", i), "type": "track",
				"track_number": i + 1, "disc_number": 1, "duration_ms": 1000,
				"artists": []map[string]any{{"id": "ar1", "name": "Ozawa"}},
			})
		}
		return map[string]any{"items": items, "total": total}, 0
	})
	for name, fetch := range map[string]func(context.Context, string) ([]catalog.TrackRecord, error){
		"retrying": p.AlbumTrackRecords, "once": p.AlbumTrackRecordsOnce,
	} {
		got, err := fetch(context.Background(), "al1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != total {
			t.Fatalf("%s: tracks = %d, want %d", name, len(got), total)
		}
		tr := got[54]
		if tr.Ref.ProviderID != "t54" || tr.TrackNo != 55 || tr.PlayableURI != "spotify:track:t54" || tr.Album != nil ||
			len(tr.Artists) != 1 || tr.Duration != time.Second {
			t.Errorf("%s: last track = %+v", name, tr)
		}
	}
}

// rateLimitedAPI answers every request with status and Retry-After, and
// counts the requests that reach it.
func rateLimitedAPI(t *testing.T, status *int, retryAfter *string) (*SpotifyProvider, *int) {
	calls := 0
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		h := make(http.Header)
		h.Set("Retry-After", *retryAfter)
		return &http.Response{StatusCode: *status, Status: fmt.Sprintf("%d %s", *status, http.StatusText(*status)),
			Header: h, Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	return New(staticSession(), "client", 320), &calls
}

// The background fetch reports a rate limit at once, with Spotify's
// requested wait, instead of retrying it, and later requests wait out the
// block without asking.
func TestAlbumTrackRecordsOnceReportsRateLimits(t *testing.T) {
	status, retryAfter := http.StatusTooManyRequests, "7"
	p, calls := rateLimitedAPI(t, &status, &retryAfter)
	now := time.Unix(1_790_000_000, 0)
	p.rate.now = func() time.Time { return now }
	var recorded []time.Time
	p.OnRateLimited(func(until time.Time, _ bool) { recorded = append(recorded, until) })

	_, err := p.AlbumTrackRecordsOnce(context.Background(), "al1")
	var rl *catalog.RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second || *calls != 1 {
		t.Errorf("err = %v, calls %d; want one call and a 7s RateLimitError", err, *calls)
	}
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "al1"); !errors.As(err, &rl) || *calls != 1 {
		t.Errorf("during the block: %v, calls %d; want a RateLimitError and no request", err, *calls)
	}
	if len(recorded) != 1 || !recorded[0].Equal(now.Add(7*time.Second)) {
		t.Errorf("recorded blocks = %v, want the short one recorded too (ddsonic: persisted across restarts)", recorded)
	}

	now = now.Add(8 * time.Second)
	retryAfter = "99999999999" // an absurd wait is capped, not overflowed
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "al1"); !errors.As(err, &rl) || rl.RetryAfter != 48*time.Hour || *calls != 2 {
		t.Errorf("huge Retry-After: %v, calls %d; want a 48h RateLimitError", err, *calls)
	}
	if len(recorded) != 2 || !recorded[1].Equal(now.Add(48*time.Hour)) {
		t.Errorf("recorded blocks = %v, want the 48h one", recorded)
	}

	now = now.Add(49 * time.Hour)
	status, retryAfter = http.StatusNotFound, ""
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "gone"); !errors.Is(err, catalog.ErrForbidden) {
		t.Errorf("404: %v, want ErrForbidden", err)
	}
}

// A long Retry-After fails a retrying request at once instead of sleeping
// in it (an album open once waited 20h), and blocks the next request too.
func TestWebAPIFailsFastOnALongBlock(t *testing.T) {
	status, retryAfter := http.StatusTooManyRequests, "72300"
	p, calls := rateLimitedAPI(t, &status, &retryAfter)
	done := make(chan error, 1)
	go func() {
		_, err := p.AlbumTrackRecords(context.Background(), "al1")
		done <- err
	}()
	select {
	case err := <-done:
		var rl *catalog.RateLimitError
		if !errors.As(err, &rl) || rl.RetryAfter < 20*time.Hour || rl.Until.IsZero() {
			t.Errorf("err = %v, want a ~20h RateLimitError with its end", err)
		}
		if !strings.Contains(err.Error(), "rate limited until ") {
			t.Errorf("message = %q", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request slept through the block")
	}
	if _, err := p.webAPI(context.Background(), "GET", "/v1/me", nil); err == nil || *calls != 1 {
		t.Errorf("next request: %v, calls %d; want it blocked without asking", err, *calls)
	}
}

// A block recorded by an earlier run holds until its end.
func TestRestoredBlockHoldsUntilItEnds(t *testing.T) {
	status, retryAfter := http.StatusNotFound, ""
	p, calls := rateLimitedAPI(t, &status, &retryAfter)
	now := time.Unix(1_790_000_000, 0)
	p.rate.now = func() time.Time { return now }
	p.SetRateLimitedUntil(now.Add(time.Hour), false)
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "al1"); !errors.As(err, new(*catalog.RateLimitError)) || *calls != 0 {
		t.Errorf("restored block: %v, calls %d; want a RateLimitError and no request", err, *calls)
	}
	now = now.Add(time.Hour + time.Second)
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "al1"); !errors.Is(err, catalog.ErrForbidden) || *calls != 1 {
		t.Errorf("after the block: %v, calls %d; want the request made", err, *calls)
	}
}

// A head is one limit=1 request: the total and the first (newest) item,
// with its added_at at the second the catalog stores it at.
func TestCollectionHeads(t *testing.T) {
	var liked []map[string]any
	var likedTotal int
	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		if req.URL.Query().Get("limit") != "1" {
			t.Errorf("%s limit = %q, want 1", req.URL.Path, req.URL.Query().Get("limit"))
		}
		switch req.URL.Path {
		case "/v1/me/albums":
			return map[string]any{"total": 2141, "items": []map[string]any{
				{"added_at": "2026-09-20T18:04:05Z", "album": map[string]any{"id": "al1", "name": "A"}}}}, 0
		case "/v1/me/tracks":
			return map[string]any{"total": likedTotal, "items": liked}, 0
		}
		t.Errorf("unexpected path %s", req.URL.Path)
		return nil, http.StatusNotFound
	})
	ctx := context.Background()
	at := time.Date(2026, 9, 20, 18, 4, 5, 0, time.UTC)
	albums, err := p.SavedAlbumsHead(ctx)
	if err != nil || albums != (catalog.CollectionHead{Total: 2141, NewestID: "al1", NewestAt: albums.NewestAt}) || !albums.NewestAt.Equal(at) {
		t.Fatalf("albums head = %+v, %v", albums, err)
	}
	stored := catalog.CollectionState{Count: 2141, NewestAt: time.UnixMilli(at.UnixMilli()), Newest: []string{"al1"}}
	if !stored.Matches(albums) {
		t.Error("head does not match the catalog's copy of it")
	}

	for _, tt := range []struct {
		name  string
		total int
		items []map[string]any
		want  catalog.CollectionHead
	}{
		{"empty", 0, nil, catalog.CollectionHead{}},
		{"local file first", 3, []map[string]any{{"added_at": "2026-09-20T18:04:05Z",
			"track": map[string]any{"id": "", "name": "home.mp3", "is_local": true}}}, catalog.CollectionHead{Total: 3}},
	} {
		liked, likedTotal = tt.items, tt.total
		got, err := p.LikedTracksHead(ctx)
		if err != nil || got.Total != tt.want.Total || got.NewestID != tt.want.NewestID {
			t.Errorf("%s: liked head = %+v, %v; want %+v", tt.name, got, err, tt.want)
		}
	}
}
