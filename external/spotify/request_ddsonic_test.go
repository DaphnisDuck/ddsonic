package spotify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// stubRateWait makes rate-limit waits return at once, counts them, and runs
// during, if set, inside each.
func stubRateWait(t *testing.T) (waits *atomic.Int32, during *func(n int)) {
	t.Helper()
	waits, during = new(atomic.Int32), new(func(int))
	orig := rateWait
	rateWait = func(ctx context.Context, _ time.Duration) error {
		n := int(waits.Add(1))
		if *during != nil {
			(*during)(n)
		}
		return ctx.Err()
	}
	t.Cleanup(func() { rateWait = orig })
	return waits, during
}

func get(p *SpotifyProvider, ctx context.Context) error {
	resp, err := p.webAPI(ctx, "GET", "/v1/me", nil)
	if resp != nil {
		resp.Body.Close()
	}
	return err
}

func post(p *SpotifyProvider, ctx context.Context) error {
	resp, err := p.webAPIWithBody(ctx, "POST", "/v1/me/playlists", nil, strings.NewReader(`{}`), "application/json", http.StatusOK)
	if resp != nil {
		resp.Body.Close()
	}
	return err
}

// statuses answers API request n with seq[n-1], and the last one after that.
func statuses(seq ...int) func(int, *http.Request) (int, http.Header) {
	return func(n int, _ *http.Request) (int, http.Header) {
		status := seq[min(n, len(seq))-1]
		if status == http.StatusTooManyRequests {
			return status, http.Header{"Retry-After": {"1"}}
		}
		return status, nil
	}
}

// One logical request renews its token after a 401 once per sign-in,
// however many rate-limit waits lie in between: what it has spent lives in
// the request. (The first case is the reviewer's round-8 sequence.)
func TestRequestKeepsRenewalBudgetAcross429(t *testing.T) {
	const (
		ok          = http.StatusOK
		unauth      = http.StatusUnauthorized
		rateLimited = http.StatusTooManyRequests
	)
	tests := []struct {
		name                       string
		call                       func(*SpotifyProvider, context.Context) error
		api                        []int
		wantRequests, wantRenewals int32
		wantWaits                  int32
		wantNeedsAuth              bool
		wantErr                    bool
	}{
		{"read: 401, renewal, 429, 401", get, []int{unauth, rateLimited, unauth, ok}, 3, 1, 1, true, true},
		{"read: 401 and 429 alternating", get, []int{rateLimited, unauth, rateLimited, unauth, ok}, 4, 1, 2, true, true},
		{"read: 429, 401, renewal, 429, then through", get, []int{rateLimited, unauth, rateLimited, ok}, 4, 1, 2, false, false},
		{"read: only rate limits, then through", get, []int{rateLimited, rateLimited, rateLimited, ok}, 4, 0, 3, false, false},
		{"write: 429 then sent again", post, []int{rateLimited, ok}, 2, 0, 1, false, false},
		{"write: 429, then a 401 ends it, with one check", post, []int{rateLimited, unauth, ok}, 3, 1, 1, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeCreds(t)
			waits, _ := stubRateWait(t)
			h := fakeSpotifyHTTP(t, tokenOK, statuses(tt.api...))
			s := goodSession()
			p := New(s, "client", 320)

			err := tt.call(p, context.Background())
			if (err != nil) != tt.wantErr || errors.Is(err, playlist.ErrNeedsAuth) != tt.wantNeedsAuth {
				t.Errorf("the request = %v, want an error %t, a sign-in request %t", err, tt.wantErr, tt.wantNeedsAuth)
			}
			if h.apiCalls.Load() != tt.wantRequests || h.tokenCalls.Load() != tt.wantRenewals || waits.Load() != tt.wantWaits {
				t.Errorf("requests %d, renewals %d, waits %d; want %d, %d and %d",
					h.apiCalls.Load(), h.tokenCalls.Load(), waits.Load(), tt.wantRequests, tt.wantRenewals, tt.wantWaits)
			}
			if s.signInRefused() != tt.wantNeedsAuth {
				t.Errorf("the session is refused: %t, want %t", s.signInRefused(), tt.wantNeedsAuth)
			}
			if tt.name == "write: 429, then a 401 ends it, with one check" && !provider.NotApplied(err) {
				t.Errorf("the write that got a 401 = %v, want it reported as not applied", err)
			}
		})
	}
}

// What a request has spent is its own: the next request starts afresh.
func TestIndependentRequestsHaveIndependentBudgets(t *testing.T) {
	storeCreds(t)
	const unauth, ok = http.StatusUnauthorized, http.StatusOK
	h := fakeSpotifyHTTP(t, tokenOK, statuses(unauth, ok, unauth, ok))
	p := New(goodSession(), "client", 320)
	for i := range 2 {
		if err := get(p, context.Background()); err != nil {
			t.Fatalf("read #%d = %v", i+1, err)
		}
	}
	if h.tokenCalls.Load() != 2 || h.apiCalls.Load() != 4 {
		t.Errorf("renewals %d, requests %d; want each read to renew once: 2 and 4", h.tokenCalls.Load(), h.apiCalls.Load())
	}
}

