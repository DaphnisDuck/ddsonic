package spotify

// ddsonic: one Web API request, from the first send to its last.
//
// A logical request is one apiCall. What it has spent on recovering from a
// 401 lives in it for as long as the request does.
//
// A 429 ends the request: it closes the rate-limit gate (ratelimit.go) and
// the request fails with a *catalog.RateLimitError. Nothing waits a rate
// limit out and sends again, in the background or the foreground. The gate
// is checked immediately before every send, after the token is got, so a
// request that waited for its token while another request's 429 closed the
// gate is not sent. A request already sent cannot be recalled; its own 429
// feeds the gate, which keeps the later end.
//
// The loop goes back to its start only through backAuth, which checks its
// limit before it moves. So a read makes at most 4 attempts (1, 3 after
// auth) and a write at most 1 (it is never sent again after a 401; it may add
// one read-only check). A token is renewed after a 401 at most once per
// sign-in; a token that has merely expired is refreshed by the coordinator
// whenever it is asked for, as it always was, and that is not counted
// against the request.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/playlist"
)

// maxAuthRounds is how often one request goes round again because of its
// sign-in: a renewed token, or a sign-in replaced under it.
const maxAuthRounds = 3

// beforeSend runs immediately before every Web API send, after the last
// check; nil in production. A test sets it to act at that point.
var beforeSend func(path string)

// apiCall is one logical Web API request and what it has spent.
type apiCall struct {
	method, path string
	query        url.Values
	body         []byte
	contentType  string
	// pin is the sign-in a write belongs to; nil for a read.
	pin *signInPin

	// renewedIn is the sign-in whose token this request has renewed after a
	// 401: its one renewal there is spent.
	renewedIn *tokenCoordinator
	authRound int
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

// admit is the last check before a send: the request's context, quitting,
// and the rate-limit gate, which another request's 429 may have closed while
// this one waited for its token. It holds no lock across the send.
func (p *SpotifyProvider) admit(ctx context.Context) error {
	if ctx.Err() != nil {
		return context.Cause(ctx) // ctx.Err() unless a cause was given (a fenced sync)
	}
	if p.calls.stopping() {
		return errQuitting
	}
	return p.rateLimited()
}

// errQuitting fails a Web API request that quitting refused before it was
// admitted to be sent (StopRequests).
var errQuitting = errors.New("spotify: quitting; the request was not sent")

// webCalls counts the Web API requests in progress, from request's start to
// its return, so quitting can wait for their answers: a 429 is recorded
// (OnRateLimited) before its request returns. Once stopped, no request
// starts, and none is admitted to be sent; one admitted just before may
// still be sent, and is still counted.
type webCalls struct {
	mu      sync.Mutex
	n       int
	stopped bool
	idle    chan struct{} // made by stop; closed once n is 0 after it
}

// begin counts a request in, or refuses it once stopped.
func (w *webCalls) begin() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return false
	}
	w.n++
	return true
}

// end counts a request out.
func (w *webCalls) end() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.n--
	if w.n == 0 && w.stopped { // once: nothing begins after stop
		close(w.idle)
	}
}

func (w *webCalls) stopping() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopped
}

// StopRequests is for quitting: from now on Web API requests are refused at
// admission (request's start, and admit before each send), and the channel
// it returns is closed once every request already in progress has returned.
// A send already admitted when it is called may still proceed; that request
// is in progress, so it is still waited for. A request that got a 429 returns only after the function
// OnRateLimited set has returned, so a caller that waits for the channel
// waits for every rate limit being recorded. It may be called more than once.
func (p *SpotifyProvider) StopRequests() <-chan struct{} {
	w := &p.calls
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stopped {
		w.stopped = true
		w.idle = make(chan struct{})
		if w.n == 0 {
			close(w.idle)
		}
	}
	return w.idle
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
// what the answer says about the sign-in or the rate limit. A 429 closes the
// gate and fails the request with a *catalog.RateLimitError; any answer other
// than a 401 or a 429 is returned as it came.
//
// A 401 is answered by renewing the token, once per sign-in for this
// request, and sending a read again; a 401 the renewed token gets too is
// playlist.ErrNeedsAuth. A write is never sent again after a 401: so that it
// still learns what its 401 meant, the renewed token is tried on one
// read-only request.
func (p *SpotifyProvider) request(ctx context.Context, c *apiCall) (*http.Response, error) {
	if !p.calls.begin() {
		return nil, errQuitting
	}
	defer p.calls.end()
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
		// Quitting may also have begun while the token was awaited.
		if err := p.admit(ctx); err != nil { // the gate may have closed while the token was awaited
			return nil, err
		}
		if beforeSend != nil {
			beforeSend(c.path)
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
			return nil, p.tooManyRequests(resp)
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
	// The probe is a Web API request like any other: not sent while the gate
	// is closed, and its 429 closes the gate.
	if err := p.admit(ctx); err != nil {
		return rejectedWrite(c.method, err)
	}
	if beforeSend != nil {
		beforeSend("/v1/me")
	}
	resp, err := sendWebAPI(ctx, http.MethodGet, "/v1/me", nil, nil, "", access)
	if err != nil {
		return rejectedWrite(c.method, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return rejectedWrite(c.method, p.tooManyRequests(resp))
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
