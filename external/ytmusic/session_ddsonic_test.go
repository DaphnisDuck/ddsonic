package ytmusic

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// TestSessionTokenSourceOutlivesSignIn: the sign-in's context ends with the
// sign-in; a refresh after that must still work. (Upstream 85a0f120.)
func TestSessionTokenSourceOutlivesSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()
	conf := &oauth2.Config{ClientID: "id", Endpoint: oauth2.Endpoint{TokenURL: srv.URL}}

	ctx, cancel := context.WithCancel(context.Background())
	ts := sessionTokenSource(ctx, conf, &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: time.Now().Add(-time.Minute)})
	cancel() // the sign-in ended

	tok, err := ts.Token()
	if err != nil {
		t.Fatalf("refresh after the sign-in ended: %v", err)
	}
	if tok.AccessToken != "fresh" {
		t.Errorf("access token = %q, want fresh", tok.AccessToken)
	}
}

// The page a browser shows after sign-in is titled ddsonic, not cliamp.
func TestOAuthCallbackPageNamesDdsonic(t *testing.T) {
	rec := httptest.NewRecorder()
	oauthCallback("s", make(chan string, 1))(rec, httptest.NewRequest(http.MethodGet, "/callback?code=c&state=s", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<title>ddsonic</title>") || strings.Contains(body, "cliamp") {
		t.Errorf("callback page does not name ddsonic alone:\n%s", body)
	}
}
