package sqlite

import (
	"context"
	"testing"
	"time"
)

func TestRateLimitedUntilRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if got, err := s.RateLimitedUntil(ctx, "spotify"); err != nil || !got.IsZero() {
		t.Fatalf("before any block: %v, %v; want zero", got, err)
	}
	for _, until := range []time.Time{time.UnixMilli(1_790_000_000_000), time.UnixMilli(1_790_000_600_000)} {
		if err := s.SetRateLimitedUntil(ctx, "spotify", until); err != nil {
			t.Fatal(err)
		}
		if got, err := s.RateLimitedUntil(ctx, "spotify"); err != nil || !got.Equal(until) {
			t.Errorf("got %v, %v; want %v", got, err, until)
		}
	}
	// A block recorded late, and shorter, does not shorten the stored one.
	if err := s.SetRateLimitedUntil(ctx, "spotify", time.UnixMilli(1_790_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.RateLimitedUntil(ctx, "spotify"); !got.Equal(time.UnixMilli(1_790_000_600_000)) {
		t.Errorf("after an earlier end: %v, want the later one kept", got)
	}
	if got, _ := s.RateLimitedUntil(ctx, "youtube"); !got.IsZero() {
		t.Errorf("another provider: %v, want zero", got)
	}
}

// ddsonic: the quota mark is a row of its own, which RateLimitedUntil of the
// provider (all an older ddsonic reads) never sees, and it can be cleared.
func TestQuotaMark(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	if held, err := s.QuotaExceeded(ctx, "spotify"); err != nil || held {
		t.Fatalf("before any mark: %t, %v", held, err)
	}
	if err := s.SetQuotaExceeded(ctx, "spotify", time.UnixMilli(1_790_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if held, err := s.QuotaExceeded(ctx, "spotify"); err != nil || !held {
		t.Errorf("after the mark: %t, %v; want held", held, err)
	}
	if got, err := s.RateLimitedUntil(ctx, "spotify"); err != nil || !got.IsZero() {
		t.Errorf("the provider's block = %v, %v; want the mark not read as one", got, err)
	}
	if held, _ := s.QuotaExceeded(ctx, "youtube"); held {
		t.Error("another provider is held")
	}
	// A clear by a success that began before the mark was set does not
	// remove it; one that began after it does.
	set := time.UnixMilli(1_790_000_000_000)
	for _, began := range []time.Time{set.Add(-time.Second), set} {
		if err := s.ClearQuotaExceeded(ctx, "spotify", began); err != nil {
			t.Fatal(err)
		}
		if held, _ := s.QuotaExceeded(ctx, "spotify"); !held {
			t.Errorf("a success that began at %v cleared a mark set at %v", began, set)
		}
	}
	if err := s.ClearQuotaExceeded(ctx, "spotify", set.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if held, err := s.QuotaExceeded(ctx, "spotify"); err != nil || held {
		t.Errorf("after clearing: %t, %v", held, err)
	}
	if err := s.ClearQuotaExceeded(ctx, "spotify", time.Now()); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}