// The limits are checked before the request goes round again: the eighth
// rate limit ends it without a wait, and a waiting request never asks more
// than eight times. A request that does not wait asks once.
func TestRequestLimits(t *testing.T) {
	storeCreds(t)
	waits, _ := stubRateWait(t)
	h := fakeSpotifyHTTP(t, tokenOK, statuses(http.StatusTooManyRequests))
	p := New(goodSession(), "client", 320)

	err := get(p, context.Background())
	if err == nil || h.apiCalls.Load() != maxRateRetries || waits.Load() != maxRateRetries-1 {
		t.Errorf("a read that is always rate limited = %v after %d requests and %d waits; want %d and %d",
			err, h.apiCalls.Load(), waits.Load(), maxRateRetries, maxRateRetries-1)
	}
	before := h.apiCalls.Load()
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "album"); err == nil || h.apiCalls.Load() != before+1 || waits.Load() != maxRateRetries-1 {
		t.Errorf("a read that does not wait = %v after %d requests, %d waits in all; want one request and no wait", err, h.apiCalls.Load()-before, waits.Load())
	}
}

// A wait ends with its caller, and a sign-in accepted during a wait ends a
// write (it was not asked for under the new one) and lets a read go on.
func TestWhatHappensDuringARateWait(t *testing.T) {
	t.Run("the caller cancels", func(t *testing.T) {
		storeCreds(t)
		_, during := stubRateWait(t)
		h := fakeSpotifyHTTP(t, tokenOK, statuses(http.StatusTooManyRequests, http.StatusOK))
		ctx, cancel := context.WithCancel(context.Background())
		*during = func(int) { cancel() }
		if err := get(New(goodSession(), "client", 320), ctx); !errors.Is(err, context.Canceled) || h.apiCalls.Load() != 1 {
			t.Errorf("the read = %v after %d requests, want its cancellation after one", err, h.apiCalls.Load())
		}
	})
	for _, kind := range []string{"read", "write"} {
		t.Run("a sign-in during the wait: "+kind, func(t *testing.T) {
			storeCreds(t)
			stubSignIn(t, nil)
			_, during := stubRateWait(t)
			h := fakeSpotifyHTTP(t, tokenOK, statuses(http.StatusTooManyRequests, http.StatusOK))
			s := goodSession()
			p := New(s, "client", 320)
			*during = func(int) {
				s.refuseSignIn(s.generation())
				if err := p.Authenticate(); err != nil {
					t.Error(err)
				}
			}
			call := map[string]func(*SpotifyProvider, context.Context) error{"read": get, "write": post}[kind]
			err := call(p, context.Background())
			if kind == "read" && (err != nil || h.apiCalls.Load() != 2) {
				t.Errorf("the read = %v after %d requests, want it through on the new sign-in", err, h.apiCalls.Load())
			}
			if kind == "write" && (!errors.Is(err, errSignInChanged) || h.apiCalls.Load() != 1) {
				t.Errorf("the write = %v after %d requests, want errSignInChanged and no second send", err, h.apiCalls.Load())
			}
		})
	}
}

