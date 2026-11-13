package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RateLimitedUntil returns the end of provider's recorded rate-limit block,
// or the zero time when none was recorded.
func (s *Store) RateLimitedUntil(ctx context.Context, provider string) (time.Time, error) {
	var ms int64
	err := s.db.QueryRowContext(ctx, `SELECT until FROM rate_limits WHERE provider = ?`, provider).Scan(&ms)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("rate limit %s: %w", provider, err)
	}
	return time.UnixMilli(ms), nil
}

// SetRateLimitedUntil records the end of provider's rate-limit block. It
// never shortens a recorded block: callers may record out of order.
func (s *Store) SetRateLimitedUntil(ctx context.Context, provider string, until time.Time) error {
	_, err := s.wdb.ExecContext(ctx, `INSERT INTO rate_limits (provider, until) VALUES (?, ?)
		ON CONFLICT (provider) DO UPDATE SET until = MAX(until, excluded.until)`, provider, until.UnixMilli())
	if err != nil {
		return fmt.Errorf("record rate limit %s: %w", provider, err)
	}
	return nil
}

// quotaKey is the rate_limits row that marks provider's quota as exhausted
// (ddsonic): a second row, not a new column, so an older ddsonic opens the
// catalog unchanged and ignores it. Its until is when the mark was set.
func quotaKey(provider string) string { return provider + ":quota" }

// QuotaExceeded reports whether provider's quota was recorded as exhausted
// and not cleared since.
func (s *Store) QuotaExceeded(ctx context.Context, provider string) (bool, error) {
	at, err := s.RateLimitedUntil(ctx, quotaKey(provider))
	if err != nil {
		return false, fmt.Errorf("quota %s: %w", provider, err)
	}
	return !at.IsZero(), nil
}

// SetQuotaExceeded records provider's quota as exhausted at at.
func (s *Store) SetQuotaExceeded(ctx context.Context, provider string, at time.Time) error {
	return s.SetRateLimitedUntil(ctx, quotaKey(provider), at)
}

// ClearQuotaExceeded removes provider's exhausted-quota mark if it was set
// before before. A mark set at before or later (a newer observation than
// the success that clears it) is kept.
//
// Limitation: the comparison is by wall-clock milliseconds. If the clock
// steps backwards between a sync's start and a later quota observation,
// that observation's mark can look older and be deleted. The running
// process stays held (its in-memory observation count is not clock based);
// only a restart after that would miss the hold. A persisted revision would
// remove this, at the cost of a store read at every sync start.
//
// A mark written late (after the clear, or stamped after the clearing
// sync began) survives it and holds again after a restart: an over-block,
// never an extra request. See keepRateLimit.
func (s *Store) ClearQuotaExceeded(ctx context.Context, provider string, before time.Time) error {
	if _, err := s.wdb.ExecContext(ctx, `DELETE FROM rate_limits WHERE provider = ? AND until < ?`, quotaKey(provider), before.UnixMilli()); err != nil {
		return fmt.Errorf("clear quota %s: %w", provider, err)
	}
	return nil
}
