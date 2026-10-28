package ytmusic

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time" // ddsonic

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta" // ddsonic
	"github.com/bjarneo/cliamp/internal/browser"
	"github.com/bjarneo/cliamp/internal/fileutil"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

// storedCreds holds persisted YouTube Music credentials for re-authentication.
type storedCreds struct {
	RefreshToken string `json:"refresh_token"`
}

// CallbackPort is the fixed port for the OAuth2 callback server.
// Must match the redirect URI registered in the Google Cloud console.
const CallbackPort = 19873

// Session manages a YouTube Data API v3 service for YouTube Music integration.
type Session struct {
	mu           sync.Mutex
	clientID     string
	clientSecret string
	service      *youtube.Service
	tokenSource  oauth2.TokenSource
	cacheScope   string
}

// oauthScopes are the YouTube API scopes needed for cliamp.
var oauthScopes = []string{
	"https://www.googleapis.com/auth/youtube.readonly",
}

// googleOAuthConfig returns the OAuth2 config for the given client ID and secret.
// Google Desktop OAuth requires both a client_id and client_secret (unlike Spotify
// which supports PKCE-only public clients).
func googleOAuthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  fmt.Sprintf("http://127.0.0.1:%d/callback", CallbackPort),
		Scopes:       oauthScopes,
		Endpoint:     google.Endpoint,
	}
}

// NewSession creates a YouTube API session, using stored credentials if
// available, otherwise starting an interactive OAuth2 flow.
func NewSession(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	creds, err := loadCreds()
	if err == nil && creds.RefreshToken != "" {
		s, err := newSessionFromStored(ctx, clientID, clientSecret, creds)
		if err == nil {
			return s, nil
		}
		// Stored credentials failed, fall through to interactive.
	}
	return newInteractiveSession(ctx, clientID, clientSecret)
}

// NewSessionSilent is like NewSession but only uses stored credentials.
// Returns an error if interactive auth is required.
func NewSessionSilent(ctx context.Context, clientID, clientSecret string) (*Session, error) {
	creds, err := loadCreds()
	if err != nil || creds.RefreshToken == "" {
		return nil, fmt.Errorf("no stored credentials")
	}
	return newSessionFromStored(ctx, clientID, clientSecret, creds)
}

// newSessionFromStored creates a session from stored credentials via silent refresh.
func newSessionFromStored(ctx context.Context, clientID, clientSecret string, creds *storedCreds) (*Session, error) {
	token, err := silentTokenRefresh(tokenContext(ctx), clientID, clientSecret, creds.RefreshToken) // ddsonic: ctx
	if err != nil {
		return nil, fmt.Errorf("ytmusic: silent refresh: %w", err)
	}

	ts := sessionTokenSource(ctx, googleOAuthConfig(clientID, clientSecret), token) // ddsonic: bounded, outlives ctx

	svc, err := youtube.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: create service: %w", err)
	}

	// Re-save credentials (refresh token may have been rotated).
	refreshToken := creds.RefreshToken
	if token.RefreshToken != "" {
		refreshToken = token.RefreshToken
		if err := saveCreds(&storedCreds{RefreshToken: refreshToken}); err != nil {
			fmt.Fprintf(os.Stderr, "ytmusic: failed to save credentials: %v\n", err)
		}
	}

	return &Session{
		clientID:     clientID,
		clientSecret: clientSecret,
		service:      svc,
		tokenSource:  ts,
		cacheScope:   oauthCacheScope(clientID, refreshToken),
	}, nil
}

// silentTokenRefresh uses a stored refresh token to get a new access token
// without opening a browser.
func silentTokenRefresh(ctx context.Context, clientID, clientSecret, refreshToken string) (*oauth2.Token, error) { // ddsonic: ctx
	conf := googleOAuthConfig(clientID, clientSecret)
	src := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	return src.Token()
}

// ddsonic: token requests are bounded and follow the caller's ctx. Without a
// client in ctx, oauth2 uses http.DefaultClient, which never times out, so
// a stalled token endpoint held a catalog sync forever.
var tokenHTTPClient = &http.Client{Timeout: 30 * time.Second}

// tokenContext is ctx carrying the bounded client for oauth2's requests.
func tokenContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, tokenHTTPClient)
}

// sessionTokenSource is a session's token source. oauth2 refreshes with the
// context it was built with for as long as the session lives, so it must not
// be the sign-in's: that one ends with the sign-in, and the first refresh,
// about an hour later, failed with "context canceled" until a restart
// (ddsonic: upstream 85a0f120).
func sessionTokenSource(ctx context.Context, conf *oauth2.Config, token *oauth2.Token) oauth2.TokenSource {
	return conf.TokenSource(tokenContext(context.WithoutCancel(ctx)), token)
}

