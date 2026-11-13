// ddsonic: the Web API rate-limit gate. Spotify answers too many requests
// with a 429, usually with a Retry-After in seconds, and a development-mode
// app's exhausted quota with a 429 whose body's error.reason is
// QUOTA_EXCEEDED (developer.spotify.com/documentation/web-api/concepts/
// rate-limits and .../concepts/quota-modes). Every 429 closes the gate:
// while it is closed, every Web API request fails at once with a
// *catalog.RateLimitError instead of reaching Spotify. No request waits a
// rate limit out and sends again.

package spotify

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// MinCooldown is how long the gate closes for a 429 that gives no usable
// wait: none, malformed, zero, negative, or already past.
const MinCooldown = 60 * time.Second

// maxQuotaBody bounds how much of a 429's body is read for its reason.
const maxQuotaBody = 4 << 10

// rateGate holds the end of Spotify's current block, and whether a quota
// answer started or extended it.
type rateGate struct {
	mu      sync.Mutex
	until   time.Time
	quota   bool
	onBlock func(until time.Time, quota bool)
	now     func() time.Time // replaced in tests
}

// clock is the gate's time; the caller holds mu.
func (g *rateGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// SetRateLimitedUntil restores a block recorded by an earlier run. It never
// shortens a block; quota marks a block still running as a quota block.
func (p *SpotifyProvider) SetRateLimitedUntil(until time.Time, quota bool) {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	if until.After(p.rate.until) {
		p.rate.until = until
	}
	if quota && p.rate.until.After(p.rate.clock()) {
		p.rate.quota = true
	}
}

// OnRateLimited sets fn to be called with the block's end each time a 429
// starts or extends a block, short ones included, or makes it a quota block,
// so it can be recorded. fn may run on any goroutine, and calls may land out
// of order.
func (p *SpotifyProvider) OnRateLimited(fn func(until time.Time, quota bool)) {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	p.rate.onBlock = fn
}

// rateLimited returns a *catalog.RateLimitError while a block lasts, else nil.
func (p *SpotifyProvider) rateLimited() error {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	if left := p.rate.until.Sub(p.rate.clock()); left > 0 {
		return &catalog.RateLimitError{RetryAfter: left, Until: p.rate.until, Quota: p.rate.quota}
	}
	return nil
}

// block closes the gate for the wait retryAfter header asks for, unless it is
// already closed for longer, and returns the error the request fails with.
// It never shortens a block.
func (p *SpotifyProvider) block(header string, quota bool) error {
	p.rate.mu.Lock()
	now := p.rate.clock()
	if !p.rate.until.After(now) {
		p.rate.quota = false // the last block has ended; this one starts afresh
	}
	until := now.Add(retryAfter(header, now))
	changed := false
	if until.After(p.rate.until) {
		p.rate.until, changed = until, true
	}
	if quota && !p.rate.quota {
		p.rate.quota, changed = true, true
	}
	end, q, fn := p.rate.until, p.rate.quota, p.rate.onBlock
	p.rate.mu.Unlock()
	if changed && fn != nil {
		fn(end, q) // outside the lock: it may write to the catalog
	}
	return p.rateLimited()
}

// tooManyRequests closes the gate for a 429 and returns the request's error.
// It reads the body (bounded) for Spotify's quota reason, and closes it.
func (p *SpotifyProvider) tooManyRequests(resp *http.Response) error {
	quota := isQuotaExceeded(resp.Body)
	resp.Body.Close()
	return p.block(resp.Header.Get("Retry-After"), quota)
}

// retryAfter is the wait a 429's Retry-After asks for: whole seconds (the
// form Spotify documents) or an HTTP date, capped at MaxRateLimit; or
// MinCooldown when it gives no usable wait.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return MinCooldown
	}
	if secs, err := strconv.ParseInt(h, 10, 64); err == nil {
		if secs <= 0 {
			return MinCooldown
		}
		return time.Duration(min(secs, maxRetryAfterSecs)) * time.Second
	}
	if at, err := http.ParseTime(h); err == nil && at.After(now) {
		return min(at.Sub(now), MaxRateLimit)
	}
	return MinCooldown
}

// isQuotaExceeded reports whether a 429's body is Spotify's documented quota
// answer, {"error":{"status":429,...,"reason":"QUOTA_EXCEEDED"}}. Anything
// else, an unreadable body included, is an ordinary rate limit.
func isQuotaExceeded(body io.Reader) bool {
	if body == nil {
		return false
	}
	var e struct {
		Error struct {
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(body, maxQuotaBody)).Decode(&e); err != nil {
		return false
	}
	return e.Error.Reason == "QUOTA_EXCEEDED"
}
