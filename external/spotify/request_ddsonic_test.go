package spotify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

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

// ddsonic: a 429 ends the request, whatever came before it, read or write:
// it is not sent again, and the request fails with a RateLimitError. (Until
// the gate fix, a 429 was waited out and the request sent again, up to eight
// times; TestRequestKeepsRenewalBudgetAcross429 pinned that.)
func TestA429EndsTheRequest(t *testing.T) {
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
	}{
		{"read: 429", get, []int{rateLimited, ok}, 1, 0},
		{"read: 401, renewal, 429", get, []int{unauth, rateLimited, ok}, 2, 1},
		{"write: 429", post, []int{rateLimited, ok}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storeCreds(t)
			h := fakeSpotifyHTTP(t, tokenOK, statuses(tt.api...))
			s := goodSession()
			p := New(s, "client", 320)

			err := tt.call(p, context.Background())
			if _, isRate := errors.AsType[*catalog.RateLimitError](err); !isRate || errors.Is(err, playlist.ErrNeedsAuth) {
				t.Errorf("the request = %v, want a RateLimitError", err)
			}
			if h.apiCalls.Load() != tt.wantRequests || h.tokenCalls.Load() != tt.wantRenewals {
				t.Errorf("requests %d, renewals %d; want %d and %d",
					h.apiCalls.Load(), h.tokenCalls.Load(), tt.wantRequests, tt.wantRenewals)
			}
			if s.signInRefused() {
				t.Error("a rate limit refused the sign-in")
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

// ddsonic: a request that is always rate limited asks once, and the next
// request, background or foreground, does not ask at all while the gate is
// closed.
func TestRequestLimits(t *testing.T) {
	storeCreds(t)
	h := fakeSpotifyHTTP(t, tokenOK, statuses(http.StatusTooManyRequests))
	p := New(goodSession(), "client", 320)

	if err := get(p, context.Background()); err == nil || h.apiCalls.Load() != 1 {
		t.Errorf("a read that is always rate limited = %v after %d requests; want 1", err, h.apiCalls.Load())
	}
	if _, err := p.AlbumTrackRecordsOnce(context.Background(), "album"); err == nil || h.apiCalls.Load() != 1 {
		t.Errorf("the next read = %v after %d requests in all; want none sent", err, h.apiCalls.Load())
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
// that is not its renewal), a 401 (its one renewal in this sign-in), the
// user signing in again just before the next send, a 401 on the new sign-in
// (its one renewal there), and a 401 once more, which refuses it. What it
// spent was never reset. (Adapted from the reviewer's round-9 probe; the
// sign-in used to land during a rate-limit wait, which no longer exists.)
func TestMixedExpiry401AndSignIn(t *testing.T) {
	storeCreds(t)
	stubSignIn(t, nil)
	h := fakeSpotifyHTTP(t, tokenOK, statuses(401, 401, 401, 401))
	s := expiredSession()
	p := New(s, "client", 320)
	t.Cleanup(p.Close)
	sends := 0
	orig := beforeSend
	beforeSend = func(string) {
		if sends++; sends == 2 {
			s.refuseSignIn(s.generation())
			if err := p.Authenticate(); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { beforeSend = orig })

	c := &apiCall{method: "GET", path: "/v1/me"}
	_, err := p.request(context.Background(), c)
	if !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("the request = %v, want the refusal of the second sign-in's renewed token", err)
	}
	// Token requests: the refresh on expiry, the first sign-in's renewal,
	// the second sign-in's renewal. The second send still carries the first
	// sign-in's renewed token (got before the user signed in again), so its
	// 401 is about the old sign-in and takes one more round than when the
	// sign-in landed in a wait.
	if h.apiCalls.Load() != 4 || h.tokenCalls.Load() != 3 || c.authRound != 3 {
		t.Errorf("requests %d, token requests %d, auth rounds %d; want 4, 3 and 3",
			h.apiCalls.Load(), h.tokenCalls.Load(), c.authRound)
	}
}
