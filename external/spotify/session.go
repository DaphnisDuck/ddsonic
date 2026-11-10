package spotify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time" // ddsonic

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/appmeta" // ddsonic
	"github.com/bjarneo/cliamp/internal/browser"
	"github.com/bjarneo/cliamp/internal/fileutil"

	librespot "github.com/devgianlu/go-librespot"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	devicespb "github.com/devgianlu/go-librespot/proto/spotify/connectstate/devices"
	"github.com/devgianlu/go-librespot/session"
	"golang.org/x/oauth2"
	spotifyoauth2 "golang.org/x/oauth2/spotify"
)

// storedCreds holds persisted Spotify credentials for re-authentication.
type storedCreds struct {
	Username     string `json:"username"`
	Data         []byte `json:"data"`
	DeviceID     string `json:"device_id"`
	RefreshToken string `json:"refresh_token,omitempty"` // OAuth2 refresh token for silent re-auth
}

const (
	callbackHost = "127.0.0.1"

	// CallbackPort is the fixed port for the OAuth2 callback server.
	// Must match the redirect URI registered in the Spotify Developer app.
	CallbackPort = 19872
)

func callbackAddress() string {
	return net.JoinHostPort(callbackHost, fmt.Sprint(CallbackPort))
}

// authURLObserver is invoked with the OAuth URL when interactive auth begins.
// Set via SetAuthURLObserver. Used by the TUI to show the URL when the
// launched browser doesn't reach the user (containers, headless envs).
var authURLObserver atomic.Pointer[func(string)]

// SetAuthURLObserver registers a callback invoked once with the OAuth URL at
// the start of an interactive sign-in. Pass nil to remove.
func SetAuthURLObserver(fn func(string)) {
	if fn == nil {
		authURLObserver.Store(nil)
		return
	}
	authURLObserver.Store(&fn)
}

func notifyAuthURL(u string) {
	// ddsonic: the URL carries the sign-in's state and PKCE challenge, so the
	// log file, which other users can read, gets none of it; the UI gets it
	// whole (RC4-S1).
	applog.Info("spotify: sign-in started")
	if p := authURLObserver.Load(); p != nil {
		(*p)(u)
	}
}

// Session manages a go-librespot session and player for Spotify integration.
//
// ddsonic: upstream kept the librespot session, the player and an
// auto-refreshing token source here, under a lock held across librespot's
// calls. They are the session's playback bundle (playback_ddsonic.go) and its
// token coordinator (token_coordinator_ddsonic.go) now, and mu is held for
// short steps only.
type Session struct {
	mu          sync.Mutex
	clientID    string // Spotify Developer app client ID
	signInState        // ddsonic: which sign-in this is, and what belongs to it (session_auth_ddsonic.go)
}

type streamContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

// RoundTrip attaches the stream lifetime to librespot's context-free chunk requests.
//
// ddsonic: once the stream is cancelled its requests fail with net.ErrClosed,
// the one error librespot's chunk reader does not retry: otherwise a closed
// stream's read went on retrying for seconds, and its Close with it. Any
// other error is returned as it is.
func (t streamContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, fmt.Errorf("spotify: stream closed: %w: %w", net.ErrClosed, err)
	}
	resp, err := t.base.RoundTrip(req.Clone(t.ctx))
	if err != nil && t.ctx.Err() != nil {
		return nil, fmt.Errorf("spotify: stream closed: %w: %w", net.ErrClosed, err)
	}
	return resp, err
}

func newSpotifyStreamHTTPClient(ctx context.Context, transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: streamContextTransport{ctx: ctx, base: transport}}
}

// NewSession creates a go-librespot session, using stored credentials if
// available, otherwise starting an interactive OAuth2 flow.
// clientID is the Spotify Developer app client ID for Web API access.
func NewSession(ctx context.Context, clientID string) (*Session, error) {
	creds, err := loadCreds()
	if err == nil && creds.Username != "" && len(creds.Data) > 0 {
		s, err := newSessionFromStored(ctx, clientID, creds, false)
		if err == nil {
			return s, nil
		}
		// Stored credentials failed (expired/revoked), fall through to interactive.
		// ddsonic: unless the caller has given up meanwhile.
		if err := oauthCancelled(ctx); err != nil {
			return nil, fmt.Errorf("spotify: %w", err)
		}
	}
	return newInteractiveSession(ctx, clientID)
}

