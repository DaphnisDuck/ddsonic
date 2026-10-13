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

// The guard looks at where the credential path leads, not at how it is
// spelled: a config directory, or a credential file, that is a symbolic link
// out of the temporary directory is refused for reading, for the atomic
// write and for deletion, whether or not anything exists at the far end.
// What it leads to is left as it was. A link that stays inside the temporary
// directory is an ordinary path. (Adapted from the reviewer's round-9 probe;
// every credential here is synthetic.)
func TestCredentialGuardFollowsSymlinks(t *testing.T) {
	synthetic := []byte(`{"username":"synthetic","data":"AQ==","refresh_token":"synthetic"}`)
	for _, link := range []string{"the config directory", "the credential file", "a credential file that leads nowhere yet"} {
		for _, op := range []string{"load", "save", "delete"} {
			t.Run(link+": "+op, func(t *testing.T) {
				// "Outside" is a directory that is not under the temporary
				// directory the test then narrows TMPDIR to.
				outside := t.TempDir()
				inside := filepath.Join(t.TempDir(), "tmp")
				if err := os.MkdirAll(inside, 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TMPDIR", inside)
				if underTempDir(outside) {
					t.Fatal("the case's outside directory is inside the temporary directory")
				}
				config := filepath.Join(inside, "config")
				target := filepath.Join(outside, "spotify_credentials.json")
				switch link {
				case "the config directory":
					if err := os.Symlink(outside, config); err != nil {
						t.Fatal(err)
					}
				default:
					if err := os.MkdirAll(config, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, filepath.Join(config, "spotify_credentials.json")); err != nil {
						t.Fatal(err)
					}
				}
				exists := link != "a credential file that leads nowhere yet"
				if exists {
					if err := os.WriteFile(target, synthetic, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("CLIAMP_CONFIG_DIR", config)

				var err error
				switch op {
				case "load":
					_, err = loadCreds()
				case "save":
					err = saveCreds(&storedCreds{Username: "written-by-the-test"})
				case "delete":
					_, err = DeleteCreds()
				}
				if !errors.Is(err, errNoStandIn) {
					t.Errorf("%s through %s = %v, want the guard's refusal", op, link, err)
				}
				got, readErr := os.ReadFile(target)
				switch {
				case exists && (readErr != nil || string(got) != string(synthetic)):
					t.Errorf("what the link leads to was changed or removed: %q, %v", got, readErr)
				case !exists && readErr == nil:
					t.Errorf("a file was created outside the temporary directory: %q", got)
				}
			})
		}
	}
	t.Run("a link that stays inside is an ordinary path", func(t *testing.T) {
		root := t.TempDir()
		real := filepath.Join(root, "real")
		if err := os.MkdirAll(real, 0o755); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(root, "config")
		if err := os.Symlink(real, config); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLIAMP_CONFIG_DIR", config)
		if err := saveCreds(&storedCreds{Username: "synthetic"}); err != nil {
			t.Fatal(err)
		}
		if creds, err := loadCreds(); err != nil || creds.Username != "synthetic" {
			t.Errorf("loadCreds() through a link inside the temporary directory = %+v, %v", creds, err)
		}
		if removed, err := DeleteCreds(); err != nil || !removed {
			t.Errorf("DeleteCreds() through a link inside the temporary directory = %t, %v", removed, err)
		}
	})
}

// guardOp runs one of the three things done to the credential file.
func guardOp(t *testing.T, op string) error {
	t.Helper()
	var err error
	switch op {
	case "load":
		_, err = loadCreds()
	case "save":
		err = saveCreds(&storedCreds{Username: "written-by-the-test"})
	case "delete":
		_, err = DeleteCreds()
	default:
		t.Fatalf("unknown operation %q", op)
	}
	return err
}

// A ".." in a link's target is taken the way the filesystem takes it: from
// the directory the links before it led to, not from the spelling. Here
// "pivot" is a link out of the temporary directory, so "pivot/.." is outside
// it, though the spelling "inside/pivot/../x" reads as "inside/x". Such a
// path is refused for reading, for the atomic write and for deletion, with
// the target absolute or relative, as the credential file's link or as the
// config directory's. (Adapted from the reviewer's round-10 probe; every
// credential here is synthetic.)
func TestCredentialGuardTakesParentComponentsAsTheFilesystemDoes(t *testing.T) {
	synthetic := []byte(`{"username":"synthetic","data":"AQ==","refresh_token":"synthetic"}`)
	links := []struct {
		name string
		dir  bool // the config directory is the link, not the credential file
		// target spells the link's target, given the inside directory.
		target func(inside string) string
	}{
		{"the credential file, absolute target", false, func(inside string) string {
			return inside + "/pivot/../spotify_credentials.json"
		}},
		{"the credential file, relative target", false, func(string) string {
			return "../pivot/../spotify_credentials.json"
		}},
		{"the config directory, absolute target", true, func(inside string) string {
			return inside + "/pivot/.."
		}},
		{"the config directory, relative target", true, func(string) string {
			return "pivot/../../" + "outside"
		}},
	}
	for _, link := range links {
		for _, op := range []string{"load", "save", "delete"} {
			t.Run(link.name+": "+op, func(t *testing.T) {
				root := t.TempDir()
				outside := filepath.Join(root, "outside")
				inside := filepath.Join(root, "tmp")
				deep := filepath.Join(outside, "deep")
				for _, dir := range []string{deep, inside} {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("TMPDIR", inside)
				if underTempDir(outside) {
					t.Fatal("the case's outside directory is inside the temporary directory")
				}
				if err := os.Symlink(deep, filepath.Join(inside, "pivot")); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(outside, "spotify_credentials.json")
				if err := os.WriteFile(target, synthetic, 0o600); err != nil {
					t.Fatal(err)
				}
				config := filepath.Join(inside, "config")
				if link.dir {
					if err := os.Symlink(link.target(inside), config); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(config, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(link.target(inside), filepath.Join(config, "spotify_credentials.json")); err != nil {
						t.Fatal(err)
					}
				}
				// The case is what it says: the filesystem reaches the outside
				// file through the config directory.
				if got, err := os.ReadFile(filepath.Join(config, "spotify_credentials.json")); err != nil || string(got) != string(synthetic) {
					t.Fatalf("the case's path does not lead to the outside file: %q, %v", got, err)
				}
				t.Setenv("CLIAMP_CONFIG_DIR", config)

				if err := guardOp(t, op); !errors.Is(err, errNoStandIn) {
					t.Errorf("%s through %s = %v, want the guard's refusal", op, link.name, err)
				}
				if got, err := os.ReadFile(target); err != nil || string(got) != string(synthetic) {
					t.Errorf("what the link leads to was changed or removed: %q, %v", got, err)
				}
			})
		}
	}

	// The same rule the other way: a target spelled through an outside
	// directory whose link leads back in is an ordinary path.
	t.Run("a parent component that leads back inside is an ordinary path", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(root, "outside")
		inside := filepath.Join(root, "tmp")
		deep := filepath.Join(inside, "deep")
		realDir := filepath.Join(inside, "real")
		for _, dir := range []string{outside, deep, realDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("TMPDIR", inside)
		if err := os.Symlink(deep, filepath.Join(outside, "back")); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(inside, "config")
		if err := os.Symlink(outside+"/back/../real", config); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLIAMP_CONFIG_DIR", config)
		if err := saveCreds(&storedCreds{Username: "synthetic"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(realDir, "spotify_credentials.json")); err != nil {
			t.Errorf("the credential was not written where the path leads: %v", err)
		}
		if creds, err := loadCreds(); err != nil || creds.Username != "synthetic" {
			t.Errorf("loadCreds() = %+v, %v", creds, err)
		}
		if removed, err := DeleteCreds(); err != nil || !removed {
			t.Errorf("DeleteCreds() = %t, %v", removed, err)
		}
	})
}

// A path the filesystem would not resolve is refused, and in bounded time: a
// loop of links, and a ".." after a name that does not exist.
func TestCredentialGuardRefusesWhatCannotBeResolved(t *testing.T) {
	cases := []struct {
		name  string
		links map[string]string // name in the config directory -> target
	}{
		{"a loop of links", map[string]string{"spotify_credentials.json": "a", "a": "b", "b": "a"}},
		{"a link to itself", map[string]string{"spotify_credentials.json": "spotify_credentials.json"}},
		{"a parent component after a missing name", map[string]string{"spotify_credentials.json": "missing/../elsewhere.json"}},
	}
	for _, tc := range cases {
		for _, op := range []string{"load", "save", "delete"} {
			t.Run(tc.name+": "+op, func(t *testing.T) {
				config := t.TempDir()
				for name, target := range tc.links {
					if err := os.Symlink(target, filepath.Join(config, name)); err != nil {
						t.Fatal(err)
					}
				}
				t.Setenv("CLIAMP_CONFIG_DIR", config)
				done := make(chan error, 1)
				go func() { done <- guardOp(t, op) }()
				select {
				case err := <-done:
					if !errors.Is(err, errNoStandIn) {
						t.Errorf("%s through %s = %v, want the guard's refusal", op, tc.name, err)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s through %s did not return", op, tc.name)
				}
				entries, err := os.ReadDir(config)
				if err != nil || len(entries) != len(tc.links) {
					t.Errorf("the config directory was changed: %d entries, %v", len(entries), err)
				}
			})
		}
	}
}
