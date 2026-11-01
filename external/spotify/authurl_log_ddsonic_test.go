package spotify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/applog"
)

// The sign-in URL carries the flow's state and PKCE challenge. The UI is
// given it whole, but the diagnostic log, a file other users can read, keeps
// no part of it (RC4-S1).
func TestSignInURLReachesTheUIButNotTheLog(t *testing.T) {
	const (
		state     = "STATE-SENTINEL-7f3a"
		challenge = "CHALLENGE-SENTINEL-91c2"
	)
	authURL := "https://accounts.spotify.com/authorize?client_id=synthetic-client&code_challenge=" +
		challenge + "&code_challenge_method=S256&state=" + state

	path := filepath.Join(t.TempDir(), "ddsonic.log")
	closeLog, err := applog.Init(path, applog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	origObserver := authURLObserver.Load()
	t.Cleanup(func() { authURLObserver.Store(origObserver) })
	var seen []string
	SetAuthURLObserver(func(u string) { seen = append(seen, u) })

	notifyAuthURL(authURL)
	if err := closeLog(); err != nil {
		t.Fatal(err)
	}

	if len(seen) != 1 || seen[0] != authURL {
		t.Errorf("observer got %q, want the intact URL once", seen)
	}
	logged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), "spotify: sign-in started") {
		t.Errorf("log %q has no sign-in line; the sink did not record the call", logged)
	}
	for _, secret := range []string{authURL, state, challenge, "code_challenge", "accounts.spotify.com/authorize?"} {
		if strings.Contains(string(logged), secret) {
			t.Errorf("log keeps %q: %q", secret, logged)
		}
	}
}
