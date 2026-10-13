package spotify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/playlist"
)

// builtWith is a session as a builder returns it, with a credential of its
// own that would be seen in the file if it were ever written.
func builtWith(name string) *Session {
	return builtSession("client", nil, storedCreds{Username: name, Data: []byte(name), DeviceID: "dev", RefreshToken: name + "-refresh"},
		&oauth2.Token{AccessToken: name, Expiry: time.Now().Add(time.Hour)})
}

// A build whose call has ended is not published, however late the end came:
// between the build's return and the lock that decides, or while that lock
// was being waited for. The session keeps its coordinator, its token and its
// stored credential, and the build is retired, unwritten. A cancellation
// that arrives after the publication changes nothing: the sign-in stands.
// (The first arrangement is adapted from the reviewer's round-9 probe.)
func TestCancelledBuildIsNotPublished(t *testing.T) {
	for _, kind := range []string{"interactive", "silent"} {
		for _, when := range []string{"after the build returned", "while waiting for the lock", "after the publication"} {
			t.Run(kind+": cancelled "+when, func(t *testing.T) {
				path := storeCreds(t)
				stubSignIn(t, nil) // counts teardowns
				s := goodSession()
				before, tokens, gen := mustRead(t, path), s.viewNow().tokens, s.generation()
				fresh := builtWith("synthetic-new")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				silent := kind == "silent"

				switch when {
				case "after the build returned":
					beforeAccept = cancel
				case "while waiting for the lock":
					s.mu.Lock() // the publication will wait here
					beforeAccept = func() {
						go func() {
							time.Sleep(20 * time.Millisecond) // it is waiting for the lock now
							cancel()
							s.mu.Unlock()
						}()
					}
				}
				t.Cleanup(func() { beforeAccept = nil })
				err := s.reconnect(ctx, func(context.Context, string) (*Session, error) { return fresh, nil }, gen, silent, nil)

				if when == "after the publication" {
					cancel()
					if err != nil || s.generation() != gen+1 {
						t.Fatalf("the publication = %v, build %d -> %d; want it done before the cancellation", err, gen, s.generation())
					}
					if !silent && (s.viewNow().tokens == tokens || !strings.Contains(mustRead(t, path), "synthetic-new")) {
						t.Error("a sign-in that was published before its cancellation did not stand")
					}
					return
				}
				if !errors.Is(err, context.Canceled) {
					t.Errorf("the cancelled build's publication = %v, want the cancellation", err)
				}
				if v := s.viewNow(); v.tokens != tokens || v.gen != gen || tokenOf(s) != "access" {
					t.Errorf("the cancelled build was published: coordinator kept %t, build %d -> %d, token %q", v.tokens == tokens, gen, v.gen, tokenOf(s))
				}
				if got := mustRead(t, path); got != before {
					t.Errorf("the cancelled build changed the stored credential:\n was %q\n now %q", before, got)
				}
				builds.Wait()
				tails.Wait()
				if !fresh.viewNow().closed {
					t.Error("the unpublished build was not retired")
				}
			})
		}
	}
}

// The same for a provider that had no session: a browser sign-in whose call
// ended before its session was installed installs nothing and writes
// nothing; one that was installed first stands.
func TestCancelledSignInIsNotInstalled(t *testing.T) {
	for _, cancelled := range []bool{true, false} {
		name := map[bool]string{true: "cancelled before the install", false: "not cancelled"}[cancelled]
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
			path, _ := CredsPath()
			writes := countWrites(t)
			fresh := builtWith("synthetic-new")
			stubSignIn(t, func(context.Context) (*Session, error) { return fresh, nil })
			p := New(nil, "client", 320)
			if cancelled {
				beforeAccept = func() { cancelSignIn(t, p) }
				t.Cleanup(func() { beforeAccept = nil })
			}

			err := p.Authenticate()
			if !cancelled {
				if err != nil || p.sessionNow() != fresh || !strings.Contains(mustRead(t, path), "synthetic-new") {
					t.Fatalf("Authenticate() = %v, installed %t; want the sign-in taken and stored", err, p.sessionNow() == fresh)
				}
				return
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("the cancelled Authenticate() = %v, want the cancellation", err)
			}
			if p.sessionNow() != nil || p.authEpoch.Load() != 0 || writes.Load() != 0 || credsExist(t, path) {
				t.Errorf("the cancelled sign-in left something: session %t, sign-ins counted %d, writes %d, credential file %t",
					p.sessionNow() != nil, p.authEpoch.Load(), writes.Load(), credsExist(t, path))
			}
			builds.Wait()
			tails.Wait()
			if !fresh.viewNow().closed {
				t.Error("the session that was not installed was not retired")
			}
			if phaseOf(p) != phaseIdle {
				t.Errorf("the provider is left in build phase %d", phaseOf(p))
			}
		})
	}
}

