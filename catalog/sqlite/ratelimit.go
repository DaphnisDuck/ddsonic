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
