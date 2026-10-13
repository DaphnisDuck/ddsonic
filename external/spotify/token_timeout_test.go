// ddsonic: a stalled token endpoint must not hold a session start forever.

package spotify

import (
	"context"
	"net/http"
	"testing"
	"time"
)

type stallTransport struct{}

func (stallTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestSilentTokenRefreshIsBounded(t *testing.T) {
	// The stall sits in the default transport, which a client without a
	// transport of its own (http.DefaultClient included) goes through.
	oldClient, oldTransport := tokenHTTPClient, http.DefaultTransport
	t.Cleanup(func() { tokenHTTPClient, http.DefaultTransport = oldClient, oldTransport })
	http.DefaultTransport = stallTransport{}
	tokenHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		_, err := silentTokenRefresh(context.Background(), "id", "rt") // ddsonic: takes the caller's context
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("timed-out refresh succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh still blocked on the token endpoint")
	}
}
