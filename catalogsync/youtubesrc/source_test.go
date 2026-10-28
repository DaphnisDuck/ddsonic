package youtubesrc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/playlist"
)

func ref(id string) catalog.Ref { return catalog.Ref{Provider: catalog.YouTube, ProviderID: id} }

func track(id, title string) catalog.TrackRecord {
	return catalog.TrackRecord{Ref: ref(id), Title: title, PlayableURI: "https://music.youtube.com/watch?v=" + id,
		Artists: []catalog.ArtistRecord{{Ref: ref("channel:" + id), Name: "Artist " + id}}}
}

type fakeClient struct {
	lists   []catalog.PlaylistRecord
	listErr error
	items   map[string][]catalog.TrackRecord
	itemErr map[string]error
	liked   []catalog.TrackRecord
	fetched []string
}

func (f *fakeClient) PlaylistRecords(context.Context, map[string]string) ([]catalog.PlaylistRecord, error) {
	return slices.Clone(f.lists), f.listErr
}
func (f *fakeClient) PlaylistRecord(_ context.Context, id string) (catalog.PlaylistRecord, error) {
	if err := f.itemErr[id]; err != nil {
		return catalog.PlaylistRecord{}, err
	}
	if _, ok := f.items[id]; !ok {
		return catalog.PlaylistRecord{}, catalog.ErrForbidden
	}
	return catalog.PlaylistRecord{Ref: ref(id), Name: "Saved " + id, Own: true}, nil
}
func (f *fakeClient) PlaylistTrackRecords(_ context.Context, id string) ([]catalog.TrackRecord, error) {
	f.fetched = append(f.fetched, id)
	return f.items[id], f.itemErr[id]
}
func (f *fakeClient) LikedTrackRecords(context.Context) ([]catalog.TrackRecord, error) {
	return f.liked, nil
}

func setup(t *testing.T, client *fakeClient) (*sqlite.Store, *catalogsync.Engine) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, catalogsync.New(store, nil, New(client))
}

func TestSyncsPlaylistsAndLikedMusic(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		lists: []catalog.PlaylistRecord{{Ref: ref("PLroad"), Name: "Road Trip", Own: true}},
		items: map[string][]catalog.TrackRecord{"PLroad": {track("v1", "Libertango"), track("v2", "Gong-Hu")}},
		liked: []catalog.TrackRecord{track("v2", "Gong-Hu")},
	}
	store, eng := setup(t, client)
	if err := eng.Sync(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	pls, _ := store.Playlists(ctx, catalog.YouTube)
	if len(pls) != 1 || pls[0].Name != "Road Trip" || pls[0].TrackCount != 2 {
		t.Fatalf("playlists = %+v", pls)
	}
	tracks, _ := store.PlaylistTracks(ctx, pls[0].ID)
	if len(tracks) != 2 || tracks[0].Title != "Libertango" || tracks[0].PlayableURI != "https://music.youtube.com/watch?v=v1" {
		t.Errorf("tracks = %+v", tracks)
	}
	liked, _ := store.LikedTracks(ctx, catalog.YouTube)
	if len(liked) != 1 || liked[0].Title != "Gong-Hu" {
		t.Errorf("liked = %+v", liked)
	}
	// YouTube's tracks and playlists are searchable, as YouTube's.
	res, err := store.Search(ctx, catalog.ParseQuery("road source:youtube"), 5)
	if err != nil || len(res[catalog.SearchPlaylist]) != 1 {
		t.Errorf("search = %+v, %v", res, err)
	}
}

