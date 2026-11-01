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

// guardOps are the three things done to the credential file.
var guardOps = []string{"load", "save", "delete"}

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

// treeState is everything under dir as Lstat sees it: each entry's mode, a
// link's target, a file's content. Two equal states mean nothing was created,
// removed, re-moded, re-linked or rewritten.
func treeState(t *testing.T, dir string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entry += " -> " + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entry += " " + string(data)
		}
		state[path] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func sameTree(t *testing.T, before, after map[string]string) {
	t.Helper()
	for path, was := range before {
		if is, ok := after[path]; !ok {
			t.Errorf("%s was removed (it was %s)", path, was)
		} else if is != was {
			t.Errorf("%s changed: %s, was %s", path, is, was)
		}
	}
	for path, is := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s was created: %s", path, is)
		}
	}
}

// guardDirs makes a test's two directories, both mode 0755, and narrows the
// temporary directory to the first: "outside" is then not in it.
func guardDirs(t *testing.T) (base, inside, outside string) {
	t.Helper()
	base = t.TempDir()
	inside, outside = filepath.Join(base, "tmp"), filepath.Join(base, "outside")
	for _, dir := range []string{inside, outside} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", inside)
	return base, inside, outside
}

const syntheticCreds = `{"username":"synthetic","data":"AQ==","refresh_token":"synthetic"}`

