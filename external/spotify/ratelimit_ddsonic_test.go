//go:build !windows

// ddsonic: the rate-limit gate. Every 429 closes it; while it is closed no
// Web API request is sent; nothing waits a 429 out and sends again.

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/provider"
)

// quotaBody is Spotify's documented quota answer
// (developer.spotify.com/documentation/web-api/concepts/quota-modes).
const quotaBody = `{"error":{"status":429,"message":"Too many requests","reason":"QUOTA_EXCEEDED"}}`

// gateAPI is a fake Web API that answers a library of fixed size, unless
// limit says a request is to get a 429, and counts every request.
type gateAPI struct {
	mu       sync.Mutex
	calls    atomic.Int32
	paths    []string
	limit    func(req *http.Request) bool // this request gets the 429
	header   string                       // its Retry-After; "-" for none
	body     string                       // its body
	recorded []blockRecord
}

type blockRecord struct {
	until time.Time
	quota bool
}

const gateLibrarySize = 60 // two pages of 50

func newGateAPI(t *testing.T, now *time.Time) (*SpotifyProvider, *gateAPI) {
	t.Helper()
	g := &gateAPI{header: "-", body: `{}`}
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		g.calls.Add(1)
		g.mu.Lock()
		g.paths = append(g.paths, req.URL.Path+"?"+req.URL.RawQuery)
		limit, header, body := g.limit, g.header, g.body
		g.mu.Unlock()
		if !strings.Contains(req.URL.Host, "api.spotify.com") {
			t.Errorf("unexpected request to %s", req.URL)
		}
		if limit != nil && limit(req) {
			h := http.Header{}
			if header != "-" {
				h.Set("Retry-After", header)
			}
			return &http.Response{StatusCode: 429, Status: "429 Too Many Requests", Header: h,
				Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
		}
		payload, _ := json.Marshal(libraryPage(req))
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(string(payload))), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	p := New(staticSession(), "client", 320)
	p.rate.now = func() time.Time { return *now }
	p.OnRateLimited(func(until time.Time, quota bool) {
		g.mu.Lock()
		g.recorded = append(g.recorded, blockRecord{until, quota})
		g.mu.Unlock()
	})
	return p, g
}

func (g *gateAPI) set(limit func(*http.Request) bool, header, body string) {
	g.mu.Lock()
	g.limit, g.header, g.body = limit, header, body
	g.mu.Unlock()
}

func (g *gateAPI) records() []blockRecord {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]blockRecord(nil), g.recorded...)
}

// libraryPage answers each library endpoint with gateLibrarySize items.
func libraryPage(req *http.Request) any {
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 50
	}
	offset := offsetOf(req)
	n := max(0, min(limit, gateLibrarySize-offset))
	item := func(i int) map[string]any {
		id := fmt.Sprintf("x%d", offset+i)
		track := map[string]any{"id": id, "name": id, "type": "track", "uri": "spotify:track:" + id, "duration_ms": 1000}
		return map[string]any{"added_at": "2026-01-01T00:00:00Z", "album": map[string]any{"id": id, "name": id}, "track": track}
	}
	switch path := req.URL.Path; {
	case path == "/v1/me":
		return map[string]any{"id": "me"}
	case path == "/v1/me/playlists":
		if offset > 0 {
			return map[string]any{"total": 1, "items": []any{}}
		}
		return map[string]any{"total": 1, "items": []map[string]any{{"id": "pl1", "name": "P", "snapshot_id": "s1", "owner": map[string]any{"id": "me"}}}}
	case path == "/v1/me/following":
		if q.Get("after") == "" {
			items := make([]map[string]any, 50)
			for i := range items {
				items[i] = map[string]any{"id": fmt.Sprintf("a%d", i), "name": "A"}
			}
			return map[string]any{"artists": map[string]any{"total": gateLibrarySize, "items": items,
				"next": "https://api.spotify.com/v1/me/following?after=a49", "cursors": map[string]any{"after": "a49"}}}
		}
		items := make([]map[string]any, gateLibrarySize-50)
		for i := range items {
			items[i] = map[string]any{"id": fmt.Sprintf("a%d", 50+i), "name": "A"}
		}
		return map[string]any{"artists": map[string]any{"total": gateLibrarySize, "items": items}}
	case strings.HasPrefix(path, "/v1/albums/"):
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = item(i)["track"].(map[string]any)
		}
		return map[string]any{"total": gateLibrarySize, "items": items}
	case strings.HasPrefix(path, "/v1/playlists/"):
		items := make([]map[string]any, n)
		for i := range items {
			items[i] = map[string]any{"added_at": "2026-01-01T00:00:00Z", "item": item(i)["track"]}
		}
		return map[string]any{"total": gateLibrarySize, "items": items}
	}
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = item(i)
	}
	return map[string]any{"total": gateLibrarySize, "items": items}
}

