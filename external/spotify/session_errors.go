// ddsonic: keep Spotify outages from reading as "sign-in required".
//
// Upstream ensureSession reported every silent sign-in failure as
// playlist.ErrNeedsAuth, so an outage (login5 answering 503 "no healthy
// upstream") would prompt a pointless sign-in; silentSessionError keeps
// ErrNeedsAuth for failures that signing in again fixes. Upstream also keeps
// a session whose Web API token refresh failed for a passing reason, which
// then fails every Web API call until restart; ensureWebAPI restores the
// token for the catalog sync.

package spotify

import (
	"context"
	"errors"
	"fmt"
	"sync"

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

// restoreMu serializes token restores: Spotify rotates refresh tokens, so
// two concurrent refreshes with the same token could invalidate each other.
var restoreMu sync.Mutex

// ensureWebAPI is ensureSession for Web API callers that run unattended
// (the catalog sync and album fill): when the session has no Web API token
// because its refresh failed for a passing reason, it refreshes it again
// from the stored refresh token instead of failing until restart.
func (p *SpotifyProvider) ensureWebAPI() error {
	if err := p.ensureSession(); err != nil {
		return err
	}
	p.mu.Lock()
	sess := p.session
	p.mu.Unlock()
	if sess == nil {
		return playlist.ErrNeedsAuth
	}
	return sess.restoreWebAPIToken()
}

func (s *Session) restoreWebAPIToken() error {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if s.authDead.Load() { // Spotify refused this sign-in (session_auth_ddsonic.go)
		return errSignInRefused
	}
	s.mu.RLock()
	ok := s.tokenSource != nil
	s.mu.RUnlock()
	if ok {
		return nil
	}
	creds, err := loadCreds()
	if err != nil || creds.RefreshToken == "" {
		return playlist.ErrNeedsAuth
	}
	// A refresh during the session's life: no setup deadline applies, and
	// the bounded client ends a stalled request.
	token, err := silentTokenRefresh(context.Background(), s.clientID, creds.RefreshToken)
	if isInvalidGrant(err) {
		return playlist.ErrNeedsAuth
	}
	if err != nil {
		return fmt.Errorf("spotify: web api token unavailable: %w", err)
	}
	stored := *creds
	if token.RefreshToken != "" {
		stored.RefreshToken = token.RefreshToken
		if err := saveCreds(&stored); err != nil {
			return fmt.Errorf("spotify: save rotated refresh token: %w", err)
		}
	}
	s.mu.Lock()
	if s.tokenSource == nil {
		s.tokenSource = webAPITokenSource(s.clientID, token, stored)
	}
	s.mu.Unlock()
	return nil
}
