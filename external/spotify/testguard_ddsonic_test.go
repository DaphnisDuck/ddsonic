package spotify

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubBrowser puts a stand-in xdg-open first on PATH that records being run,
// and reports whether it was.
func stubBrowser(t *testing.T) (opened func() bool) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	script := "#!/bin/sh\necho \"$@\" > " + marker + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() bool {
		time.Sleep(50 * time.Millisecond) // the stand-in is started, not waited for
		_, err := os.Stat(marker)
		return err == nil
	}
}

// A test that forgets its stand-ins must fail, not sign in: with the
// product's own seams in place, no browser is started, no callback port is
// opened, and no request reaches Spotify.
func TestSignInFailsClosedUnderTest(t *testing.T) {
	opened := stubBrowser(t)
	origTokens, origSess := interactiveTokens, newLibrespotSession
	interactiveTokens, newLibrespotSession = productionInteractiveTokens, productionLibrespotSession
	t.Cleanup(func() { interactiveTokens, newLibrespotSession = origTokens, origSess })

	t.Run("first sign-in", func(t *testing.T) {
		t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
		p := New(nil, "client", 320)
		done := make(chan error, 1)
		go func() { done <- p.Authenticate() }()
		select {
		case err := <-done:
			if err == nil {
				t.Error("Authenticate() = nil with no stand-in")
			}
		case <-time.After(3 * time.Second):
			p.Close()
			t.Error("Authenticate() started a real sign-in and waited for the browser")
		}
		if conn, err := net.DialTimeout("tcp", callbackAddress(), 200*time.Millisecond); err == nil {
			conn.Close()
			t.Error("the sign-in callback port was opened")
		}
		if opened() {
			t.Error("a browser was started")
		}
	})
	t.Run("stored sign-in", func(t *testing.T) {
		storeCreds(t)
		p := New(nil, "client", 320)
		if err := p.ensureSession(); err == nil {
			t.Error("ensureSession() = nil with no stand-in")
		}
	})
	t.Run("token and web api requests", func(t *testing.T) {
		for _, client := range []*http.Client{tokenHTTPClient, webHTTPClient} {
			req, _ := http.NewRequestWithContext(context.Background(), "GET", "https://accounts.spotify.com/api/token", nil)
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				t.Error("a request reached Spotify with no stand-in")
			} else if !strings.Contains(err.Error(), "stand-in") {
				t.Errorf("the request failed with %v, want the guard's refusal", err)
			}
		}
	})
}