// A token that expires is refreshed whenever it is asked for, as it always
// was, and that is not the request's renewal: with the token expiring during
// every wait a read refreshes before each send, renews after no 401, and
// goes through.
func TestExpiryDuringEveryRateWait(t *testing.T) {
	storeCreds(t)
	_, during := stubRateWait(t)
	h := fakeSpotifyHTTP(t, tokenOK, statuses(429, 429, 429, 429, 429, 429, 429, http.StatusOK))
	s := goodSession()
	tokens := s.viewNow().tokens
	clock := time.Now()
	tokens.now = func() time.Time { return clock }
	*during = func(int) { clock = clock.Add(2 * time.Hour) } // the token runs out in each wait
	p := New(s, "client", 320)

	c := &apiCall{method: "GET", path: "/v1/me", waitRate: true}
	resp, err := p.request(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if h.apiCalls.Load() != 8 || h.tokenCalls.Load() != 7 || c.authRound != 0 || c.renewedIn != nil || c.rateTry != 7 {
		t.Errorf("requests %d, refreshes %d, auth rounds %d, renewed after a 401 %t, waits %d; want 8, 7, 0, false and 7",
			h.apiCalls.Load(), h.tokenCalls.Load(), c.authRound, c.renewedIn != nil, c.rateTry)
	}
	if revOf(tokens) != 8 {
		t.Errorf("the token is at revision %d, want 8: the seed and seven refreshes", revOf(tokens))
	}
}

// Two production callers that need a newer token at once share one refresh,
// in both arrival orders: one whose token has run out, one that got a 401.
// (This holds what the tests of a second refresh overtaking the first used
// to protect; that ordering can no longer be made.)
func TestProductionCallersShareOneRefresh(t *testing.T) {
	for _, order := range []string{"expiry first, the 401 joins", "the 401 first, expiry joins"} {
		for _, outcome := range []string{"success", "invalid_grant", "outage"} {
			t.Run(order+": "+outcome, func(t *testing.T) {
				path := storeCreds(t)
				held := newGate()
				h := fakeSpotifyHTTP(t, func(n int) (int, string) {
					if n == 1 {
						held.wait()
					}
					switch outcome {
					case "invalid_grant":
						return tokenRevoked(n)
					case "outage":
						return http.StatusServiceUnavailable, "no healthy upstream"
					}
					// Good for a day: it outlasts the clock that ran the old one out.
					return http.StatusOK, `{"access_token":"shared-access","token_type":"Bearer","refresh_token":"shared-refresh","expires_in":86400}`
				}, func(_ int, req *http.Request) (int, http.Header) {
					if req.Header.Get("Authorization") != "Bearer shared-access" {
						return http.StatusUnauthorized, nil // the old token
					}
					return http.StatusOK, nil
				})
				joined := hookOnce(t, &flightJoined)
				s := goodSession()
				tokens := s.viewNow().tokens
				p := New(s, "client", 320)

				// a: a read that gets a 401 on the old token and renews.
				// b: a read that finds the token run out and refreshes.
				a, b := make(chan error, 1), make(chan error, 1)
				after401 := func() { a <- get(p, context.Background()) }
				onExpiry := func() {
					tokens.mu.Lock()
					tokens.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
					tokens.mu.Unlock()
					b <- get(p, context.Background())
				}
				first, second := after401, onExpiry
				if strings.HasPrefix(order, "expiry first") {
					first, second = onExpiry, after401
				}
				go first()
				arrive(t, "the first caller's refresh", held.entered)
				go second()
				arrive(t, "the second caller to join the refresh", joined)
				inFlight := h.tokenCalls.Load()
				held.open()
				errA, errB := <-a, <-b

				if inFlight != 1 {
					t.Fatalf("%d token requests were in the air with both callers waiting, want the one they share", inFlight)
				}
				switch outcome {
				case "success":
					if errA != nil || errB != nil || revOf(tokens) != 2 || h.tokenCalls.Load() != 1 {
						t.Errorf("the callers got %v and %v; revision %d after %d token requests; want both through on revision 2 from one request",
							errA, errB, revOf(tokens), h.tokenCalls.Load())
					}
					if !strings.Contains(mustRead(t, path), "shared-refresh") {
						t.Errorf("the credential file holds %q, want the one answer's refresh token", mustRead(t, path))
					}
				case "invalid_grant":
					if !errors.Is(errA, playlist.ErrNeedsAuth) || !errors.Is(errB, playlist.ErrNeedsAuth) || !s.signInRefused() || revOf(tokens) != 1 {
						t.Errorf("the callers got %v and %v, refused %t, revision %d; want the one refusal for both", errA, errB, s.signInRefused(), revOf(tokens))
					}
				case "outage":
					if errA == nil || errB == nil || errors.Is(errA, playlist.ErrNeedsAuth) || errors.Is(errB, playlist.ErrNeedsAuth) || s.signInRefused() {
						t.Errorf("the callers got %v and %v, refused %t; want an outage that refuses nothing", errA, errB, s.signInRefused())
					}
				}
				if outcome != "success" && !strings.Contains(mustRead(t, path), `"refresh_token":"old"`) {
					t.Errorf("the credential file was changed by a refresh that produced no token: %q", mustRead(t, path))
				}
			})
		}
	}
}

// A 401 that arrives late, for a token the session has since refreshed by
// itself, is about the old token: it refuses nothing, and the read goes
// through on the token that stands. The second case lets the token run out
// by the clock, with nothing in the test reaching into the session.
func TestStale401DoesNotRefuseNewerLease(t *testing.T) {
	for _, expiry := range []string{"the coordinator's clock", "real time"} {
		t.Run("the token ran out by "+expiry, func(t *testing.T) {
			storeCreds(t)
			entered, release := make(chan struct{}), make(chan struct{})
			h := fakeSpotifyHTTP(t, func(n int) (int, string) {
				if n == 1 { // the renewal after the first 401: good for a second
					return http.StatusOK, `{"access_token":"forced-access","token_type":"Bearer","refresh_token":"forced-refresh","expires_in":11}`
				}
				return tokenAnswer("automatic-access", "automatic-refresh")
			}, func(n int, _ *http.Request) (int, http.Header) {
				switch n {
				case 1:
					return http.StatusUnauthorized, nil
				case 2: // sent with the renewed token; its 401 is held
					close(entered)
					<-release
					return http.StatusUnauthorized, nil
				}
				return http.StatusOK, nil
			})
			s := goodSession()
			tokens := s.viewNow().tokens
			p := New(s, "client", 320)

			slow := make(chan error, 1)
			go func() { slow <- get(p, context.Background()) }()
			<-entered
			if expiry == "real time" {
				time.Sleep(1300 * time.Millisecond) // 11 s less oauth2's 10 s margin
			} else {
				tokens.mu.Lock()
				tokens.now = func() time.Time { return time.Now().Add(time.Minute) }
				tokens.mu.Unlock()
			}
			if err := get(p, context.Background()); err != nil { // refreshes by itself, and works
				t.Fatal(err)
			}
			if h.tokenCalls.Load() != 2 || revOf(tokens) != 3 {
				t.Fatalf("the control did not refresh by itself: %d token requests, revision %d", h.tokenCalls.Load(), revOf(tokens))
			}
			close(release)
			err := <-slow
			if s.signInRefused() || err != nil || h.tokenCalls.Load() != 2 {
				t.Errorf("the late 401 for the old token: read = %v, refused %t, %d token requests; want it through and nothing refused or renewed",
					err, s.signInRefused(), h.tokenCalls.Load())
			}
		})
	}
}

// A request that keeps meeting a sign-in replaced under it gives up, after
// three rounds.
func TestRequestGivesUpWhenTheSignInKeepsChanging(t *testing.T) {
	c := &apiCall{method: "GET"}
	for i := range maxAuthRounds {
		if err := c.backAuth(); err != nil {
			t.Fatalf("round %d was refused: %v", i+1, err)
		}
	}
	if err := c.backAuth(); err == nil || c.authRound != maxAuthRounds {
		t.Errorf("a fourth round = %v with %d counted, want it refused before it is taken", err, c.authRound)
	}
}

// A refresh token that Spotify rotated and ddsonic accepted is the one the
// next start uses: it was written when it was accepted. (Adapted from the
// reviewer's round-9 probe.)
func TestRestartAfterRotation(t *testing.T) {
	storeCreds(t)
	stubBuild(t)
	var sent []string // the refresh token each token request carried
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := "{}"
		if strings.Contains(req.URL.Host, "accounts.spotify.com") {
			if err := req.ParseForm(); err != nil {
				return nil, err
			}
			sent = append(sent, req.Form.Get("refresh_token"))
			body = `{"access_token":"synthetic-access","refresh_token":"synthetic-rotated","token_type":"Bearer","expires_in":3600}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })

	p := New(expiredSession(), "client", 320)
	if err := get(p, context.Background()); err != nil { // refreshes, and Spotify rotates
		t.Fatal(err)
	}
	p.Close() // quit

	restarted := New(nil, "client", 320)
	t.Cleanup(restarted.Close)
	if err := restarted.ensureSession(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != "old" || sent[1] != "synthetic-rotated" {
		t.Errorf("the token requests carried %q, want the stored token and then the rotated one after the restart", sent)
	}
}

// One request through everything at once: its token has run out (a refresh
// that is not its renewal), a 401 (its one renewal in this sign-in), a rate
// limit during which the user signs in again, a 401 on the new sign-in (its
// one renewal there), and a 401 once more, which refuses it. What it spent
// was never reset. (Adapted from the reviewer's round-9 probe.)
func TestMixedExpiry401AndSignIn(t *testing.T) {
	storeCreds(t)
	stubSignIn(t, nil)
	waits, during := stubRateWait(t)
	h := fakeSpotifyHTTP(t, tokenOK, statuses(401, 429, 401, 401))
	s := expiredSession()
	p := New(s, "client", 320)
	t.Cleanup(p.Close)
	*during = func(int) {
		s.refuseSignIn(s.generation())
		if err := p.Authenticate(); err != nil {
			t.Error(err)
		}
	}

	c := &apiCall{method: "GET", path: "/v1/me", waitRate: true}
	_, err := p.request(context.Background(), c)
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("the request = %v, want the refusal of the second sign-in's renewed token", err)
	}
	// Token requests: the refresh on expiry, the first sign-in's renewal,
	// the second sign-in's renewal.
	if h.apiCalls.Load() != 4 || h.tokenCalls.Load() != 3 || waits.Load() != 1 || c.authRound != 2 || c.rateTry != 1 {
		t.Errorf("requests %d, token requests %d, waits %d, auth rounds %d, rate tries %d; want 4, 3, 1, 2 and 1",
			h.apiCalls.Load(), h.tokenCalls.Load(), waits.Load(), c.authRound, c.rateTry)
	}
}
