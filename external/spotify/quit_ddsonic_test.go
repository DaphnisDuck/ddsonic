//go:build !windows

// ddsonic: quitting stops the Web API requests (StopRequests): nothing is
// sent from then on, and the requests in progress are waited for, until
// the rate limit each got has been handed on to be recorded.

package spotify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

func settled(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// With nothing in progress, quitting has nothing to wait for; asked again,
// it says the same.
func TestStopRequestsWithNoneInProgress(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, g := newGateAPI(t, &now)
	first, again := p.StopRequests(), p.StopRequests()
	if !settled(first) || !settled(again) {
		t.Fatal("nothing in progress, yet quitting waits")
	}
	if err := get(p, context.Background()); !errors.Is(err, errQuitting) || g.calls.Load() != 0 {
		t.Errorf("a request after quitting began = %v after %d sent; want refused, none sent", err, g.calls.Load())
	}
}

// A request whose 429 is being read when quitting begins is waited for
// until its block, the quota read from the body included, has been handed
// on; a request made meanwhile is not sent.
func TestQuittingWaitsForARequestsRateLimit(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p, _ := newGateAPI(t, &now)
	reading, release := make(chan struct{}), make(chan struct{})
	orig := http.DefaultTransport
	var sent atomic.Int32
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent.Add(1)
		return &http.Response{StatusCode: 429, Status: "429", Header: http.Header{"Retry-After": {"120"}},
			Body: &slowBody{started: reading, release: release, body: strings.NewReader(quotaBody)}, Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	var idle <-chan struct{}
	var mu sync.Mutex
	var recordedWhileWaited []bool
	p.OnRateLimited(func(until time.Time, quota bool) {
		mu.Lock()
		if idle != nil {
			recordedWhileWaited = append(recordedWhileWaited, !settled(idle))
		}
		mu.Unlock()
	})

	first := make(chan error, 1)
	go func() { first <- get(p, context.Background()) }() // the user's request
	<-reading                                             // its header's block is handed on; the body is being read
	mu.Lock()
	idle = p.StopRequests()
	mu.Unlock()
	if settled(idle) {
		t.Fatal("quitting does not wait for the request in progress")
	}
	if err := get(p, context.Background()); !errors.Is(err, errQuitting) || sent.Load() != 1 {
		t.Errorf("a request after quitting began = %v after %d sent; want refused, none more sent", err, sent.Load())
	}
	close(release)
	if rl, ok := errors.AsType[*catalog.RateLimitError](<-first); !ok || !rl.Quota {
		t.Errorf("the request in progress = %v, want its quota block", rl)
	}
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("the request ended, yet quitting still waits")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recordedWhileWaited) != 1 || !recordedWhileWaited[0] {
		t.Errorf("quota handed on while waited for: %v; want once, before the wait ended", recordedWhileWaited)
	}
}

// A request that began before quitting but was waiting for its token is
// not sent once quitting has begun.
func TestARequestNotYetSentAtQuitIsNotSent(t *testing.T) {
	storeCreds(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := fakeSpotifyHTTP(t, func(n int) (int, string) {
		once.Do(func() { close(entered); <-release })
		return tokenOK(n)
	}, apiOK)
	p := New(expiredSession(), "client", 320)
	t.Cleanup(p.Close)

	done := make(chan error, 1)
	go func() { done <- get(p, context.Background()) }()
	<-entered // past its first checks, waiting for its token
	idle := p.StopRequests()
	if settled(idle) {
		t.Fatal("quitting does not wait for the request in progress")
	}
	close(release)
	if err := <-done; !errors.Is(err, errQuitting) || h.apiCalls.Load() != 0 {
		t.Errorf("= %v after %d sent, want refused at quitting, none sent", err, h.apiCalls.Load())
	}
	if !settled(idle) {
		t.Error("the request returned, yet quitting still waits")
	}
}
