//go:build !windows

// ddsonic: pins the Playlists() sections the library navigation splits on.

package spotify

import (
	"context"
	"net/http"
	"testing"

	"github.com/bjarneo/cliamp/library"
)

func TestLibraryContract(t *testing.T) {
	if library.SpotifyLikedSongsID != savedTracksPlaylistID {
		t.Fatalf("library Spotify IDs drifted from provider constants")
	}

	p := fakeSpotifyAPI(t, func(req *http.Request) (any, int) {
		switch req.URL.Path {
		case "/v1/me":
			return map[string]any{"id": "me"}, 0
		case "/v1/me/tracks":
			return map[string]any{"total": 1, "items": []any{}}, 0
		case "/v1/me/playlists":
			return map[string]any{"total": 2, "items": []map[string]any{
				{"id": "owned", "name": "Owned", "owner": map[string]any{"id": "me"}},
				{"id": "followed", "name": "Followed", "owner": map[string]any{"id": "other"}},
			}}, 0
		case "/v1/me/albums":
			if req.URL.Query().Get("offset") != "0" {
				return map[string]any{"total": 1, "items": []any{}}, 0
			}
			return map[string]any{"total": 1, "items": []map[string]any{
				{"album": map[string]any{"id": "al0", "name": "Mahler 5", "artists": []map[string]any{{"name": "Ozawa"}}}},
			}}, 0
		}
		t.Errorf("unexpected path %s", req.URL.Path)
		return nil, http.StatusNotFound
	})

	menu, err := library.Spotify(p).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"Albums": 1, "Playlists": 2}
	for _, e := range menu {
		n, ok := want[e.Title]
		if !ok {
			continue
		}
		rows, err := e.Open.Load(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", e.Title, err)
		}
		if len(rows) != n {
			t.Errorf("%s has %d rows, want %d: %+v", e.Title, len(rows), n, rows)
		}
		delete(want, e.Title)
	}
	if len(want) != 0 {
		t.Errorf("menu is missing %v", want)
	}
}