// at matches the request for path at offset (or cursor), the one to limit.
func at(path, offset string) func(*http.Request) bool {
	return func(req *http.Request) bool {
		if req.URL.Path != path {
			return false
		}
		q := req.URL.Query()
		switch {
		case q.Get("after") != "":
			return offset == "after"
		case offset == "after":
			return false
		}
		return q.Get("offset") == offset || (offset == "" && q.Get("offset") == "")
	}
}

// The fetchers, each run alone.
var gateFetchers = []struct {
	name  string
	call  func(*SpotifyProvider, context.Context) error
	limit func(*http.Request) bool
	want  int32 // requests sent, the limited one included
}{
	{"saved albums head", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.SavedAlbumsHead(ctx); return err }, at("/v1/me/albums", ""), 1},
	{"liked tracks head", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.LikedTracksHead(ctx); return err }, at("/v1/me/tracks", ""), 1},
	{"saved albums, first page", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.SavedAlbumRecords(ctx); return err }, at("/v1/me/albums", "0"), 1},
	{"saved albums, second page", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.SavedAlbumRecords(ctx); return err }, at("/v1/me/albums", "50"), 2},
	{"liked tracks, first page", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.LikedTrackRecords(ctx); return err }, at("/v1/me/tracks", "0"), 1},
	{"liked tracks, second page", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.LikedTrackRecords(ctx); return err }, at("/v1/me/tracks", "50"), 2},
	{"followed artists, first page", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.FollowedArtistRecords(ctx)
		return err
	}, at("/v1/me/following", ""), 1},
	{"followed artists, second page", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.FollowedArtistRecords(ctx)
		return err
	}, at("/v1/me/following", "after"), 2},
	{"current user (playlist ownership)", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.PlaylistRecords(ctx); return err }, at("/v1/me", ""), 1},
	{"playlist listing", func(p *SpotifyProvider, ctx context.Context) error { _, err := p.PlaylistRecords(ctx); return err }, at("/v1/me/playlists", "0"), 2},
	{"playlist tracks, first page", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.PlaylistTrackRecords(ctx, "pl1")
		return err
	}, at("/v1/playlists/pl1/items", "0"), 1},
	{"playlist tracks, second page", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.PlaylistTrackRecords(ctx, "pl1")
		return err
	}, at("/v1/playlists/pl1/items", "50"), 2},
	{"album tracks, foreground open", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.AlbumTrackRecords(ctx, "al1")
		return err
	}, at("/v1/albums/al1/tracks", "50"), 2},
	{"album tracks, background fill", func(p *SpotifyProvider, ctx context.Context) error {
		_, err := p.AlbumTrackRecordsOnce(ctx, "al1")
		return err
	}, at("/v1/albums/al1/tracks", "0"), 1},
	{"a write", func(p *SpotifyProvider, ctx context.Context) error {
		resp, err := p.webAPIWithBody(ctx, "POST", "/v1/me/playlists", nil, strings.NewReader(`{}`), "application/json", http.StatusOK, http.StatusCreated)
		if resp != nil {
			resp.Body.Close()
		}
		return err
	}, func(req *http.Request) bool { return req.Method == "POST" }, 1},
}