// The credential file is fail-closed under test. With nothing set, the
// package's tests resolve a config directory inside the temporary directory;
// and a test that points HOME somewhere else is refused the file, before it
// is read, written or deleted.
func TestCredentialFileFailsClosedUnderTest(t *testing.T) {
	if path, err := credsFile(); err != nil {
		t.Fatalf("with nothing set, the credential path is refused: %q, %v", path, err)
	}

	outside := "/var/empty/ddsonic-not-a-test-home"
	t.Setenv("HOME", outside)
	for _, op := range guardOps {
		if err := guardOp(t, op); !errors.Is(err, errNoStandIn) {
			t.Errorf("%s outside the temporary directory = %v, want the guard's refusal", op, err)
		}
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

// Under go test the credential file is a plain path inside the temporary
// directory or it is refused: for reading, for the atomic write and for
// deletion alike, and before any of them touches anything. Each case builds
// its own directories and links; after the refusal everything in them is as
// it was: every entry, every link, every directory's mode, every file's
// content, and nothing new. No case asks where a link leads; a link is
// refused wherever it is and wherever it goes. (The cases come from the
// reviewer's probes of rounds 9, 10 and 11; every credential is synthetic.)
func TestCredentialGuardRefusesAnythingButAPlainPathInside(t *testing.T) {
	const name = "spotify_credentials.json"
	write := func(t *testing.T, path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(syntheticCreds), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mkdir := func(t *testing.T, path string) string {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	link := func(t *testing.T, target, path string) {
		t.Helper()
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
	}
	// pivot is a link in inside to a directory two levels into outside, so
	// "inside/pivot/.." is outside, whatever its spelling suggests.
	pivot := func(t *testing.T, inside, outside string) {
		link(t, mkdir(t, filepath.Join(outside, "deep")), filepath.Join(inside, "pivot"))
		write(t, filepath.Join(outside, name))
	}
	// Each case returns the config directory it has built.
	cases := []struct {
		name  string
		build func(t *testing.T, inside, outside string) string
	}{
		// The original location is outside the temporary directory.
		{"a config directory outside", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(outside, name))
			return outside
		}},
		{"a config directory outside whose entry is a link to a file inside", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(inside, "synthetic.json"))
			link(t, filepath.Join(inside, "synthetic.json"), filepath.Join(outside, name))
			return outside
		}},
		{"the temporary directory itself as the config directory", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(inside, name))
			return inside
		}},
		// A link that leads out.
		{"a config directory that is a link out", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(outside, name))
			link(t, outside, filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		{"a credential file that is a link out", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(outside, name))
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, filepath.Join(outside, name), filepath.Join(config, name))
			return config
		}},
		{"a credential file that is a link out to nothing yet", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, filepath.Join(outside, name), filepath.Join(config, name))
			return config
		}},
		{"a component above the config directory that is a link out", func(t *testing.T, inside, outside string) string {
			mkdir(t, filepath.Join(outside, "config"))
			link(t, outside, filepath.Join(inside, "up"))
			return filepath.Join(inside, "up", "config")
		}},
		// A link whose target has parent components.
		{"a credential file link, absolute, through a parent component", func(t *testing.T, inside, outside string) string {
			pivot(t, inside, outside)
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, inside+"/pivot/../"+name, filepath.Join(config, name))
			return config
		}},
		{"a credential file link, relative, through a parent component", func(t *testing.T, inside, outside string) string {
			pivot(t, inside, outside)
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, "../pivot/../"+name, filepath.Join(config, name))
			return config
		}},
		{"a config directory link, absolute, through a parent component", func(t *testing.T, inside, outside string) string {
			pivot(t, inside, outside)
			link(t, inside+"/pivot/..", filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		{"a config directory link, relative, through a parent component", func(t *testing.T, inside, outside string) string {
			pivot(t, inside, outside)
			link(t, "pivot/../../outside", filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		// A link that stays inside, or comes back in: refused all the same.
		{"a credential file that is a link to a file inside", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			write(t, filepath.Join(inside, "synthetic.json"))
			link(t, filepath.Join(inside, "synthetic.json"), filepath.Join(config, name))
			return config
		}},
		{"a config directory that is a link to a directory inside", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(mkdir(t, filepath.Join(inside, "real")), name))
			link(t, filepath.Join(inside, "real"), filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		{"a config directory link spelled outside that leads back inside", func(t *testing.T, inside, outside string) string {
			mkdir(t, filepath.Join(inside, "real"))
			link(t, mkdir(t, filepath.Join(inside, "deep")), filepath.Join(outside, "back"))
			link(t, outside+"/back/../real", filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		{"a component above the config directory that is a link inside", func(t *testing.T, inside, outside string) string {
			mkdir(t, filepath.Join(inside, "real", "config"))
			link(t, "real", filepath.Join(inside, "alias"))
			return filepath.Join(inside, "alias", "config")
		}},
		// Links the filesystem would not resolve.
		{"a loop of links", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, "a", filepath.Join(config, name))
			link(t, "b", filepath.Join(config, "a"))
			link(t, "a", filepath.Join(config, "b"))
			return config
		}},
		{"a link to itself", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, name, filepath.Join(config, name))
			return config
		}},
		{"a link through a missing name and a parent component", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			link(t, "missing/../elsewhere.json", filepath.Join(config, name))
			return config
		}},
		{"a link to a file followed by a slash", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			write(t, filepath.Join(config, "plain.json"))
			link(t, filepath.Join(config, "plain.json")+"/", filepath.Join(config, name))
			return config
		}},
		{"a link to a file followed by a dot", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			write(t, filepath.Join(config, "plain.json"))
			link(t, filepath.Join(config, "plain.json")+"/.", filepath.Join(config, name))
			return config
		}},
		{"a link to a file followed by a parent component", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			write(t, filepath.Join(config, "plain.json"))
			link(t, filepath.Join(config, "plain.json")+"/../plain.json", filepath.Join(config, name))
			return config
		}},
		// No link at all, and still not a file in a directory.
		{"a config directory that is a regular file", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(inside, "config"))
			return filepath.Join(inside, "config")
		}},
		{"a regular file above the config directory", func(t *testing.T, inside, outside string) string {
			write(t, filepath.Join(inside, "plain"))
			return filepath.Join(inside, "plain", "config")
		}},
		{"a credential file that is a directory", func(t *testing.T, inside, outside string) string {
			config := mkdir(t, filepath.Join(inside, "config"))
			mkdir(t, filepath.Join(config, name))
			return config
		}},
	}
	for _, tc := range cases {
		for _, op := range guardOps {
			t.Run(tc.name+": "+op, func(t *testing.T) {
				base, inside, outside := guardDirs(t)
				t.Setenv("CLIAMP_CONFIG_DIR", tc.build(t, inside, outside))
				before := treeState(t, base)

				done := make(chan error, 1)
				go func() { done <- guardOp(t, op) }()
				select {
				case err := <-done:
					if !errors.Is(err, errNoStandIn) {
						t.Errorf("%s = %v, want the guard's refusal", op, err)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("%s did not return", op)
				}
				sameTree(t, before, treeState(t, base))
			})
		}
	}
}

