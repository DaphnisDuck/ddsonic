package spotify

import (
	"context"
	"errors"
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

// The credential file is fail-closed under test too. With nothing set, the
// package's tests resolve a config directory inside the temporary directory;
// and a test that points HOME somewhere else is refused the file, before it
// is read, written or deleted.
func TestCredentialFileFailsClosedUnderTest(t *testing.T) {
	path, err := CredsPath()
	if err != nil || !underTempDir(path) {
		t.Fatalf("with nothing set, the credential path is %q (%v): not in the temporary directory", path, err)
	}

	outside := "/var/empty/ddsonic-not-a-test-home"
	t.Setenv("HOME", outside)
	if path, _ := CredsPath(); underTempDir(path) {
		t.Fatalf("the case does not leave the temporary directory: %q", path)
	}
	if _, err := loadCreds(); !errors.Is(err, errNoStandIn) {
		t.Errorf("loadCreds() outside the temporary directory = %v, want the guard's refusal", err)
	}
	if err := saveCreds(&storedCreds{Username: "u"}); !errors.Is(err, errNoStandIn) {
		t.Errorf("saveCreds() outside the temporary directory = %v, want the guard's refusal", err)
	}
	if _, err := DeleteCreds(); !errors.Is(err, errNoStandIn) {
		t.Errorf("DeleteCreds() outside the temporary directory = %v, want the guard's refusal", err)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("%s was created", outside)
	}
	// A session built there owns no file and writes none.
	s := builtSession("client", nil, storedCreds{Username: "u", RefreshToken: "r"}, nil)
	s.accept()
	s.Close()
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("%s was created by an accepted session", outside)
	}
}