// A playlist YouTube will not show keeps its stored tracks; a failed feed
// read leaves the collection as it was.
func TestFailuresKeepTheCache(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		lists: []catalog.PlaylistRecord{{Ref: ref("PLa"), Name: "A"}, {Ref: ref("PLb"), Name: "B"}},
		items: map[string][]catalog.TrackRecord{"PLa": {track("a1", "One")}, "PLb": {track("b1", "Two")}},
	}
	store, eng := setup(t, client)
	if err := eng.Sync(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	client.itemErr = map[string]error{"PLb": catalog.ErrForbidden}
	client.items["PLb"] = nil
	if err := eng.Sync(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	pls, _ := store.Playlists(ctx, catalog.YouTube)
	for _, p := range pls {
		if tracks, _ := store.PlaylistTracks(ctx, p.ID); len(tracks) != 1 {
			t.Errorf("%s has %d tracks, want its stored one", p.Name, len(tracks))
		}
	}
	client.listErr = errors.New("yt-dlp: HTTP Error 401")
	if err := eng.Sync(ctx, catalog.YouTube); err == nil {
		t.Error("a failed feed read succeeded")
	}
	if pls, _ := store.Playlists(ctx, catalog.YouTube); len(pls) != 2 {
		t.Errorf("playlists after a failed read = %d, want both kept", len(pls))
	}
}

// A playlist whose marker is unchanged is not refetched.
func TestUnchangedMarkerSkipsTracks(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		lists: []catalog.PlaylistRecord{{Ref: ref("PLa"), Name: "A", Snapshot: "3:etag1"}},
		items: map[string][]catalog.TrackRecord{"PLa": {track("a1", "One")}},
	}
	_, eng := setup(t, client)
	for range 2 {
		if err := eng.Sync(ctx, catalog.YouTube); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(client.fetched, []string{"PLa"}) {
		t.Errorf("fetched %v, want once", client.fetched)
	}
}

// Signed in both ways: liked through OAuth, playlists through cookies, and
// liked through cookies when OAuth needs signing in or refuses.
func TestMixedReadsEachCollectionItsBestWay(t *testing.T) {
	ctx := context.Background()
	oauth := &fakeClient{liked: []catalog.TrackRecord{track("v1", "From OAuth")}, listErr: errors.New("oauth lists nothing")}
	cookies := &fakeClient{
		lists: []catalog.PlaylistRecord{{Ref: ref("PLa"), Name: "Saved"}},
		items: map[string][]catalog.TrackRecord{"PLa": {track("a1", "One")}},
		liked: []catalog.TrackRecord{track("v1", "From cookies")},
	}
	m := Mixed{OAuth: oauth, Cookies: cookies}
	if lists, err := m.PlaylistRecords(ctx, nil); err != nil || len(lists) != 1 || lists[0].Name != "Saved" {
		t.Errorf("playlists = %+v, %v; want cookies'", lists, err)
	}
	if tracks, err := m.PlaylistTrackRecords(ctx, "PLa"); err != nil || len(tracks) != 1 {
		t.Errorf("playlist tracks = %+v, %v", tracks, err)
	}
	if liked, err := m.LikedTrackRecords(ctx); err != nil || liked[0].Title != "From OAuth" {
		t.Errorf("liked = %+v, %v; want OAuth's", liked, err)
	}
	m.OAuth = &needsAuth{}
	if liked, err := m.LikedTrackRecords(ctx); err != nil || liked[0].Title != "From cookies" {
		t.Errorf("liked when signed out = %+v, %v; want cookies'", liked, err)
	}
	m.OAuth = &refuses{}
	if liked, err := m.LikedTrackRecords(ctx); err != nil || liked[0].Title != "From cookies" {
		t.Errorf("liked when OAuth refuses = %+v, %v; want cookies'", liked, err)
	}
}

type refuses struct{ fakeClient }

func (*refuses) LikedTrackRecords(context.Context) ([]catalog.TrackRecord, error) {
	return nil, fmt.Errorf("youtube: playlist LM: quotaExceeded: %w", catalog.ErrForbidden)
}

type needsAuth struct{ fakeClient }

func (*needsAuth) LikedTrackRecords(context.Context) ([]catalog.TrackRecord, error) {
	return nil, fmt.Errorf("youtube: no stored credentials: %w", playlist.ErrNeedsAuth)
}

func TestPlaylistID(t *testing.T) {
	tests := map[string]string{
		"https://music.youtube.com/playlist?list=PLsqtv0Ifjk&si=abc": "PLsqtv0Ifjk",
		"https://www.youtube.com/playlist?list=PLx":                  "PLx",
		"https://www.youtube.com/watch?v=abc&list=PLy":               "PLy",
		"  PLbare  ":                          "PLbare",
		"https://www.youtube.com/watch?v=abc": "",
		"not a link/":                         "",
		"":                                    "",
	}
	for in, want := range tests {
		if got := PlaylistID(in); got != want {
			t.Errorf("PlaylistID(%q) = %q, want %q", in, got, want)
		}
	}
}

// Other people's playlists sync as followed playlists, once, beside the
// account's own; one that is gone is skipped, and any other failure keeps
// the cache.
func TestExtraPlaylists(t *testing.T) {
	ctx := context.Background()
	client := &fakeClient{
		lists: []catalog.PlaylistRecord{{Ref: ref("PLmine"), Name: "Mine", Own: true}},
		items: map[string][]catalog.TrackRecord{
			"PLmine":  {track("m1", "Mine One")},
			"PLsaved": {track("s1", "Saved One"), track("s2", "Saved Two")},
		},
	}
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	src := New(client, "https://music.youtube.com/playlist?list=PLsaved&si=x", "PLmine", "PLgone", "PLsaved", "junk/")
	eng := catalogsync.New(store, nil, src)
	if err := eng.Sync(ctx, catalog.YouTube); err != nil {
		t.Fatal(err)
	}
	pls, _ := store.Playlists(ctx, catalog.YouTube)
	var got []string
	for _, p := range pls {
		got = append(got, fmt.Sprintf("%s own=%v %d", p.Name, p.Own, p.TrackCount))
	}
	if !slices.Equal(got, []string{"Mine own=true 1", "Saved PLsaved own=false 2"}) {
		t.Errorf("playlists = %q", got)
	}
	client.itemErr = map[string]error{"PLsaved": errors.New("network down")}
	if err := eng.Sync(ctx, catalog.YouTube); err == nil {
		t.Error("a failed read of a listed playlist succeeded")
	}
	if pls, _ := store.Playlists(ctx, catalog.YouTube); len(pls) != 2 {
		t.Errorf("after the failure: %d playlists, want both kept", len(pls))
	}
}
