package spotify

// ddsonic: the rest of upstream deb2f447, "ensureSession stops after 30 s".

import (
	"context"
	"fmt"
	"time"
)

// sessionSetupTimeout bounds the silent session setup in ensureSession.
// go-librespot sets a deadline on its connection to Spotify's access point
// only when its context has one, so without a bound an access point that
// accepts the connection and then says nothing is waited on forever, with
// sessionMu held: every Spotify call queues behind it. A variable so a test
// can shorten it.
var sessionSetupTimeout = 30 * time.Second

// newSessionSilent creates the session for ensureSession. A variable so a
// test can stand in for Spotify.
var newSessionSilent = NewSessionSilent

// setupExpired returns the setup's error when a step failed because the
// setup's time ran out (or it was cancelled), and nil otherwise. Such a
// failure is an outage, not a rejected sign-in: the caller gives the setup up
// instead of falling back to a session without a Web API token, whose
// message tells the user to sign in again.
func setupExpired(ctx context.Context, stepErr error) error {
	if stepErr == nil || ctx.Err() == nil {
		return nil
	}
	return fmt.Errorf("spotify: session setup ran out of time refreshing the web api token: %w", ctx.Err())
}
