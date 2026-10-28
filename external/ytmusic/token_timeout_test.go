// ddsonic: a stalled token endpoint must not hold a session start forever.

package ytmusic

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/option"

	"github.com/bjarneo/cliamp/playlist"
)

// stallTransport accepts a request and never answers it.
type stallTransport struct{}

func (stallTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestSilentRefreshIsBounded(t *testing.T) {
	// The stall sits in the default transport, which a client without a
	// transport of its own (http.DefaultClient included) goes through.
	oldClient, oldTransport := tokenHTTPClient, http.DefaultTransport
	t.Cleanup(func() { tokenHTTPClient, http.DefaultTransport = oldClient, oldTransport })
	http.DefaultTransport = stallTransport{}
	creds := &storedCreds{RefreshToken: "rt"}
	start := func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() {
			_, err := newSessionFromStored(ctx, "id", "secret", creds)
			done <- err
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("session start still blocked on the token endpoint")
			return nil
		}
	}

	// Cancelling the caller's context ends the refresh.
	tokenHTTPClient = &http.Client{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := start(ctx); err == nil {
		t.Error("cancelled refresh succeeded")
	}
	// Without a deadline of its own, the client's timeout ends it.
	tokenHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	if err := start(context.Background()); err == nil {
		t.Error("timed-out refresh succeeded")
	}
}

// tokenServer answers token requests (with an access token good for an
// hour, or with refused), counting them. It honours cancellation, as a real
// transport does.
type tokenServer struct {
	requests int
	refuse   bool
}

func (s *tokenServer) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	s.requests++
	status, body := 200, `{"access_token":"a","token_type":"Bearer","expires_in":3600}`
	if s.refuse {
		status, body = 400, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func useTokenServer(t *testing.T, rt http.RoundTripper) {
	t.Helper()
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	old := tokenHTTPClient
	t.Cleanup(func() { tokenHTTPClient = old })
	tokenHTTPClient = &http.Client{Transport: rt}
	if err := saveCreds(&storedCreds{RefreshToken: "rt"}); err != nil {
		t.Fatal(err)
	}
}

// A sync's reads share the access token: one sign-in for all of them, and
// a new one only when the stored sign-in changes.
func TestOAuthCatalogReusesItsToken(t *testing.T) {
	srv := &tokenServer{}
	useTokenServer(t, srv)
	c := NewOAuthCatalog("id", "secret")
	for range 3 {
		ts, _, err := c.tokenSource(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ts.Token(); err != nil {
			t.Fatal(err)
		}
	}
	if srv.requests != 1 {
		t.Errorf("three reads signed in %d times, want once", srv.requests)
	}
	if err := saveCreds(&storedCreds{RefreshToken: "rt2"}); err != nil {
		t.Fatal(err)
	}
	ts, _, err := c.tokenSource(context.Background())
	if err == nil {
		_, err = ts.Token()
	}
	if err != nil || srv.requests != 2 {
		t.Errorf("after a new sign-in: %d sign-ins, %v; want a new one", srv.requests, err)
	}
}

// A read's refresh is cancelled with the read: nothing outlives its caller.
func TestOAuthCatalogRefreshFollowsTheRead(t *testing.T) {
	useTokenServer(t, stallTransport{})
	c := NewOAuthCatalog("id", "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ts, _, err := c.tokenSource(ctx)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ts.Token()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled refresh succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh still blocked after its read was cancelled")
	}
}

// A grant Google refuses at a read's refresh (expired, revoked) asks for
// signing in again, all the way out of the read, so Mixed falls back to
// cookies.
func TestOAuthCatalogRefusedGrantNeedsAuth(t *testing.T) {
	srv := &tokenServer{}
	useTokenServer(t, srv)
	_, f := newOAuthFixture(t)
	if err := saveCreds(&storedCreds{RefreshToken: "rt"}); err != nil { // newOAuthFixture moved the config dir
		t.Fatal(err)
	}
	api := httptest.NewServer(f)
	t.Cleanup(api.Close)
	c := NewOAuthCatalog("id", "secret")
	c.apiOptions = []option.ClientOption{option.WithEndpoint(api.URL + "/")}
	if _, err := c.LikedTrackRecords(context.Background()); err != nil {
		t.Fatalf("signed in: %v", err)
	}
	c.mu.Lock()
	c.token.Expiry = time.Now().Add(-time.Hour) // the access token runs out
	c.mu.Unlock()
	srv.refuse = true
	if _, err := c.LikedTrackRecords(context.Background()); !errors.Is(err, playlist.ErrNeedsAuth) {
		t.Errorf("refused grant at a later read = %v, want ErrNeedsAuth", err)
	}
}

// A read still holding the previous sign-in's token source cannot write
// that sign-in back over a newer one.
func TestOAuthCatalogStaleReadKeepsTheNewSignIn(t *testing.T) {
	srv := &tokenServer{}
	useTokenServer(t, srv)
	c := NewOAuthCatalog("id", "secret")
	old, _, err := c.tokenSource(context.Background()) // a read made from "rt"
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCreds(&storedCreds{RefreshToken: "new-account"}); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := c.tokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Token(); err != nil { // the old read goes on
		t.Fatal(err)
	}
	creds, err := loadCreds()
	if err != nil || creds.RefreshToken != "new-account" {
		t.Errorf("stored sign-in = %+v, %v; want the new one kept", creds, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokenFor != "new-account" || c.token.RefreshToken != "new-account" {
		t.Errorf("catalog token is for %q (%q), want the new sign-in: the old read must not share its token",
			c.tokenFor, c.token.RefreshToken)
	}
}
