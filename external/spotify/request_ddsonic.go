package spotify

// ddsonic: one Web API request, from the first send to its last.
//
// A logical request is one apiCall. What it has spent on recovering from a
// 401 and on waiting out rate limits lives in it for as long as the request
// does, so neither can reset the other: a read renews its token once per
// sign-in however many rate-limit waits lie in between.
//
// The loop goes back to its start only through backAuth or backRate, and
// each checks its limit before it moves. So a read makes at most 11 attempts
// (1, 3 after auth, 7 after waits) and a write at most 9 (it is never sent
// again after a 401; it may add one read-only check). A token is renewed
// after a 401 at most once per sign-in; a token that has merely expired is
// refreshed by the coordinator whenever it is asked for, as it always was,
// and that is not counted against the request.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/playlist"
)

const (
	// maxAuthRounds is how often one request goes round again because of
	// its sign-in: a renewed token, or a sign-in replaced under it.
	maxAuthRounds = 3
	// maxRateRetries is how many rate limits a waiting request meets before
	// it gives up: upstream's eight attempts.
	maxRateRetries = 8
)

// rateWait waits out a rate limit, or ends with ctx. A variable so a test
// need not sleep, and can act during the wait.
var rateWait = func(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// apiCall is one logical Web API request and what it has spent.
type apiCall struct {
	method, path string
	query        url.Values
	body         []byte
	contentType  string
	// waitRate: wait out a short rate limit and send again (webAPIWithBody);
	// otherwise a 429 is returned as it came (webAPIOnce).
	waitRate bool
	// pin is the sign-in a write belongs to; nil for a read.
	pin *signInPin

	// renewedIn is the sign-in whose token this request has renewed after a
	// 401: its one renewal there is spent.
	renewedIn *tokenCoordinator
	authRound int
	rateTry   int
}

// backAuth takes the request round again for its sign-in's sake, unless it
// has done so too often.
func (c *apiCall) backAuth() error {
	if c.authRound == maxAuthRounds {
		return errors.New("spotify: the Spotify session kept changing during the request; try again")
	}
	c.authRound++
	return nil
}

// send makes one HTTP request to the Web API with access as its token.
func sendWebAPI(ctx context.Context, method, path string, query url.Values, body []byte, contentType, access string) (*http.Response, error) {
	u, _ := url.Parse("https://api.spotify.com")
	u = u.JoinPath(path)
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return webHTTPClient.Do(req) // bounded
}

// errNoToken is a session that has no coordinator to ask for a token.
func errNoToken() error {
	return fmt.Errorf("spotify: web api token unavailable, run '%s spotify reset' and sign in again: %w", appmeta.ClientName(), playlist.ErrNeedsAuth)
}

// request carries one logical request through: it reads the session, keeps
// a write to its action's sign-in, gets the token, sends, and deals with
// what the answer says about the sign-in or the rate limit. Any answer other
// than a 401, and a 429 of a request that does not wait, is returned as it
// came.
//
// A 401 is answered by renewing the token, once per sign-in for this
// request, and sending a read again; a 401 the renewed token gets too is
// playlist.ErrNeedsAuth. A write is never sent again after a 401: so that it
// still learns what its 401 meant, the renewed token is tried on one
// read-only request.
func (p *SpotifyProvider) request(ctx context.Context, c *apiCall) (*http.Response, error) {
	write := isWrite(c.method)
	for {
		if err := p.rateLimited(); err != nil { // blocked, do not ask
			return nil, err
		}
		// Read under the lock: the catalog sync calls this from a
		// background goroutine while Close may clear the session.
		sess := p.sessionNow()
		if sess == nil {
			return nil, playlist.ErrNeedsAuth
		}
		if c.pin != nil && !c.pin.holds(sess) {
			return nil, errSignInChanged
		}
		v := sess.viewNow()
		switch {
		case v.closed:
			return nil, errSessionClosed
		case v.playbackRefused:
			return nil, errSignInRefused
		case v.tokens == nil:
			return nil, errNoToken()
		}
		l, access, err := v.tokens.token(ctx)
		if errors.Is(err, errSuperseded) {
			// The sign-in was replaced while the token was awaited.
			if sess.viewNow().closed {
				return nil, errSessionClosed
			}
			if write {
				return nil, errSignInChanged
			}
			if err := c.backAuth(); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if c.pin != nil && !c.pin.holds(sess) { // again, as the last thing before sending
			return nil, errSignInChanged
		}
		resp, err := sendWebAPI(ctx, c.method, c.path, c.query, c.body, c.contentType, access)
		if err != nil {
			return nil, err
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			resp.Body.Close()
			if err := p.unauthorized(ctx, c, sess, l); err != nil {
				return nil, err
			}
		case http.StatusTooManyRequests:
			if !c.waitRate {
				return resp, nil
			}
			resp.Body.Close()
			if err := p.rateLimitedAnswer(ctx, c, resp.Header.Get("Retry-After")); err != nil {
				return nil, err
			}
		default:
			return resp, nil
		}
	}
}

// unauthorized handles a 401 for a request sent with l. A nil return sends a
// read again; a write always ends here.
func (p *SpotifyProvider) unauthorized(ctx context.Context, c *apiCall, sess *Session, l lease) error {
	tokens := l.c
	if c.renewedIn == tokens {
		// The renewed token was refused too: the sign-in is, unless that
		// token has been replaced meanwhile, when this answer is about the
		// old one and the token that stands is asked.
		if tokens.refuse(l) {
			return errSignInRefused
		}
		return c.backAuth()
	}
	c.renewedIn = tokens
	write := isWrite(c.method)
	_, _, err := tokens.renew(ctx, l)
	switch {
	case errors.Is(err, errSuperseded):
		// The sign-in was replaced: a read asks the new one, a write was
		// not asked for under it.
		if write {
			return errSignInChanged
		}
		return c.backAuth()
	case err != nil:
		// A write that got the 401 was refused, whatever became of the
		// renewal: it is not applied, and says so along with the cause.
		return rejectedWrite(c.method, err)
	case !write:
		return c.backAuth()
	}

	// A write is not sent again. The renewed token is tried on one read.
	probe, access, err := tokens.token(ctx)
	if errors.Is(err, errSuperseded) {
		return errSignInChanged
	}
	if err != nil {
		return rejectedWrite(c.method, err)
	}
	resp, err := sendWebAPI(ctx, http.MethodGet, "/v1/me", nil, nil, "", access)
	if err != nil {
		return rejectedWrite(c.method, err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		if tokens.refuse(probe) {
			return errSignInRefused
		}
		return errSignInChanged
	}
	return notSent("spotify: the request was not authorised, so it was not carried out, and has not been sent again; the sign-in has been renewed, so try once more")
}

// rateLimitedAnswer handles a 429 for a request that waits: it sleeps the
// time Spotify asks for and returns nil to send again, or fails when that
// time is long (the gate closes for it) or the request has waited too often.
func (p *SpotifyProvider) rateLimitedAnswer(ctx context.Context, c *apiCall, retryAfter string) error {
	wait := time.Duration(1<<uint(c.rateTry)) * time.Second
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			wait = time.Duration(min(secs, maxRetryAfterSecs)) * time.Second // capped
			if wait > maxInlineRetryWait {                                   // a long block fails now
				return p.block(wait)
			}
		}
	}
	// On the last attempt there's no retry after the wait, so don't sleep
	// just to give up; fail now. After reading Retry-After, so a long block
	// on it closes the gate.
	if c.rateTry == maxRateRetries-1 {
		return fmt.Errorf("spotify: web api rate-limited on %s after %d retries (try re-authenticating)", c.path, maxRateRetries)
	}
	c.rateTry++
	applog.UserWarn("spotify: web api rate-limited on %s, retrying in %v (attempt %d/%d)", c.path, wait, c.rateTry, maxRateRetries)
	return rateWait(ctx, wait)
}