// What the guard permits: a plain path inside the temporary directory, the
// config directory there already or not, and a temporary directory that is
// itself reached through a link. The path handed out is spelled from the
// resolved temporary directory, so no link is in it.
func TestCredentialGuardPermitsAPlainPathInside(t *testing.T) {
	roundTrip := func(t *testing.T, wantDir string) {
		t.Helper()
		if err := saveCreds(&storedCreds{Username: "synthetic"}); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(wantDir, "spotify_credentials.json")
		if info, err := os.Lstat(file); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Errorf("the credential was not written as a 0600 file at %s: %v, %v", file, info, err)
		}
		if info, err := os.Stat(wantDir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("the config directory is not 0700 after a save: %v, %v", info, err)
		}
		if creds, err := loadCreds(); err != nil || creds.Username != "synthetic" {
			t.Errorf("loadCreds() = %+v, %v", creds, err)
		}
		if removed, err := DeleteCreds(); err != nil || !removed {
			t.Errorf("DeleteCreds() = %t, %v", removed, err)
		}
		if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the credential file is still there after deletion: %v", err)
		}
	}
	t.Run("a config directory that exists", func(t *testing.T) {
		_, inside, _ := guardDirs(t)
		config := filepath.Join(inside, "config")
		if err := os.Mkdir(config, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLIAMP_CONFIG_DIR", config)
		if _, err := loadCreds(); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("loadCreds() with no file yet = %v, want not-exist", err)
		}
		roundTrip(t, config)
	})
	t.Run("a config directory two levels down that does not exist yet", func(t *testing.T) {
		_, inside, _ := guardDirs(t)
		config := filepath.Join(inside, "not", "yet")
		t.Setenv("CLIAMP_CONFIG_DIR", config)
		roundTrip(t, config)
	})
	t.Run("a temporary directory reached through a link", func(t *testing.T) {
		base, inside, outside := guardDirs(t)
		alias := filepath.Join(base, "alias")
		if err := os.Symlink(inside, alias); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TMPDIR", alias)
		t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join(alias, "config"))
		// The resolved spelling of inside: t.TempDir may itself be reached
		// through a link (macOS's /var is /private/var).
		resolved, err := filepath.EvalSymlinks(inside)
		if err != nil {
			t.Fatal(err)
		}
		path, err := credsFile()
		if want := filepath.Join(resolved, "config", "spotify_credentials.json"); err != nil || path != want {
			t.Errorf("credsFile() = %q, %v, want the resolved spelling %q", path, err, want)
		}
		roundTrip(t, filepath.Join(inside, "config"))
		// Its sibling is still outside.
		t.Setenv("CLIAMP_CONFIG_DIR", outside)
		if _, err := credsFile(); !errors.Is(err, errNoStandIn) {
			t.Errorf("credsFile() beside a linked temporary directory = %v, want the guard's refusal", err)
		}
	})
	// The temporary directory named relative to the working directory, and
	// through a link; the config directory by its resolved, absolute path.
	// (Adapted from the reviewer's round-12 probe.)
	t.Run("a relative temporary directory through a link, the config directory resolved", func(t *testing.T) {
		base, inside, outside := guardDirs(t)
		if err := os.Symlink("tmp", filepath.Join(base, "alias")); err != nil {
			t.Fatal(err)
		}
		t.Chdir(base)
		t.Setenv("TMPDIR", "alias")
		config := filepath.Join(inside, "config")
		t.Setenv("CLIAMP_CONFIG_DIR", config)
		path, err := credsFile()
		if want := filepath.Join(config, "spotify_credentials.json"); err != nil || path != want {
			t.Errorf("credsFile() = %q, %v, want %q", path, err, want)
		}
		roundTrip(t, config)
		// The aliased spelling is the same place, and the sibling is outside.
		t.Setenv("CLIAMP_CONFIG_DIR", filepath.Join("alias", "config"))
		if path, err := credsFile(); err != nil || path != filepath.Join(config, "spotify_credentials.json") {
			t.Errorf("credsFile() by the aliased spelling = %q, %v", path, err)
		}
		roundTrip(t, config)
		t.Setenv("CLIAMP_CONFIG_DIR", outside)
		if _, err := credsFile(); !errors.Is(err, errNoStandIn) {
			t.Errorf("credsFile() beside a relative temporary directory = %v, want the guard's refusal", err)
		}
	})
}
