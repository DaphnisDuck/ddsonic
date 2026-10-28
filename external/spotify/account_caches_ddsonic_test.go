package spotify

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
	"golang.org/x/oauth2"
)

// accountCachesLeft reports what of an account's live caches the provider
// still holds: its identity, its playlist list, its track lists and its
// unfinished loads.
func accountCachesLeft(p *SpotifyProvider) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var left []string
	if p.userID != "" || p.meFetched {
		left = append(left, "user ID")
	}
	if p.listCache != nil {
		left = append(left, "playlist list")
	}
	if len(p.trackCache) != 0 {
		left = append(left, "track lists")
	}
	if len(p.pending) != 0 {
		left = append(left, "unfinished loads")
	}
	return left
}

// A sign-in that replaces the session may be another account: nothing the
// old one's library put in the caches survives it (RC4-S2).
func TestReplacementSignInDropsTheOldAccountsCaches(t *testing.T) {
	stubSignIn(t, nil)
	old := tokenSession(&oauth2.Token{AccessToken: "old token"})
	old.refuseSignIn(old.generation())
	p := New(old, "client", 320)
	p.userID, p.meFetched = "old-account", true
	p.listCache, p.listCacheAt = []playlist.PlaylistInfo{{ID: "old-list"}}, time.Now()
	p.trackCache["old-list"] = &playlistCache{snapshotID: "s", tracks: []playlist.Track{{Path: "spotify:track:old"}}, total: 1}
	p.pending["old-list"] = &pendingTracks{want: 100, total: 200}

	if err := p.Authenticate(); err != nil {
		t.Fatal(err)
	}
	if left := accountCachesLeft(p); len(left) != 0 {
		t.Errorf("after the replacement sign-in the provider still holds the old account's %v", left)
	}
}

// A read of the library that began before a replacement sign-in and answers
// after it commits nothing: not the identity, not the playlist list, not a
// track list, not an unfinished load (RC4-S2). Each case holds one request
// until the sign-in has been accepted.
func TestReadAcrossReplacementSignInCommitsNothing(t *testing.T) {
	tests := []struct {
		name string
		hold string
		read func(p *SpotifyProvider)
	}{
		{"identity", "/v1/me", func(p *SpotifyProvider) { p.currentUserID(t.Context()) }},
		{"playlist list held at /v1/me", "/v1/me", func(p *SpotifyProvider) { _, _ = p.Playlists() }},
		{"playlist list held at its page", "/v1/me/playlists", func(p *SpotifyProvider) { _, _ = p.Playlists() }},
		{"track list", "/v1/playlists/pl/items", func(p *SpotifyProvider) { _, _ = p.Tracks("pl") }},
		{"paged track list", "/v1/playlists/pl/items", func(p *SpotifyProvider) { _, _, _ = p.TracksPage("pl", 0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubSignIn(t, nil)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			orig := http.DefaultTransport
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == tt.hold {
					once.Do(func() { close(entered) })
					<-release
				}
				body := `{}`
				switch req.URL.Path {
				case "/v1/me":
					body = `{"id":"old-account"}`
				case "/v1/me/tracks", "/v1/me/albums":
					body = `{"items":[],"total":0}`
				case "/v1/me/playlists":
					body = `{"items":[{"id":"pl","name":"Old","snapshot_id":"s1","owner":{"id":"old-account"},"items":{"total":1}}],"total":1}`
				case "/v1/playlists/pl/items":
					body = `{"items":[{"item":{"id":"t1","name":"Old track","type":"track","uri":"spotify:track:t1","duration_ms":1000}}],"total":1}`
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})
			t.Cleanup(func() { http.DefaultTransport = orig })
			old := tokenSession(&oauth2.Token{AccessToken: "old token"})
			p := New(old, "client", 320)

			done := make(chan struct{})
			go func() {
				defer close(done)
				tt.read(p)
			}()
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatalf("the read never reached %s", tt.hold)
			}
			old.refuseSignIn(old.generation()) // the user signs in again, perhaps as someone else
			if err := p.Authenticate(); err != nil {
				t.Fatal(err)
			}
			close(release)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the read did not finish")
			}
			if left := accountCachesLeft(p); len(left) != 0 {
				t.Errorf("a read begun before the sign-in committed the old account's %v", left)
			}
		})
	}
}