// Each fetcher, alone: its 429 ends it at once (no request sent again), the
// gate closes for the floor (no Retry-After), every other fetcher then sends
// nothing, and when the pause ends the fetcher is sent and goes through.
func TestEachFetcherStopsAtA429(t *testing.T) {
	for _, f := range gateFetchers {
		t.Run(f.name, func(t *testing.T) {
			now := time.Unix(1_790_000_000, 0)
			p, g := newGateAPI(t, &now)
			g.set(f.limit, "-", `{}`)
			ctx := context.Background()

			err := f.call(p, ctx)
			rl, ok := errors.AsType[*catalog.RateLimitError](err)
			if !ok || !rl.Until.Equal(now.Add(MinCooldown)) || rl.Quota {
				t.Fatalf("%s = %v, want a RateLimitError until %v", f.name, err, now.Add(MinCooldown))
			}
			if got := g.calls.Load(); got != f.want {
				t.Fatalf("%s sent %d requests (%v), want %d: none after the 429", f.name, got, g.paths, f.want)
			}
			if r := g.records(); len(r) != 1 || !r[0].until.Equal(now.Add(MinCooldown)) {
				t.Errorf("recorded %v, want the block recorded once", r)
			}
			for _, other := range gateFetchers {
				if err := other.call(p, ctx); !errors.As(err, new(*catalog.RateLimitError)) {
					t.Errorf("while closed, %s = %v, want a RateLimitError", other.name, err)
				}
			}
			if got := g.calls.Load(); got != f.want {
				t.Errorf("while closed: %d requests in all, want %d", got, f.want)
			}

			now = now.Add(MinCooldown)
			g.set(nil, "-", `{}`)
			if err := f.call(p, ctx); err != nil {
				t.Errorf("after the pause: %s = %v, want it through", f.name, err)
			}
			if g.calls.Load() <= f.want {
				t.Error("after the pause nothing was sent")
			}
		})
	}
}

// Every 429 closes the gate, whatever its Retry-After: a valid wait is
// honoured exactly, short ones included; no usable wait gives the floor.
func TestEvery429ClosesTheGate(t *testing.T) {
	base := time.Unix(1_790_000_000, 0)
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"5", 5 * time.Second},
		{" 7 ", 7 * time.Second},
		{"60", time.Minute},
		{"3600", time.Hour},
		{"86400", 24 * time.Hour},
		{"99999999999", MaxRateLimit},
		{"-", MinCooldown}, // none
		{"", MinCooldown},
		{"soon", MinCooldown},
		{"0", MinCooldown},
		{"-5", MinCooldown},
		{"1.5", MinCooldown},
		{base.Add(2 * time.Hour).UTC().Format(http.TimeFormat), 2 * time.Hour},
		{base.Add(-time.Hour).UTC().Format(http.TimeFormat), MinCooldown},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("Retry-After %q", tt.header), func(t *testing.T) {
			now := base
			p, g := newGateAPI(t, &now)
			g.set(func(*http.Request) bool { return true }, tt.header, `{}`)
			ctx := context.Background()
			err := get(p, ctx)
			if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Until.Equal(base.Add(tt.want)) {
				t.Fatalf("the read = %v, want a RateLimitError until +%v", err, tt.want)
			}
			if !strings.Contains(err.Error(), "rate limited until ") {
				t.Errorf("message %q does not say until when", err)
			}
			if r := g.records(); len(r) != 1 || !r[0].until.Equal(base.Add(tt.want)) {
				t.Errorf("recorded %v, want +%v recorded", r, tt.want)
			}
			now = base.Add(tt.want - time.Second)
			if err := get(p, ctx); err == nil || g.calls.Load() != 1 {
				t.Errorf("a second before the end: %v after %d requests, want none sent", err, g.calls.Load())
			}
			now = base.Add(tt.want)
			_ = get(p, ctx)
			if g.calls.Load() != 2 {
				t.Errorf("at the end: %d requests, want the next one sent", g.calls.Load())
			}
		})
	}
}

