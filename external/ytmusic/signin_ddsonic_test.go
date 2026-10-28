package ytmusic

import (
	"net/url"
	"testing"
)

// The forced sign-in's URL shows Google's account chooser and consent, so
// another account can be picked and a refresh token is issued again.
func TestForcedSignInURL(t *testing.T) {
	u, err := url.Parse(googleOAuthConfig("id", "secret").AuthCodeURL("", forcedSignIn))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("prompt"); got != "select_account consent" {
		t.Errorf("prompt = %q", got)
	}
}
