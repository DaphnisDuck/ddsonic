package spotify

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// A sign-in that replaces the session in place moves the provider's count of
// sign-ins before it stores the session's new view. A read that begins in
// between sees the new count and still the old sign-in, and gets the old
// account's answer. That answer, arriving after the sign-in has cleared the
// caches, commits nothing (RC4-S2, the publication interval).
func TestIdentityReadBetweenCountAndPublicationCommitsNothing(t *testing.T) {
	old := tokenSession(&oauth2.Token{AccessToken: "old token"})
	stubSignIn(t, func(context.Context) (*Session, error) {
		// The old sign-in is healthy again by the time the new one is
		// published, so a read in the interval can still use its token.
		old.mu.Lock()
		old.playbackRefused = false
		old.storeViewLocked()
		old.mu.Unlock()
		return goodSession(), nil
	})
	inGap, gapGate := make(chan struct{}), make(chan struct{})
	leaveGap := sync.OnceFunc(func() { close(gapGate) })
	var gapOnce sync.Once
	publishHook = func(point string) {
		if point == "counted" {
			gapOnce.Do(func() { close(inGap); <-gapGate })
		}
	}
	t.Cleanup(func() { publishHook = nil })
	t.Cleanup(leaveGap)

	entered, readGate := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(readGate) })
	var readOnce sync.Once
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"id":"new-account"}`
		if req.URL.Path == "/v1/me" && req.Header.Get("Authorization") == "Bearer old token" {
			readOnce.Do(func() { close(entered); <-readGate })
			body = `{"id":"old-account"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	t.Cleanup(release)

	p := New(old, "client", 320)
	old.refuseSignIn(old.generation()) // so that the user signs in again
	signedIn := make(chan error, 1)
	go func() { signedIn <- p.Authenticate() }()
	select {
	case <-inGap:
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in never reached its publication")
	}

	read := make(chan string, 1)
	go func() { read <- p.currentUserID(t.Context()) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the read never asked with the old token")
	}
	leaveGap()
	select {
	case err := <-signedIn:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in did not finish")
	}
	release()
	select {
	case <-read:
	case <-time.After(10 * time.Second):
		t.Fatal("the read did not finish")
	}
	if left := accountCachesLeft(p); len(left) != 0 {
		t.Errorf("a read begun before the publication committed the old account's %v", left)
	}
}