// A 429 that arrives while a longer block is closed (it was in flight) does
// not shorten it; a quota answer marks the block a quota block.
func TestALongerBlockIsNotShortened(t *testing.T) {
	for _, quota := range []bool{false, true} {
		t.Run(fmt.Sprintf("quota %t", quota), func(t *testing.T) {
			now := time.Unix(1_790_000_000, 0)
			p, g := newGateAPI(t, &now)
			release, entered := make(chan struct{}), make(chan struct{})
			body := `{}`
			if quota {
				body = quotaBody
			}
			orig := http.DefaultTransport
			var n atomic.Int32
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				h := http.Header{}
				b := `{}`
				if n.Add(1) == 1 { // the slow one: answered last, with a short wait
					close(entered)
					<-release
					h.Set("Retry-After", "5")
					b = body
				} else {
					h.Set("Retry-After", "3600")
				}
				return &http.Response{StatusCode: 429, Status: "429", Header: h, Body: io.NopCloser(strings.NewReader(b)), Request: req}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = orig })

			slow := make(chan error, 1)
			go func() { slow <- get(p, context.Background()) }()
			<-entered
			if err := get(p, context.Background()); !errors.As(err, new(*catalog.RateLimitError)) {
				t.Fatalf("the fast read = %v", err)
			}
			close(release)
			err := <-slow
			rl, ok := errors.AsType[*catalog.RateLimitError](err)
			if !ok || !rl.Until.Equal(now.Add(time.Hour)) || rl.Quota != quota {
				t.Errorf("the in-flight read = %v (%+v), want the hour-long block kept, quota %t", err, rl, quota)
			}
			r := g.records()
			wantRecords := 1
			if quota {
				wantRecords = 2
			}
			if len(r) != wantRecords || !r[len(r)-1].until.Equal(now.Add(time.Hour)) || r[len(r)-1].quota != quota {
				t.Errorf("recorded %v, want %d record(s), the last until +1h, quota %t", r, wantRecords, quota)
			}
		})
	}
}

// Spotify's documented quota body makes a quota error: no reset time is
// invented, the wait is Retry-After or the floor, and it is recorded as a
// quota block. Anything else is an ordinary rate limit.
func TestQuotaExceeded(t *testing.T) {
	big := `{"pad":"` + strings.Repeat("x", maxQuotaBody) + `","error":{"reason":"QUOTA_EXCEEDED"}}`
	tests := []struct {
		name, header, body string
		quota              bool
		wait               time.Duration
	}{
		{"documented body, no Retry-After", "-", quotaBody, true, MinCooldown},
		{"documented body, Retry-After", "120", quotaBody, true, 2 * time.Minute},
		{"documented body, malformed Retry-After", "later", quotaBody, true, MinCooldown},
		{"a rate-limit body", "-", `{"error":{"status":429,"message":"API rate limit exceeded"}}`, false, MinCooldown},
		{"another reason", "30", `{"error":{"status":429,"reason":"OTHER"}}`, false, 30 * time.Second},
		{"not JSON", "-", `Too Many Requests`, false, MinCooldown},
		{"empty", "-", ``, false, MinCooldown},
		{"reason past the read limit", "-", big, false, MinCooldown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(1_790_000_000, 0)
			p, g := newGateAPI(t, &now)
			g.set(func(*http.Request) bool { return true }, tt.header, tt.body)
			_, err := p.AlbumTrackRecordsOnce(context.Background(), "al1")
			rl, ok := errors.AsType[*catalog.RateLimitError](err)
			if !ok || rl.Quota != tt.quota || !rl.Until.Equal(now.Add(tt.wait)) {
				t.Fatalf("= %v (%+v), want quota %t until +%v", err, rl, tt.quota, tt.wait)
			}
			if tt.quota && (!strings.Contains(err.Error(), "quota exceeded (no reset time given)") || !strings.Contains(err.Error(), "not retried automatically")) {
				t.Errorf("message %q", err)
			}
			// The block is recorded from the header first; a quota body then
			// records it again as a quota block, with the same end.
			wantRecords := 1
			if tt.quota {
				wantRecords = 2
			}
			if r := g.records(); len(r) != wantRecords || r[len(r)-1].quota != tt.quota || !r[len(r)-1].until.Equal(r[0].until) {
				t.Errorf("recorded %v, want %d, the last with quota %t and the same end", r, wantRecords, tt.quota)
			}
			if g.calls.Load() != 1 {
				t.Errorf("%d requests, want 1", g.calls.Load())
			}
			// After the pause, an ordinary 429 starts a fresh, ordinary block.
			now = now.Add(tt.wait)
			g.set(func(*http.Request) bool { return true }, "10", `{}`)
			_, err = p.AlbumTrackRecordsOnce(context.Background(), "al1")
			if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || rl.Quota {
				t.Errorf("the next block = %v, want an ordinary one", err)
			}
		})
	}
}

