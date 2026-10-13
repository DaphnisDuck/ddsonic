// ddsonic: keep Spotify outages from reading as "sign-in required".
//
// Upstream ensureSession reported every silent sign-in failure as
// playlist.ErrNeedsAuth, so an outage (login5 answering 503 "no healthy
// upstream") would prompt a pointless sign-in; silentSessionError keeps
// ErrNeedsAuth for failures that signing in again fixes. Upstream also keeps
// a session whose Web API token refresh failed for a passing reason, which
// then fails every Web API call until restart; here the session's token
// coordinator refreshes it again when it is next asked for a token.

package spotify

import (
	"errors"
	"fmt"

	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/login5"
	pb "github.com/devgianlu/go-librespot/proto/spotify"
	login5pb "github.com/devgianlu/go-librespot/proto/spotify/login5/v3"

	"github.com/bjarneo/cliamp/playlist"
)

// errNoStoredCreds means there is no saved Spotify sign-in.
var errNoStoredCreds = errors.New("no stored credentials")

// silentSessionError maps a NewSessionSilent failure to what the caller
// should do: ErrNeedsAuth when signing in again would fix it, otherwise the
// cause itself (network trouble, a Spotify outage), to retry later.
func silentSessionError(err error) error {
	if needsSignIn(err) {
		return playlist.ErrNeedsAuth
	}
	return fmt.Errorf("spotify: sign-in unavailable: %w", err)
}

// needsSignIn reports whether err means the stored sign-in is missing or no
// longer accepted. Spotify's refusals also include throttling and outages
// (TIMEOUT, TRY_AGAIN_LATER, TryAnotherAP, …), which signing in again does
// not fix.
func needsSignIn(err error) bool {
	if errors.Is(err, playlist.ErrNeedsAuth) || errors.Is(err, errNoStoredCreds) {
		return true
	}
	var loginErr *login5.LoginError
	if errors.As(err, &loginErr) {
		switch loginErr.Code {
		case login5pb.LoginError_INVALID_CREDENTIALS, login5pb.LoginError_UNKNOWN_IDENTIFIER:
			return true
		}
		return false
	}
	var apErr *ap.AccesspointLoginError
	if errors.As(err, &apErr) && apErr.Message != nil {
		switch apErr.Message.GetErrorCode() {
		case pb.ErrorCode_BadCredentials, pb.ErrorCode_CouldNotValidateCredentials,
			pb.ErrorCode_ExtraVerificationRequired:
			return true
		}
	}
	return false
}