// NewSessionSilent is like NewSession but only uses stored credentials.
// Returns an error if interactive auth is required.
func NewSessionSilent(ctx context.Context, clientID string) (*Session, error) {
	creds, err := loadCreds()
	if err != nil || creds.Username == "" || len(creds.Data) == 0 {
		return nil, errNoStoredCreds // ddsonic: a sentinel for silentSessionError
	}
	return newSessionFromStored(ctx, clientID, creds, true)
}

// newSessionFromStored creates a session from stored credentials.
// When silentOnly is true, it will not fall back to browser-based auth
// if the silent token refresh fails.
func newSessionFromStored(ctx context.Context, clientID string, creds *storedCreds, silentOnly bool) (*Session, error) {
	devID := creds.DeviceID
	if devID == "" {
		devID = generateDeviceID()
	}

	sess, err := newLibrespotSession(ctx, &session.Options{
		Log:        &librespot.NullLogger{},
		DeviceType: devicespb.DeviceType_COMPUTER,
		DeviceId:   devID,
		Credentials: session.StoredCredentials{
			Username: creds.Username,
			Data:     creds.Data,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: stored auth: %w", err)
	}

	username, data := sessionIdentity(sess) // ddsonic: a variable, for tests

	// For stored credentials, we need a fresh Web API token via OAuth2.
	// The spclient's login5 token is NOT suitable for Web API calls.
	// Try silent refresh first (no browser), fall back to interactive.
	var oauthToken *oauth2.Token
	var refreshErr error
	// ddsonic: a setup whose context has ended sends no refresh. librespot
	// can hold a builder past its deadline (the identity read above waits for
	// a lock without it); by then the build has been given up, and the stored
	// refresh token is another build's or a coordinator's to refresh.
	if err := ctx.Err(); err != nil {
		closeLibrespot(nil, sess)
		return nil, fmt.Errorf("spotify: session setup ran out of time before refreshing the web api token: %w", err)
	}
	if creds.RefreshToken != "" {
		// ddsonic: upstream deb2f447. The refresh shares the setup's deadline.
		token, err := silentTokenRefresh(ctx, clientID, creds.RefreshToken)
		if err == nil {
			oauthToken = token
		} else {
			refreshErr = err
		}
	}
	// ddsonic: a refresh cut short by the setup's deadline says nothing about
	// the sign-in. The setup failed for now; the next call tries again.
	if err := setupExpired(ctx, refreshErr); err != nil {
		closeLibrespot(nil, sess)
		return nil, err
	}
	// ddsonic: a refresh token Spotify refuses never recovers, but the
	// credential is not ours to delete: it stays until a sign-in that worked
	// replaces it. The caller remembers the refusal (errStoredSignInRefused)
	// so that Spotify is not asked again. Upstream cleared the file here.
	if isInvalidGrant(refreshErr) {
		applog.UserError("spotify: stored refresh token is no longer accepted; please sign in again")
		closeLibrespot(nil, sess)
		return nil, errStoredSignInRefused
	}
	if oauthToken == nil {
		if silentOnly {
			// Continue without a token source — already-loaded tracks still stream
			// via spclient; new Web API calls will return ErrNeedsAuth.
			applog.UserError("%s", storedTokenMessage(creds.RefreshToken)) // ddsonic: no reset advice for a passing failure (session_messages_ddsonic.go)
			// ddsonic: the credential is staged with the session's
			// coordinator, and written when the session is taken.
			s := builtSession(clientID, sess, storedCreds{
				Username:     username,
				Data:         data,
				DeviceID:     devID,
				RefreshToken: creds.RefreshToken, // preserve for next attempt
			}, nil)
			if err := initSessionPlayer(s); err != nil {
				closeLibrespot(nil, sess)
				return nil, err
			}
			return s, nil
		}
		if err := oauthCancelled(ctx); err != nil { // ddsonic: no browser for a caller that gave up
			closeLibrespot(nil, sess)
			return nil, fmt.Errorf("spotify: %w", err)
		}
		token, err := doWebAPIAuth(ctx, clientID)
		if err != nil {
			closeLibrespot(nil, sess)
			return nil, fmt.Errorf("stored session needs fresh Web API token: %w", err)
		}
		oauthToken = token
	}

	// Re-save credentials (including refresh token for next launch).
	stored := storedCreds{
		Username:     username,
		Data:         data,
		DeviceID:     devID,
		RefreshToken: oauthToken.RefreshToken,
	}

	// ddsonic: the credential is staged with the session's coordinator, and
	// written when the session is taken; upstream wrote it here, before the
	// player started.
	s := builtSession(clientID, sess, stored, oauthToken)

	if err := initSessionPlayer(s); err != nil {
		closeLibrespot(nil, sess)
		return nil, err
	}
	return s, nil
}

// oauthScopes are the Spotify Web API scopes needed for cliamp.
// See: https://developer.spotify.com/documentation/web-api/concepts/scopes
var oauthScopes = []string{
	// Playlist browsing
	"playlist-read-collaborative",
	"playlist-read-private",
	// Playlist modification (save queue, create playlists)
	"playlist-modify-public",
	"playlist-modify-private",
	// Streaming audio
	"streaming",
	// Library (liked songs, saved albums)
	"user-library-read",
	"user-library-modify",
	// User profile
	"user-read-private",
	// Playback state (current track, queue)
	"user-read-playback-state",
	"user-modify-playback-state",
	"user-read-currently-playing",
	// Recently played / top tracks
	"user-read-recently-played",
	"user-top-read",
	// Following (artists, users)
	"user-follow-read",
	"user-follow-modify",
}

// playbackOAuthScopes are requested through Spotify's keymaster client. Since
// August 2026, login5 rejects playback credentials minted by any other client.
var playbackOAuthScopes = []string{"streaming"}

// spotifyOAuthConfig returns the OAuth2 config for the given client ID.
func spotifyOAuthConfig(clientID string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    clientID,
		RedirectURL: fmt.Sprintf("http://%s/login", callbackAddress()),
		Scopes:      scopes,
		Endpoint:    spotifyoauth2.Endpoint,
	}
}

// silentTokenRefresh uses a stored refresh token to get a new access token
// without opening a browser.
//
// ddsonic: upstream deb2f447. The request ends with ctx, so a refresh made
// during session setup cannot outlast the setup's deadline; a caller with no
// deadline of its own still has the bounded client's.
func silentTokenRefresh(ctx context.Context, clientID, refreshToken string) (*oauth2.Token, error) {
	conf := spotifyOAuthConfig(clientID, oauthScopes)
	src := conf.TokenSource(tokenContextFrom(ctx), &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}

// ddsonic: token refreshes use a bounded client. oauth2's default
// (http.DefaultClient) never times out, so a stalled token endpoint held a
// session start, and every Web API call after the token expired, forever.
var tokenHTTPClient = &http.Client{Timeout: 30 * time.Second}

// webHTTPClient sends Web API and lyrics requests. http.DefaultClient never
// times out, so a stalled connection held a catalog sync until quit, and r
// was dropped while it ran (ddsonic: upstream deb2f447).
var webHTTPClient = &http.Client{Timeout: 30 * time.Second}

// tokenContextFrom carries the bounded client to oauth2's requests, which
// also end when ctx does. ddsonic
func tokenContextFrom(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, tokenHTTPClient)
}

// isInvalidGrant reports whether err is an OAuth2 invalid_grant response
// from the token endpoint, indicating the refresh token is dead and
// retrying with the same token will not succeed.
func isInvalidGrant(err error) bool {
	var rerr *oauth2.RetrieveError
	if !errors.As(err, &rerr) {
		return false
	}
	return rerr.ErrorCode == "invalid_grant"
}

// ddsonic: the callback listener and the browser launch, as variables so a
// test can stand in for them.
var (
	oauthListen = net.Listen
	openBrowser = browser.Open
)

// oauthCallbackHTML is the response sent to the browser after a successful OAuth2 callback.
const oauthCallbackHTML = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>✅ Authenticated!</h2>
<p>You can close this tab now.</p>
<script>setTimeout(function(){window.close()},1500)</script>
</div></body></html>`

type oauthFlow struct {
	name     string
	clientID string
	scopes   []string
}

type pendingOAuthFlow struct {
	oauthFlow
	config   *oauth2.Config
	verifier string
	state    string
	authURL  string
}

type oauthCallback struct {
	flow int
	code string
	err  error
}

func oauthCallbackHandler(pending []pendingOAuthFlow, callbackCh chan<- oauthCallback) http.Handler {
	byState := make(map[string]int, len(pending))
	for i, flow := range pending {
		byState[flow.state] = i
	}

	seen := make(map[string]bool, len(pending))
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		flow, ok := byState[state]
		if !ok {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		oauthErr := r.URL.Query().Get("error")
		if code == "" && oauthErr == "" {
			http.Error(w, "OAuth callback contains no code", http.StatusBadRequest)
			return
		}

		mu.Lock()
		first := !seen[state]
		seen[state] = true
		mu.Unlock()
		if first {
			result := oauthCallback{flow: flow, code: code}
			if oauthErr != "" {
				result.err = fmt.Errorf("spotify returned %s", oauthErr)
			}
			callbackCh <- result
		}

		if oauthErr != "" {
			http.Error(w, "Spotify authorization failed", http.StatusBadRequest)
			return
		}
		if flow+1 < len(pending) {
			http.Redirect(w, r, pending[flow+1].authURL, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(appmeta.CallbackPage(oauthCallbackHTML))) // ddsonic: the tab title
	})
}

// performOAuth2PKCE runs one OAuth2 PKCE flow and returns its token.
func performOAuth2PKCE(ctx context.Context, clientID string, scopes []string) (*oauth2.Token, error) {
	tokens, err := interactiveTokens(ctx, []oauthFlow{{name: "web api", clientID: clientID, scopes: scopes}}) // ddsonic: a variable, for tests
	if err != nil {
		return nil, err
	}
	return tokens[0], nil
}

// oauthCancelled is the error of a sign-in flow whose context has ended, and
// nil while it has not. ddsonic
func oauthCancelled(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("authentication cancelled: %w", err)
	}
	return nil
}

// performOAuth2PKCEFlows opens one browser journey for one or more OAuth
// authorizations. Each callback redirects the same tab to the next flow, so a
// custom Web API client can be followed by keymaster playback authorization
// without relying on the browser to foreground a second tab.
func performOAuth2PKCEFlows(ctx context.Context, flows []oauthFlow) ([]*oauth2.Token, error) {
	if len(flows) == 0 {
		return nil, fmt.Errorf("no OAuth flows configured")
	}
	// ddsonic: a flow that is already cancelled offers nothing: no callback
	// port here, no URL and no browser below. A cancellation that lands just
	// after a check is an ordinary race with the browser, as it always was.
	if err := oauthCancelled(ctx); err != nil {
		return nil, err
	}

	lis, err := oauthListen("tcp", callbackAddress()) // ddsonic: a variable, for tests
	if err != nil {
		return nil, fmt.Errorf("listen on port %d: %w", CallbackPort, err)
	}
	defer lis.Close() // always release the port

	pending := make([]pendingOAuthFlow, len(flows))
	for i, flow := range flows {
		conf := spotifyOAuthConfig(flow.clientID, flow.scopes)
		verifier := oauth2.GenerateVerifier()
		state := oauth2.GenerateVerifier()
		pending[i] = pendingOAuthFlow{
			oauthFlow: flow,
			config:    conf,
			verifier:  verifier,
			state:     state,
			authURL:   conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)),
		}
	}

	callbackCh := make(chan oauthCallback, len(flows))
	go func() {
		if err := http.Serve(lis, oauthCallbackHandler(pending, callbackCh)); err != nil && !errors.Is(err, net.ErrClosed) {
			applog.UserError("spotify: auth callback server error: %v", err)
		}
	}()

	if err := oauthCancelled(ctx); err != nil { // ddsonic
		return nil, err
	}
	notifyAuthURL(pending[0].authURL)
	if err := oauthCancelled(ctx); err != nil { // ddsonic
		return nil, err
	}
	_ = openBrowser(pending[0].authURL) // best-effort — user can open the URL manually if this fails; ddsonic: a variable, for tests

	tokens := make([]*oauth2.Token, len(pending))
	for remaining := len(pending); remaining > 0; remaining-- {
		select {
		case result := <-callbackCh:
			flow := pending[result.flow]
			if result.err != nil {
				return nil, fmt.Errorf("%s authorization: %w", flow.name, result.err)
			}
			token, err := flow.config.Exchange(ctx, result.code, oauth2.VerifierOption(flow.verifier))
			if err != nil {
				return nil, fmt.Errorf("%s token exchange: %w", flow.name, err)
			}
			tokens[result.flow] = token
		case <-ctx.Done():
			return nil, fmt.Errorf("authentication cancelled: %w", ctx.Err())
		}
	}
	return tokens, nil
}

// doWebAPIAuth performs an OAuth2 PKCE flow to get a fresh Web API access token.
// Opens a browser for user consent, returns the full token (including refresh token).
func doWebAPIAuth(ctx context.Context, clientID string) (*oauth2.Token, error) {
	token, err := performOAuth2PKCE(ctx, clientID, oauthScopes)
	if err != nil {
		return nil, err
	}
	fmt.Println("Spotify: Web API token refreshed.")
	return token, nil
}

// interactiveOAuthFlows returns the authorization sequence for a new session.
// The built-in client already is keymaster, so it needs only one flow.
func interactiveOAuthFlows(clientID string) []oauthFlow {
	if clientID == DefaultClientID {
		return []oauthFlow{{name: "web api and playback", clientID: clientID, scopes: oauthScopes}}
	}
	return []oauthFlow{
		{name: "web api", clientID: clientID, scopes: oauthScopes},
		{name: "playback", clientID: DefaultClientID, scopes: playbackOAuthScopes},
	}
}

func newInteractiveSession(ctx context.Context, clientID string) (*Session, error) {
	devID := generateDeviceID()

	tokens, err := interactiveTokens(ctx, interactiveOAuthFlows(clientID)) // ddsonic: a variable, for tests
	if err != nil {
		return nil, fmt.Errorf("spotify: %w", err)
	}
	webToken := tokens[0]
	playbackToken := tokens[len(tokens)-1]

	username, _ := playbackToken.Extra("username").(string)
	accessToken := playbackToken.AccessToken

	// Create go-librespot session using the keymaster-issued playback token.
	sess, err := newLibrespotSession(ctx, &session.Options{
		Log:        &librespot.NullLogger{},
		DeviceType: devicespb.DeviceType_COMPUTER,
		DeviceId:   devID,
		Credentials: session.SpotifyTokenCredentials{
			Username: username,
			Token:    accessToken,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("spotify: session from token: %w", err)
	}

	// Persist stored credentials + refresh token for future sessions.
	username, data := sessionIdentity(sess) // ddsonic: a variable, for tests
	stored := storedCreds{
		Username:     username,
		Data:         data,
		DeviceID:     devID,
		RefreshToken: webToken.RefreshToken,
	}

	// ddsonic: the credential is staged with the session's coordinator, and
	// written when the session is taken; upstream wrote it here, before the
	// player started.
	s := builtSession(clientID, sess, stored, webToken)
	if err := initSessionPlayer(s); err != nil {
		closeLibrespot(nil, sess)
		return nil, err
	}
	return s, nil
}

// initPlayer creates the go-librespot player. We only use NewStream() for
// decoded AudioSources — audio output is routed through cliamp's Beep pipeline,
// not go-librespot's output backend.
func (s *Session) initPlayer() error {
	// go-librespot uses this for media restriction checks but Premium
	// accounts can play all tracks regardless.
	countryCode := "US"
	p, err := librespotPlayer.NewPlayer(&librespotPlayer.Options{
		Spclient:             s.play.sess.Spclient(), // ddsonic: the session's playback bundle
		AudioKey:             s.play.sess.AudioKey(),
		Events:               s.play.sess.Events(),
		Log:                  &librespot.NullLogger{},
		CountryCode:          &countryCode,
		NormalisationEnabled: true,
		AudioBackend:         "pipe",
		AudioOutputPipe:      os.DevNull,
	})
	if err != nil {
		return fmt.Errorf("spotify: player init: %w", err)
	}
	s.play.player = p
	return nil
}

// ddsonic: NewStream and Close are in playback_ddsonic.go and
// session_auth_ddsonic.go. Upstream's held the session's lock across
// librespot's stream setup and teardown; upstream's webApiWithBody asked an
// oauth2 token source here, which the token coordinator replaces.

// ddsonic: Reconnect and ReconnectInteractive are in session_auth_ddsonic.go.
// Upstream's silent reconnect built a whole second session, with its own
// refresh of the stored refresh token; here it rebuilds playback only.

func generateDeviceID() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func loadCreds() (*storedCreds, error) {
	path, err := credsFile() // ddsonic: refuses the real file under go test
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds storedCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func saveCreds(creds *storedCreds) error {
	path, err := credsFile() // ddsonic: refuses the real file under go test
	if err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600)
}