// A restored quota block reports itself as one, and sends nothing.
func TestRestoredQuotaBlock(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	p.SetRateLimitedUntil(now.Add(time.Hour), true)
	p.SetRateLimitedUntil(now.Add(time.Minute), false) // never shortens
	_, err := p.SavedAlbumsHead(context.Background())
	if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Quota || !rl.Until.Equal(now.Add(time.Hour)) || g.calls.Load() != 0 {
		t.Errorf("= %v, %d requests; want an hour-long quota block and none sent", err, g.calls.Load())
	}
}

// The admission race: the gate is checked again after the token is got. A
// read that waited for its token while another request's 429 closed the
// gate is not sent.
func TestTheGateIsCheckedAgainBeforeSending(t *testing.T) {
	storeCreds(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := fakeSpotifyHTTP(t, func(n int) (int, string) {
		once.Do(func() { close(entered); <-release })
		return tokenOK(n)
	}, apiOK)
	p := New(expiredSession(), "client", 320)
	t.Cleanup(p.Close)

	done := make(chan error, 1)
	go func() { done <- get(p, context.Background()) }()
	<-entered              // the read is past its first check, waiting for its token
	p.block("3600", false) // another request's 429 closes the gate meanwhile
	close(release)
	err := <-done
	if !errors.As(err, new(*catalog.RateLimitError)) || h.apiCalls.Load() != 0 {
		t.Errorf("the read = %v after %d requests, want a RateLimitError and none sent", err, h.apiCalls.Load())
	}
}

// A cancelled request is not sent, whether it was cancelled before it began
// or while it waited for its token.
func TestACancelledRequestIsNotSent(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, apiOK)
		p := New(goodSession(), "client", 320)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := get(p, ctx); !errors.Is(err, context.Canceled) || h.apiCalls.Load() != 0 {
			t.Errorf("= %v after %d requests, want cancelled, none sent", err, h.apiCalls.Load())
		}
	})
	t.Run("while the token is awaited", func(t *testing.T) {
		storeCreds(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		h := fakeSpotifyHTTP(t, func(n int) (int, string) {
			once.Do(func() { close(entered); <-release })
			return tokenOK(n)
		}, apiOK)
		p := New(expiredSession(), "client", 320)
		t.Cleanup(p.Close)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- get(p, ctx) }()
		<-entered
		cancel()
		close(release)
		if err := <-done; err == nil || h.apiCalls.Load() != 0 {
			t.Errorf("= %v after %d requests, want an error and none sent", err, h.apiCalls.Load())
		}
	})
}

