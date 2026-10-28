package ytmusic

// ddsonic: a forced sign-in, to switch Google accounts.

import (
	"context"

	"golang.org/x/oauth2"
)

// NewSessionForced signs in through the browser even when credentials are
// stored, with Google's account chooser and consent screen, so another
// account can be picked and a new refresh token is issued. The stored
// credentials are replaced only when the sign-in succeeds.
func NewSessionForced(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	return newInteractiveSession(ctx, clientID, clientSecret, forcedSignIn)
}

// forcedSignIn asks Google for its account chooser and consent screen.
var forcedSignIn = oauth2.SetAuthURLParam("prompt", "select_account consent")