// newInteractiveSession performs an OAuth2 flow to authenticate.
func newInteractiveSession(ctx context.Context, clientID, clientSecret string, opts ...oauth2.AuthCodeOption) (*Session, error) { // ddsonic: opts
	token, err := doOAuth(ctx, clientID, clientSecret, opts...)
	if err != nil {
		return nil, err
	}

	ts := sessionTokenSource(ctx, googleOAuthConfig(clientID, clientSecret), token) // ddsonic: outlives the sign-in's ctx

	svc, err := youtube.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, fmt.Errorf("ytmusic: create service: %w", err)
	}

	// Persist refresh token for future sessions.
	if err := saveCreds(&storedCreds{RefreshToken: token.RefreshToken}); err != nil {
		fmt.Fprintf(os.Stderr, "ytmusic: failed to save credentials: %v\n", err)
	}
	cacheIdentity := token.RefreshToken
	if cacheIdentity == "" {
		cacheIdentity = token.AccessToken
	}

	return &Session{
		clientID:     clientID,
		clientSecret: clientSecret,
		service:      svc,
		tokenSource:  ts,
		cacheScope:   oauthCacheScope(clientID, cacheIdentity),
	}, nil
}

// doOAuth performs an OAuth2 flow: starts localhost server, opens browser,
// exchanges code for token. The context controls cancellation — if ctx is
// cancelled (e.g. the user retries auth), the listener is closed and the
// function returns promptly, freeing the callback port.
func doOAuth(ctx context.Context, clientID, clientSecret string, opts ...oauth2.AuthCodeOption) (*oauth2.Token, error) { // ddsonic: opts
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", CallbackPort)) // ddsonic: the redirect URI's host only, not every interface
	if err != nil {
		return nil, fmt.Errorf("ytmusic: listen on port %d (is another instance running?): %w", CallbackPort, err)
	}
	defer lis.Close() // always release the port

	oauthConf := googleOAuthConfig(clientID, clientSecret)

	verifier := oauth2.GenerateVerifier()
	// ddsonic: a callback without this state is not Google's.
	state := rand.Text()
	authURL := oauthConf.AuthCodeURL(state, append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oauth2.AccessTypeOffline}, opts...)...) // ddsonic: opts

	codeCh := make(chan string, 1)
	go func() {
		if err := http.Serve(lis, oauthCallback(state, codeCh)); err != nil && !errors.Is(err, net.ErrClosed) { // ddsonic: oauthCallback
			fmt.Fprintf(os.Stderr, "ytmusic: auth callback server error: %v\n", err)
		}
	}()

	_ = browser.Open(authURL) // best-effort — user can open the URL manually if this fails

	var code string
	select {
	case code = <-codeCh:
	case <-ctx.Done():
		return nil, fmt.Errorf("ytmusic: authentication cancelled: %w", ctx.Err())
	}

	token, err := oauthConf.Exchange(tokenContext(ctx), code, oauth2.VerifierOption(verifier)) // ddsonic: bounded
	if err != nil {
		return nil, fmt.Errorf("ytmusic: token exchange: %w", err)
	}

	fmt.Println("YouTube Music: authenticated.")
	return token, nil
}

// oauthCallback serves the OAuth redirect: it hands on the first code that
// carries state, and turns away requests without it (another local process
// or page racing the browser). ddsonic: extracted from doOAuth, with the state
// check and a send that never blocks.
func oauthCallback(state string, codeCh chan<- string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "unexpected sign-in callback", http.StatusBadRequest)
			return
		}
		if code := q.Get("code"); code != "" {
			select {
			case codeCh <- code:
			default: // a code is already waiting
			}
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(appmeta.CallbackPage(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>cliamp</title></head>
<body style="font-family:system-ui;display:flex;justify-content:center;align-items:center;height:100vh;margin:0;background:#1a1a2e;color:#e0e0e0">
<div style="text-align:center">
<h2>Authenticated!</h2>
<p>You can close this tab now.</p>
<script>setTimeout(function(){window.close()},1500)</script>
</div></body></html>`))) // ddsonic: the tab title
	}
}

// Service returns the YouTube API service, holding the lock briefly.
func (s *Session) Service() *youtube.Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.service
}

// Close is a no-op for YouTube Music sessions (no persistent connections).
func (s *Session) Close() {}

func credsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ytmusic_credentials.json"), nil
}

func loadCreds() (*storedCreds, error) {
	path, err := credsPath()
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
	path, err := credsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, data, 0o600) // ddsonic: the catalog sync reads it concurrently
}