// What a stored build returns is looked at only while its call is live. A
// call that ended before its completion step, by its deadline or by a
// cancellation, ended: a refusal it carries is not remembered against the
// stored sign-in, an error is not classified, and a session is not taken.
// The live controls behave as before. (The refusal case is adapted from the
// reviewer's round-9 probe.)
func TestExpiredStoredBuildResultIsNotClassified(t *testing.T) {
	results := map[string]func() (*Session, error){
		"a refusal": func() (*Session, error) { return nil, errStoredSignInRefused },
		"an outage": func() (*Session, error) { return nil, errors.New("dial tcp: no route to host") },
		"a session": func() (*Session, error) { return builtWith("synthetic-stored"), nil },
	}
	for name, result := range results {
		for _, live := range []bool{false, true} {
			state := map[bool]string{true: "a live call", false: "a call that ended before its completion"}[live]
			t.Run(name+" from "+state, func(t *testing.T) {
				path := storeCreds(t)
				before := mustRead(t, path)
				stubSignIn(t, nil)
				var built *Session
				stubSessionSetup(t, time.Minute, func(context.Context, string) (*Session, error) {
					sess, err := result()
					built = sess
					return sess, err
				})
				p := New(nil, "client", 320)
				if !live {
					buildCommit = func() { // the build has returned; its call ends now
						p.mu.Lock()
						cancel := p.storedCancel
						p.mu.Unlock()
						cancel()
					}
					t.Cleanup(func() { buildCommit = nil })
				}
				writes := countWrites(t)

				err := p.ensureSession()
				p.mu.Lock()
				refused, phase := p.storedRefused, p.phase
				p.mu.Unlock()
				if phase != phaseIdle {
					t.Errorf("the provider is left in build phase %d", phase)
				}
				if live {
					switch name {
					case "a refusal":
						if !refused || !errors.Is(err, playlist.ErrNeedsAuth) {
							t.Errorf("a live refusal: remembered %t, error %v; want it remembered as sign-in required", refused, err)
						}
					case "an outage":
						if refused || err == nil || errors.Is(err, playlist.ErrNeedsAuth) {
							t.Errorf("a live outage: remembered %t, error %v; want an error that asks for no sign-in", refused, err)
						}
					case "a session":
						if err != nil || p.sessionNow() != built {
							t.Errorf("a live session: error %v, taken %t", err, p.sessionNow() == built)
						}
					}
					return
				}
				if !errors.Is(err, context.Canceled) || errors.Is(err, playlist.ErrNeedsAuth) {
					t.Errorf("the ended call = %v, want its cancellation and no sign-in request", err)
				}
				if refused || p.sessionNow() != nil {
					t.Errorf("the ended call's result was used: refusal remembered %t, session taken %t", refused, p.sessionNow() != nil)
				}
				if writes.Load() != 0 || mustRead(t, path) != before {
					t.Errorf("the ended call wrote the credential (%d writes)", writes.Load())
				}
				builds.Wait()
				tails.Wait()
				if built != nil && !built.viewNow().closed {
					t.Error("the session of the ended call was not retired")
				}
			})
		}
	}
}
