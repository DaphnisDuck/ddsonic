package spotify

// ddsonic: the token coordinator, the one authority for a sign-in's Web API
// token and its stored credential.
//
// One coordinator belongs to one sign-in: it is made with the session a
// builder produces and closed when that sign-in is replaced or its session
// closed. It holds no reference to its session.
//
//   - It caches the access token and its expiry itself. Every refresh, on
//     expiry or forced after a 401, is the same routine, and at most one runs
//     at a time (a flight); a caller that needs a newer token while one is in
//     the air waits for that one.
//   - Every token it publishes is a new revision. A 401 or a refusal carries
//     the lease it is about and counts only while that lease is current.
//   - Its credential is written to the file only in a durable section: the
//     file's lock (credStore.mu) is held from before a result is accepted
//     until the write it calls for has returned, and closing needs that lock
//     too. So a result is accepted and written, or dropped, and never
//     accepted and then skipped by a close, nor written after one.
//
// Lock order: SpotifyProvider.mu, Session.mu, credStore.mu, tokenCoordinator.mu.
// The coordinator's own lock is never held across I/O of any kind.
//
// docs/ddsonic/design/spotify-token-coordinator.md has the argument.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/playlist"
)

// tokenExpiryMargin is how long before its expiry a token stops being used,
// the margin oauth2 keeps.
const tokenExpiryMargin = 10 * time.Second

// errSuperseded ends a wait on a coordinator that was closed meanwhile: its
// sign-in was replaced, or its session closed. Callers turn it into what
// that means for them; it does not leave the package.
var errSuperseded = errors.New("spotify: the sign-in was replaced")

// What the coordinator reaches outside itself, as variables so a test can
// stand in for Spotify and for the file, and can stop it at the points where
// orderings are decided. The three hooks are nil outside tests.
var (
	refreshTokens = silentTokenRefresh
	writeCreds    = saveCreds
	// flightJoined runs when a caller has started to wait for a flight that
	// another caller began.
	flightJoined func()
	// durableSnapshot runs inside a durable section, after its memory step
	// and before its write.
	durableSnapshot func()
	// closeWaiting runs when a close or a replacement is about to take the
	// file's lock.
	closeWaiting func()
)

// lease says which token a request was sent with: the coordinator's revision
// when the token was handed out.
type lease struct {
	c   *tokenCoordinator
	rev uint64
}

// refreshFlight is one refresh in the air. done is closed after its result
// has been settled and written; the result fields are read after that.
type refreshFlight struct {
	base   uint64 // the coordinator's revision when the flight began
	done   chan struct{}
	lease  lease
	access string
	err    error
}

// tokenCoordinator is a sign-in's token state. mu guards every field below
// it; closed and dirty are written only with credStore.mu held as well.
type tokenCoordinator struct {
	clientID string
	ctx      context.Context // ends at close; every flight runs on it
	cancel   context.CancelFunc
	now      func() time.Time

	mu     sync.Mutex
	closed bool
	// creds is the sign-in's credential; dirty says it differs from what
	// this coordinator last wrote. A new coordinator is dirty: its seed is
	// not on disk in this form.
	creds storedCreds
	dirty bool
	// rev is the revision of the current token, zero before the first.
	rev    uint64
	access string
	expiry time.Time // zero: does not expire, as oauth2 reads it
	// refused says Spotify refused the token of revision refusedRev.
	refused    bool
	refusedRev uint64
	flight     *refreshFlight
}

// credStore is the credential file: whose credential it holds. mu is the
// durable-commit guard; it is held across the file write and nothing else.
var credStore struct {
	mu    sync.Mutex
	owner *tokenCoordinator
}

// newTokenCoordinator is the coordinator of a sign-in whose credential is
// creds. seed is the token the builder got with it, or nil when it got none
// (a stored session whose refresh failed for a passing reason).
func newTokenCoordinator(clientID string, creds storedCreds, seed *oauth2.Token) *tokenCoordinator {
	ctx, cancel := context.WithCancel(context.Background())
	c := &tokenCoordinator{clientID: clientID, ctx: ctx, cancel: cancel, now: time.Now, creds: creds, dirty: true}
	if seed != nil && seed.AccessToken != "" {
		c.rev, c.access, c.expiry = 1, seed.AccessToken, seed.Expiry
	}
	return c
}

func (c *tokenCoordinator) refusedLocked() bool { return c.refused && c.refusedRev == c.rev }

func (c *tokenCoordinator) usableLocked() bool {
	return c.access != "" && (c.expiry.IsZero() || c.now().Before(c.expiry.Add(-tokenExpiryMargin)))
}

// token returns the current token and its lease, refreshing it first when
// it has expired or there is none. A refused sign-in is
// playlist.ErrNeedsAuth; a closed coordinator is errSuperseded.
func (c *tokenCoordinator) token(ctx context.Context) (lease, string, error) {
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return lease{}, "", errSuperseded
	case c.refusedLocked():
		c.mu.Unlock()
		return lease{}, "", errSignInRefused
	case c.usableLocked():
		l, access := lease{c, c.rev}, c.access
		c.mu.Unlock()
		return l, access, nil
	}
	basis := lease{c, c.rev}
	c.mu.Unlock()
	return c.renew(ctx, basis)
}

