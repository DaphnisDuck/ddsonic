package spotify

import (
	"context"
	"net/http"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestWebAPIRequestIsBounded: a Web API request on a stalled connection ends
// on webHTTPClient's timeout instead of waiting for its caller's context,
// which for a catalog sync lasts until quit. (Upstream deb2f447.)
func TestWebAPIRequestIsBounded(t *testing.T) {
	// The stall sits in the default transport, which http.DefaultClient and
	// a client without a transport of its own go through.
	oldClient, oldTransport := webHTTPClient, http.DefaultTransport
	t.Cleanup(func() { webHTTPClient, http.DefaultTransport = oldClient, oldTransport })
	http.DefaultTransport = stallTransport{}
	webHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}

	s := tokenSession(&oauth2.Token{AccessToken: "t"})
	done := make(chan error, 1)
	go func() {
		_, err := New(s, "client", 320).webAPI(context.Background(), http.MethodGet, "/v1/me", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a stalled request succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request still waits on the stalled connection")
	}
}
