// ddsonic: tests for sign-in error classification and Web API token restore.

package spotify

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/devgianlu/go-librespot/ap"
	"github.com/devgianlu/go-librespot/login5"
	pb "github.com/devgianlu/go-librespot/proto/spotify"
	login5pb "github.com/devgianlu/go-librespot/proto/spotify/login5/v3"

	"github.com/bjarneo/cliamp/playlist"
)

func apLoginError(code pb.ErrorCode) error {
	return &ap.AccesspointLoginError{Message: &pb.APLoginFailed{ErrorCode: &code}}
}

func TestSilentSessionError(t *testing.T) {
	outage := errors.New("faield unmarshalling LoginResponse: proto: cannot parse invalid wire-format data")
	stored := func(err error) error { return fmt.Errorf("spotify: stored auth: %w", err) }
	tests := []struct {
		name      string
		err       error
		needsAuth bool
	}{
		{"no stored credentials", errNoStoredCreds, true},
		{"refresh token revoked", fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth), true},
		{"login5 invalid credentials", stored(&login5.LoginError{Code: login5pb.LoginError_INVALID_CREDENTIALS}), true},
		{"login5 unknown identifier", stored(&login5.LoginError{Code: login5pb.LoginError_UNKNOWN_IDENTIFIER}), true},
		{"accesspoint bad credentials", stored(apLoginError(pb.ErrorCode_BadCredentials)), true},
		{"login5 try again later", stored(&login5.LoginError{Code: login5pb.LoginError_TRY_AGAIN_LATER}), false},
		{"login5 timeout", stored(&login5.LoginError{Code: login5pb.LoginError_TIMEOUT}), false},
		{"login5 too many attempts", stored(&login5.LoginError{Code: login5pb.LoginError_TOO_MANY_ATTEMPTS}), false},
		{"accesspoint try another", stored(apLoginError(pb.ErrorCode_TryAnotherAP)), false},
		{"login5 outage", stored(fmt.Errorf("failed requesting login5 endpoint: %w", outage)), false},
		{"offline", errors.New("dial tcp: lookup apresolve.spotify.com: no such host"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := silentSessionError(tt.err)
			if errors.Is(got, playlist.ErrNeedsAuth) != tt.needsAuth {
				t.Fatalf("silentSessionError() = %v, needs auth %v", got, !tt.needsAuth)
			}
			if !tt.needsAuth && !errors.Is(got, tt.err) {
				t.Errorf("silentSessionError() = %v, lost the cause", got)
			}
		})
	}
}

// fakeTokenEndpoint answers Spotify's token endpoint with status and body.
func fakeTokenEndpoint(t *testing.T, status int, body string) *int {
	t.Helper()
	calls := 0
	orig := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Host, "accounts.spotify.com") {
			t.Errorf("unexpected request to %s", req.URL)
		}
		calls++
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
			Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = orig })
	return &calls
}

// A session whose startup token refresh failed for a passing reason gets
// its Web API token back on the next catalog request, and the rotated
// refresh token is saved.
func TestEnsureWebAPIRestoresToken(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		wantErr   bool
		needsAuth bool
	}{
		{"restored", http.StatusOK, `{"access_token":"new","token_type":"Bearer","refresh_token":"rotated","expires_in":3600}`, false, false},
		{"refresh token revoked", http.StatusBadRequest, `{"error":"invalid_grant"}`, true, true},
		{"token endpoint down", http.StatusServiceUnavailable, `no healthy upstream`, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			if err := saveCreds(&storedCreds{Username: "u", Data: []byte("d"), DeviceID: "dev", RefreshToken: "old"}); err != nil {
				t.Fatal(err)
			}
			calls := fakeTokenEndpoint(t, tt.status, tt.body)
			p := New(tokenSession(nil), "client", 320)

			err := ensureWebAPI(p)
			if (err != nil) != tt.wantErr || errors.Is(err, playlist.ErrNeedsAuth) != tt.needsAuth {
				t.Fatalf("ensureWebAPI() = %v, want error %v, needs auth %v", err, tt.wantErr, tt.needsAuth)
			}
			creds, _ := loadCreds()
			if tt.wantErr {
				if creds.RefreshToken != "old" {
					t.Errorf("refresh token = %q after a failed restore, want it kept", creds.RefreshToken)
				}
				return
			}
			if creds.RefreshToken != "rotated" || creds.Username != "u" {
				t.Errorf("stored creds = %+v, want the rotated refresh token", creds)
			}
			// Restored once; later calls use the token source.
			if err := ensureWebAPI(p); err != nil || *calls != 1 {
				t.Errorf("second ensureWebAPI() = %v after %d token calls, want no new refresh", err, *calls)
			}
		})
	}
}