// renew returns a token newer than basis: the current one when it has been
// renewed since, else the result of the one flight, which it starts or
// joins. A caller that stops waiting leaves the flight to finish.
func (c *tokenCoordinator) renew(ctx context.Context, basis lease) (lease, string, error) {
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return lease{}, "", errSuperseded
	case c.refusedLocked():
		c.mu.Unlock()
		return lease{}, "", errSignInRefused
	case c.rev > basis.rev && c.usableLocked():
		l, access := lease{c, c.rev}, c.access
		c.mu.Unlock()
		return l, access, nil
	}
	f, joined := c.flight, true
	if f == nil {
		refreshToken := c.creds.RefreshToken
		if refreshToken == "" {
			c.mu.Unlock()
			return lease{}, "", fmt.Errorf("spotify: web api token unavailable, run '%s spotify reset' and sign in again: %w", appmeta.ClientName(), playlist.ErrNeedsAuth)
		}
		f, joined = &refreshFlight{base: c.rev, done: make(chan struct{})}, false
		c.flight = f
		go c.fly(f, refreshToken)
	}
	c.mu.Unlock()
	if joined && flightJoined != nil {
		flightJoined()
	}
	select {
	case <-f.done:
		return f.lease, f.access, f.err
	case <-ctx.Done():
		return lease{}, "", ctx.Err()
	}
}

// fly is a flight: the refresh, on the coordinator's own context and with no
// lock held, and then its settlement.
func (c *tokenCoordinator) fly(f *refreshFlight, refreshToken string) {
	tok, err := refreshTokens(c.ctx, c.clientID, refreshToken)
	c.settle(f, tok, err)
}

// settle is the one place a token is published, or a refresh's refusal
// recorded. It is a durable section: the answer is accepted and its rotated
// refresh token written, or the coordinator is closed and the answer dropped.
func (c *tokenCoordinator) settle(f *refreshFlight, tok *oauth2.Token, err error) {
	credStore.mu.Lock()
	c.mu.Lock()
	if c.flight == f {
		c.flight = nil
	}
	if c.closed {
		// Never accepted: the sign-in was replaced or closed first.
		c.mu.Unlock()
		credStore.mu.Unlock()
		f.err = errSuperseded
		close(f.done)
		return
	}
	switch {
	case err == nil:
		c.rev++
		c.access, c.expiry = tok.AccessToken, tok.Expiry
		if tok.RefreshToken != "" && tok.RefreshToken != c.creds.RefreshToken {
			c.creds.RefreshToken, c.dirty = tok.RefreshToken, true
		}
		f.lease, f.access = lease{c, c.rev}, c.access
	case isInvalidGrant(err):
		// Held against the token it was fetched to replace, which is still
		// the current one: no other flight ran meanwhile.
		c.refused, c.refusedRev = true, c.rev
		f.err = errSignInRefused
	default:
		f.err = fmt.Errorf("spotify: web api token unavailable: %w", err)
	}
	c.writeOwnedLocked()
	credStore.mu.Unlock()
	close(f.done)
}

// writeOwnedLocked is a durable section's write step: when c is the file's
// owner and its credential has changed since it last wrote it, the file is
// written. The caller holds credStore.mu and c.mu; c.mu is released before
// the write and not held on return. A failed write is logged and tried again
// by the next section.
func (c *tokenCoordinator) writeOwnedLocked() {
	owned := credStore.owner == c && c.dirty && !c.closed
	snapshot := c.creds
	c.mu.Unlock()
	if durableSnapshot != nil {
		durableSnapshot()
	}
	if !owned {
		return
	}
	if err := writeCreds(&snapshot); err != nil {
		applog.UserError("spotify: failed to save credentials: %v", err)
		return
	}
	c.mu.Lock()
	c.dirty = false
	c.mu.Unlock()
}

// claimCreds makes c the file's owner and writes its credential: the session
// it belongs to has been taken. old, when not nil, is the coordinator of the
// sign-in c replaces; it is closed in the same section, so nothing of the old
// sign-in can be written after the new one's credential.
func claimCreds(c, old *tokenCoordinator) {
	if closeWaiting != nil && old != nil {
		closeWaiting()
	}
	credStore.mu.Lock()
	defer credStore.mu.Unlock()
	if old != nil && old != c {
		old.mu.Lock()
		old.closeLocked()
		old.mu.Unlock()
		if credStore.owner == old {
			credStore.owner = nil
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	credStore.owner = c
	c.writeOwnedLocked()
}

// close ends the coordinator: nothing is published, refused or written for
// it afterwards. A credential it had accepted and could not write is tried
// once more first, while it is still open; nothing new is accepted.
func (c *tokenCoordinator) close() {
	if c == nil {
		return
	}
	if closeWaiting != nil {
		closeWaiting()
	}
	credStore.mu.Lock()
	defer credStore.mu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.writeOwnedLocked()
	c.mu.Lock()
	c.closeLocked()
	c.mu.Unlock()
	if credStore.owner == c {
		credStore.owner = nil
	}
}

func (c *tokenCoordinator) closeLocked() {
	c.closed = true
	c.cancel()
}

// refuse records that the Web API refused the token of l after it had been
// renewed. It reports whether that is the sign-in's token still: a refusal
// of a token that has been replaced since changes nothing.
func (c *tokenCoordinator) refuse(l lease) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || l.c != c || l.rev != c.rev {
		return false
	}
	c.refused, c.refusedRev = true, c.rev
	return true
}

// setIdentity records the librespot half of the credential, after a silent
// rebuild of the session: a durable section.
func (c *tokenCoordinator) setIdentity(username string, data []byte, deviceID string) {
	credStore.mu.Lock()
	defer credStore.mu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.creds.Username, c.creds.Data, c.creds.DeviceID, c.dirty = username, data, deviceID, true
	c.writeOwnedLocked()
}

// needsSignIn reports whether the coordinator cannot produce a token until
// the user signs in again: its token was refused, or it never had one.
func (c *tokenCoordinator) needsSignIn() bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refusedLocked() || c.rev == 0
}

// snapshot is the sign-in's credential as it stands.
func (c *tokenCoordinator) snapshot() storedCreds {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creds
}
