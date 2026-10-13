package spotify

import (
	"context"
	"errors"
	"net"
	"testing"

	"golang.org/x/oauth2"
)

// oauthSeams counts what a browser sign-in offers: the callback listener, the
// URL and the browser. Each hook may be replaced to cancel the flow there.
type oauthSeams struct {
	listens, urls, opens int
	onListen, onURL      func()
}

func stubOAuthSeams(t *testing.T) *oauthSeams {
	t.Helper()
	s := &oauthSeams{}
	origListen, origOpen, origObserver := oauthListen, openBrowser, authURLObserver.Load()
	t.Cleanup(func() {
		oauthListen, openBrowser = origListen, origOpen
		authURLObserver.Store(origObserver)
	})
	oauthListen = func(network, _ string) (net.Listener, error) {
		s.listens++
		lis, err := net.Listen(network, "127.0.0.1:0")
		if s.onListen != nil {
			s.onListen()
		}
		return lis, err
	}
	openBrowser = func(string) error { s.opens++; return nil }
	SetAuthURLObserver(func(string) {
		s.urls++
		if s.onURL != nil {
			s.onURL()
		}
	})
	return s
}

// A sign-in flow that is already cancelled offers nothing further: no
// callback port, no URL, no browser, whichever of them it had reached.
func TestCancelledFlowOffersNothing(t *testing.T) {
	flow := []oauthFlow{{name: "synthetic", clientID: "synthetic-client", scopes: []string{"user-read-private"}}}
	tests := []struct {
		name                             string
		cancelAt                         string
		wantListens, wantURLs, wantOpens int
	}{
		{"cancelled before it starts", "entry", 0, 0, 0},
		{"cancelled once the port is open", "listen", 1, 0, 0},
		{"cancelled once the URL is shown", "url", 1, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seams := stubOAuthSeams(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch tt.cancelAt {
			case "entry":
				cancel()
			case "listen":
				seams.onListen = cancel
			case "url":
				seams.onURL = cancel
			}
			_, err := performOAuth2PKCEFlows(ctx, flow)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("performOAuth2PKCEFlows() = %v, want the cancellation", err)
			}
			if seams.listens != tt.wantListens || seams.urls != tt.wantURLs || seams.opens != tt.wantOpens {
				t.Errorf("the flow listened %d, showed its URL %d and opened a browser %d times, want %d, %d and %d",
					seams.listens, seams.urls, seams.opens, tt.wantListens, tt.wantURLs, tt.wantOpens)
			}
		})
	}
}

// A stored session that fails after its caller gave up is not followed by a
// browser sign-in.
func TestCancelledBetweenStoredAndInteractive(t *testing.T) {
	storeCreds(t)
	stubBuild(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	asked := 0
	origTokens := interactiveTokens
	t.Cleanup(func() { interactiveTokens = origTokens })
	interactiveTokens = func(context.Context, []oauthFlow) ([]*oauth2.Token, error) {
		asked++
		return nil, errors.New("asked for a browser sign-in")
	}
	// The stored refresh is refused, and the caller cancels while it is.
	fakeSpotifyHTTP(t, func(n int) (int, string) { cancel(); return tokenRevoked(n) }, apiOK)

	if _, err := NewSession(ctx, "client"); !errors.Is(err, context.Canceled) {
		t.Errorf("NewSession() = %v, want the cancellation", err)
	}
	if asked != 0 {
		t.Errorf("a browser sign-in was asked for %d times after the caller had cancelled", asked)
	}
}