// The write-authentication probe (/v1/me after a write's 401) is a Web API
// request like any other: not sent while the gate is closed, and its 429
// closes the gate. The write is reported not applied either way.
func TestTheWriteProbeHonoursTheGate(t *testing.T) {
	t.Run("the gate closes during the renewal", func(t *testing.T) {
		storeCreds(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		h := fakeSpotifyHTTP(t, func(n int) (int, string) {
			once.Do(func() { close(entered); <-release })
			return tokenOK(n)
		}, func(int, *http.Request) (int, http.Header) { return http.StatusUnauthorized, nil })
		p := New(goodSession(), "client", 320)
		done := make(chan error, 1)
		go func() { done <- post(p, context.Background()) }()
		<-entered // the write got its 401 and is renewing the token
		p.block("3600", false)
		close(release)
		err := <-done
		if !provider.NotApplied(err) || !errors.As(err, new(*catalog.RateLimitError)) || h.apiCalls.Load() != 1 {
			t.Errorf("the write = %v after %d requests; want not applied, a RateLimitError, and no probe sent", err, h.apiCalls.Load())
		}
	})
	t.Run("the probe is rate limited", func(t *testing.T) {
		storeCreds(t)
		h := fakeSpotifyHTTP(t, tokenOK, func(_ int, req *http.Request) (int, http.Header) {
			if req.Method == http.MethodPost {
				return http.StatusUnauthorized, nil
			}
			return http.StatusTooManyRequests, nil // no Retry-After
		})
		p := New(goodSession(), "client", 320)
		now := time.Unix(1_790_000_000, 0)
		p.rate.now = func() time.Time { return now }
		var recorded []time.Time
		p.OnRateLimited(func(until time.Time, _ bool) { recorded = append(recorded, until) })
		err := post(p, context.Background())
		rl, ok := errors.AsType[*catalog.RateLimitError](err)
		if !provider.NotApplied(err) || !ok || !rl.Until.Equal(now.Add(MinCooldown)) || h.apiCalls.Load() != 2 {
			t.Errorf("the write = %v after %d requests; want not applied, the probe's RateLimitError, 2", err, h.apiCalls.Load())
		}
		if len(recorded) != 1 {
			t.Errorf("recorded %v, want the probe's block recorded", recorded)
		}
		if err := get(p, context.Background()); !errors.As(err, new(*catalog.RateLimitError)) || h.apiCalls.Load() != 2 {
			t.Errorf("the next read = %v after %d requests, want none sent", err, h.apiCalls.Load())
		}
	})
}

// Retry-After's edges: the 48 h cap, in seconds and as a date.
func TestRetryAfterParsing(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	for header, want := range map[string]time.Duration{
		"1": time.Second, "172800": MaxRateLimit, "172801": MaxRateLimit, "": MinCooldown, "abc": MinCooldown,
		now.Add(MaxRateLimit + time.Hour).UTC().Format(http.TimeFormat): MaxRateLimit,
	} {
		if got := retryAfter(header, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", header, got, want)
		}
	}
}

// The gate is closed before the block is handed on to be recorded, so a
// record that fails (or never returns quickly) cannot leave it open.
func TestTheGateClosesBeforeTheBlockIsRecorded(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	g.set(func(*http.Request) bool { return true }, "-", `{}`)
	var closedFirst bool
	p.OnRateLimited(func(time.Time, bool) { closedFirst = p.rateLimited() != nil })
	_ = get(p, context.Background())
	if !closedFirst {
		t.Error("the block was recorded while the gate was still open")
	}
}

// A 429 always fails its request with a RateLimitError, even when recording
// the block outlasts it: the error is the block as the 429 left it.
func TestA429FailsItsRequestHoweverLongRecordingTakes(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	g.set(func(*http.Request) bool { return true }, "5", `{}`)
	p.OnRateLimited(func(time.Time, bool) { now = now.Add(time.Minute) }) // a slow catalog write
	for _, call := range []func() error{
		func() error { return get(p, context.Background()) },
		func() error { _, err := p.AlbumTrackRecordsOnce(context.Background(), "al1"); return err },
		func() error { _, err := p.SavedAlbumRecords(context.Background()); return err },
	} {
		err := call() // must not panic on a nil response
		if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || rl.Until.IsZero() {
			t.Errorf("= %v, want the 429's RateLimitError", err)
		}
		now = now.Add(time.Hour)
	}
}

// The gate closes from the header before the body is read: while a slow body
// is read, a second caller sends nothing; the quota body then upgrades the
// block without moving its end.
func TestTheGateClosesBeforeTheBodyIsRead(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	reading, release := make(chan struct{}), make(chan struct{})
	orig := http.DefaultTransport
	var n atomic.Int32
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if n.Add(1) > 1 {
			return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{},
				Body: io.NopCloser(strings.NewReader(`{"id":"me"}`)), Request: req}, nil
		}
		return &http.Response{StatusCode: 429, Status: "429", Header: http.Header{"Retry-After": {"120"}},
			Body: &slowBody{started: reading, release: release, body: strings.NewReader(quotaBody)}, Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	first := make(chan error, 1)
	go func() { first <- get(p, context.Background()) }()
	<-reading
	if err := get(p, context.Background()); !errors.As(err, new(*catalog.RateLimitError)) || n.Load() != 1 {
		t.Errorf("a second caller while the body is read = %v after %d requests; want a RateLimitError, none sent", err, n.Load())
	}
	now = now.Add(30 * time.Second) // the body is slow
	close(release)
	err := <-first
	rl, ok := errors.AsType[*catalog.RateLimitError](err)
	start := now.Add(-30 * time.Second)
	if !ok || !rl.Quota || !rl.Until.Equal(start.Add(2*time.Minute)) {
		t.Errorf("the first = %v (%+v), want a quota block ending at +120s from the answer, not restarted", err, rl)
	}
	if r := g.records(); len(r) != 2 || r[0].quota || !r[1].quota || !r[0].until.Equal(r[1].until) {
		t.Errorf("recorded %v, want the header's block, then the same block as a quota one", r)
	}
}

// slowBody blocks its first read until released.
type slowBody struct {
	started, release chan struct{}
	once             sync.Once
	body             io.Reader
}

func (b *slowBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.body.Read(p)
}
func (*slowBody) Close() error { return nil }

// Every quota answer is an observation of its own, even when the gate's
// flag is still set from an earlier quota block: here a second response,
// admitted before the first quota block closed the gate, has a shorter
// Retry-After (so it does not extend the block) and a slow quota body that
// arrives only after that block has ended (and, in the runtime, after a
// sync the user started has cleared the hold). It is still recorded as a
// quota observation, so the runtime holds and persists it again.
func TestAQuotaAnswerAfterRecoveryIsObservedAgain(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	entered, answer := make(chan struct{}), make(chan struct{})
	reading, release := make(chan struct{}), make(chan struct{})
	orig := http.DefaultTransport
	var n atomic.Int32
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if n.Add(1) == 1 { // the slow one: admitted first, answered later
			close(entered)
			<-answer
			return &http.Response{StatusCode: 429, Status: "429", Header: http.Header{"Retry-After": {"1"}},
				Body: &slowBody{started: reading, release: release, body: strings.NewReader(quotaBody)}, Request: req}, nil
		}
		return &http.Response{StatusCode: 429, Status: "429", Header: http.Header{"Retry-After": {"5"}},
			Body: io.NopCloser(strings.NewReader(quotaBody)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	slow := make(chan error, 1)
	go func() { slow <- get(p, context.Background()) }()
	<-entered
	if err := get(p, context.Background()); !errors.As(err, new(*catalog.RateLimitError)) {
		t.Fatalf("the first quota answer = %v", err)
	}
	before := len(g.records()) // its header's block, then its quota
	close(answer)
	<-reading                       // the slow answer's header did not extend the 5 s block
	now = now.Add(10 * time.Second) // the block has ended; recovery happens meanwhile
	close(release)
	err := <-slow
	if rl, ok := errors.AsType[*catalog.RateLimitError](err); !ok || !rl.Quota {
		t.Errorf("the slow answer = %v, want a quota RateLimitError", err)
	}
	r := g.records()
	if len(r) != before+1 || !r[len(r)-1].quota {
		t.Errorf("records %v (had %d), want one more, a quota observation", r, before)
	}
}

// A request whose context was cancelled with a cause (the runtime's fence of
// a background sync) is not sent, and fails with that cause.
func TestAFencedRequestIsNotSentAndSaysWhy(t *testing.T) {
	storeCreds(t)
	h := fakeSpotifyHTTP(t, tokenOK, apiOK)
	p := New(goodSession(), "client", 320)
	fence := &catalog.RateLimitError{Quota: true}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(fence)
	for _, call := range []func() error{
		func() error { return get(p, ctx) },
		func() error { _, err := p.SavedAlbumRecords(ctx); return err },
	} {
		if err := call(); !errors.Is(err, fence) || h.apiCalls.Load() != 0 {
			t.Errorf("= %v after %d requests, want the fence and none sent", err, h.apiCalls.Load())
		}
	}
}
