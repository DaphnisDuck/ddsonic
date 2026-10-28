// ddsonic: the Web API rate-limit gate. Spotify answers sustained use with a
// 429 whose Retry-After can run to many hours. While that block lasts, every
// Web API request fails at once with a *catalog.RateLimitError instead of
// reaching Spotify (which may extend the block) or sleeping in the request.

package spotify

import (
	"sync"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// maxInlineRetryWait is the longest Retry-After a retrying request sleeps
// through; a longer one closes the gate and fails the request.
const maxInlineRetryWait = 30 * time.Second

// rateGate holds the end of Spotify's current block.
type rateGate struct {
	mu      sync.Mutex
	until   time.Time
	onBlock func(until time.Time)
	now     func() time.Time // replaced in tests
}

func (g *rateGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// SetRateLimitedUntil restores a block recorded by an earlier run.
func (p *SpotifyProvider) SetRateLimitedUntil(until time.Time) {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	if until.After(p.rate.until) {
		p.rate.until = until
	}
}

// OnRateLimited sets fn to be called with the block's end each time a 429
// starts or extends a block longer than maxInlineRetryWait, so it can be
// recorded. fn may run on any goroutine, and calls may land out of order.
func (p *SpotifyProvider) OnRateLimited(fn func(until time.Time)) {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	p.rate.onBlock = fn
}

// rateLimited returns a *catalog.RateLimitError while a block lasts, else nil.
func (p *SpotifyProvider) rateLimited() error {
	p.rate.mu.Lock()
	defer p.rate.mu.Unlock()
	if left := p.rate.until.Sub(p.rate.clock()); left > 0 {
		return &catalog.RateLimitError{RetryAfter: left, Until: p.rate.until}
	}
	return nil
}

// block closes the gate for wait, unless it is already closed for longer,
// and returns the error the request fails with.
func (p *SpotifyProvider) block(wait time.Duration) error {
	p.rate.mu.Lock()
	until := p.rate.clock().Add(wait)
	extended := until.After(p.rate.until)
	if extended {
		p.rate.until = until
	}
	fn := p.rate.onBlock
	p.rate.mu.Unlock()
	if extended && fn != nil && wait > maxInlineRetryWait {
		fn(until)
	}
	return p.rateLimited()
}
