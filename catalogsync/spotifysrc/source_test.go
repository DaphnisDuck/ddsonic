package spotifysrc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
)

func ref(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Spotify, ProviderID: id} }

type fakeClient struct {
	albums      []catalog.AlbumRecord
	artists     []catalog.ArtistRecord
	liked       []catalog.TrackRecord
	playlists   []catalog.PlaylistRecord
	items       map[string][]catalog.TrackRecord
	itemErr     map[string]error
	itemFetches []string
	albumCalls  []string
	reads       []string // full reads and head reads of albums and liked
}

func (f *fakeClient) SavedAlbumRecords(context.Context) ([]catalog.AlbumRecord, error) {
	f.reads = append(f.reads, "albums")
	return f.albums, nil
}
func (f *fakeClient) FollowedArtistRecords(context.Context) ([]catalog.ArtistRecord, error) {
	return f.artists, nil
}
func (f *fakeClient) LikedTrackRecords(context.Context) ([]catalog.TrackRecord, error) {
	f.reads = append(f.reads, "liked")
	return f.liked, nil
}

// The heads describe the lists as given, newest first.
func (f *fakeClient) SavedAlbumsHead(context.Context) (catalog.CollectionHead, error) {
	f.reads = append(f.reads, "albums head")
	h := catalog.CollectionHead{Total: len(f.albums)}
	if len(f.albums) > 0 {
		h.NewestID, h.NewestAt = f.albums[0].Ref.ProviderID, f.albums[0].AddedAt
	}
	return h, nil
}
func (f *fakeClient) LikedTracksHead(context.Context) (catalog.CollectionHead, error) {
	f.reads = append(f.reads, "liked head")
	h := catalog.CollectionHead{Total: len(f.liked)}
	if len(f.liked) > 0 {
		h.NewestID, h.NewestAt = f.liked[0].Ref.ProviderID, f.liked[0].AddedAt
	}
	return h, nil
}
func (f *fakeClient) PlaylistRecords(context.Context) ([]catalog.PlaylistRecord, error) {
	// Return a copy: the source fills in tracks.
	return slices.Clone(f.playlists), nil
}
func (f *fakeClient) PlaylistTrackRecords(_ context.Context, id string) ([]catalog.TrackRecord, error) {
	f.itemFetches = append(f.itemFetches, id)
	return f.items[id], f.itemErr[id]
}

func (f *fakeClient) AlbumTrackRecords(_ context.Context, id string) ([]catalog.TrackRecord, error) {
	f.albumCalls = append(f.albumCalls, "retry:"+id)
	return nil, nil
}
func (f *fakeClient) AlbumTrackRecordsOnce(_ context.Context, id string) ([]catalog.TrackRecord, error) {
	f.albumCalls = append(f.albumCalls, "once:"+id)
	return nil, nil
}

func track(id string) catalog.TrackRecord {
	album := catalog.AlbumRecord{Ref: ref("al-" + id), Title: "Album " + id}
	return catalog.TrackRecord{Ref: ref(id), Title: id, PlayableURI: "spotify:track:" + id, Album: &album,
		Artists: []catalog.ArtistRecord{{Ref: ref("ar-" + id), Name: "Artist " + id}}}
}

func TestPlaylistsFetchOnlyChangedTracks(t *testing.T) {
	client := &fakeClient{
		playlists: []catalog.PlaylistRecord{
			{Ref: ref("same"), Name: "Same", Snapshot: "s1"},
			{Ref: ref("changed"), Name: "Changed", Snapshot: "s2"},
			{Ref: ref("new"), Name: "New", Snapshot: "s3"},
		},
		items: map[string][]catalog.TrackRecord{"changed": {track("t1")}, "new": {track("t2")}},
	}
	snap, err := New(client).Fetch(context.Background(), Playlists, catalogsync.Known{
		PlaylistSnapshots: map[string]string{"same": "s1", "changed": "s1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"changed", "new"}; !slices.Equal(client.itemFetches, want) {
		t.Errorf("fetched items of %v, want %v", client.itemFetches, want)
	}
	fetched := map[string]bool{}
	for _, p := range snap.Playlists {
		fetched[p.Ref.ProviderID] = p.TracksFetched
	}
	if fetched["same"] || !fetched["changed"] || !fetched["new"] {
		t.Errorf("TracksFetched = %v", fetched)
	}
}

func TestPlaylistsForbiddenKeepsGoingOtherErrorsFail(t *testing.T) {
	client := &fakeClient{
		playlists: []catalog.PlaylistRecord{{Ref: ref("locked"), Name: "Locked"}, {Ref: ref("ok"), Name: "OK"}},
		items:     map[string][]catalog.TrackRecord{"ok": {track("t1")}},
		itemErr:   map[string]error{"locked": fmt.Errorf("spotify: %w", catalog.ErrForbidden)},
	}
	snap, err := New(client).Fetch(context.Background(), Playlists, catalogsync.Known{})
	if err != nil {
		t.Fatalf("Fetch() = %v, want a forbidden playlist skipped", err)
	}
	if snap.Playlists[0].TracksFetched || !snap.Playlists[1].TracksFetched {
		t.Errorf("playlists = %+v", snap.Playlists)
	}

	client.itemErr["locked"] = errors.New("http status 500")
	if _, err := New(client).Fetch(context.Background(), Playlists, catalogsync.Known{}); err == nil {
		t.Error("Fetch() succeeded despite a playlist failing for another reason")
	}
}

func TestUnknownCollection(t *testing.T) {
	if _, err := New(&fakeClient{}).Fetch(context.Background(), "podcasts", catalogsync.Known{}); err == nil {
		t.Error("Fetch(unknown) = nil")
	}
}

// End to end: a Spotify library synced through the engine is readable from
// the catalog with albums, artists and credits linked by Spotify ID.
func TestSyncIntoCatalog(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	client := &fakeClient{
		albums:    []catalog.AlbumRecord{{Ref: ref("al-t1"), Title: "Album t1", Year: 1990, Artists: []catalog.ArtistRecord{{Ref: ref("ar-t1"), Name: "Artist t1"}}}},
		artists:   []catalog.ArtistRecord{{Ref: ref("ar-t1"), Name: "Artist t1"}},
		liked:     []catalog.TrackRecord{track("t1")},
		playlists: []catalog.PlaylistRecord{{Ref: ref("pl"), Name: "Mine", Own: true, Snapshot: "s1"}},
		items:     map[string][]catalog.TrackRecord{"pl": {track("t1"), track("t2")}},
	}
	eng := catalogsync.New(store, nil, New(client))
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}

	albums, _ := store.Albums(ctx, catalog.Spotify, catalog.ByTitle)
	if len(albums) != 1 || albums[0].Year != 1990 || albums[0].Artist != "Artist t1" {
		t.Errorf("albums = %+v", albums)
	}
	liked, _ := store.LikedTracks(ctx, catalog.Spotify)
	if len(liked) != 1 || liked[0].AlbumID != albums[0].ID || liked[0].Artist != "Artist t1" {
		t.Errorf("liked = %+v, want linked to the saved album", liked)
	}
	artists, _ := store.Artists(ctx, catalog.Spotify)
	if len(artists) != 1 {
		t.Fatalf("artists = %+v", artists)
	}
	pls, _ := store.Playlists(ctx, catalog.Spotify)
	if tracks, _ := store.PlaylistTracks(ctx, pls[0].ID); len(tracks) != 2 {
		t.Errorf("playlist tracks = %+v", tracks)
	}

	// Second sync: the playlist is unchanged, so its items are not refetched.
	client.itemFetches = nil
	if err := eng.Sync(ctx, catalog.Spotify); err != nil {
		t.Fatal(err)
	}
	if len(client.itemFetches) != 0 {
		t.Errorf("refetched unchanged playlist items: %v", client.itemFetches)
	}
	if tracks, _ := store.PlaylistTracks(ctx, pls[0].ID); len(tracks) != 2 {
		t.Errorf("playlist tracks after unchanged sync = %+v", tracks)
	}
}

func TestAlbumTracksRouteByProviderID(t *testing.T) {
	client := &fakeClient{}
	src := New(client)
	ctx := context.Background()
	if _, err := src.AlbumTracks(ctx, ref("al1")); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AlbumTracksOnce(ctx, ref("al2")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"retry:al1", "once:al2"}; !slices.Equal(client.albumCalls, want) {
		t.Errorf("calls = %v, want %v", client.albumCalls, want)
	}
}

// Saved albums and liked tracks are read in full only when their head
// changed, or a day after the last full read.
func TestSavedCollectionsSkipUnchanged(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	album := func(id string, added time.Time) catalog.AlbumRecord {
		return catalog.AlbumRecord{Ref: ref(id), Title: id, AddedAt: added}
	}
	liked := func(id string, added time.Time) catalog.TrackRecord {
		tr := track(id)
		tr.AddedAt = added
		return tr
	}
	client := &fakeClient{
		albums: []catalog.AlbumRecord{album("a2", day.Add(time.Hour)), album("a1", day)},
		liked:  []catalog.TrackRecord{liked("t2", day.Add(time.Hour)), liked("t1", day)},
	}
	src := New(client)
	now := day.Add(48 * time.Hour)
	src.now = func() time.Time { return now }
	eng := catalogsync.New(store, nil, src)
	sync := func() []string {
		t.Helper()
		client.reads = nil
		if err := eng.Sync(ctx, catalog.Spotify); err != nil {
			t.Fatal(err)
		}
		return client.reads
	}

	// Never read in full: no head check.
	if got := sync(); !slices.Equal(got, []string{"albums", "liked"}) {
		t.Errorf("first sync read %q", got)
	}
	// Written just now (the store stamps the real time): heads match.
	now = time.Now()
	if got := sync(); !slices.Equal(got, []string{"albums head", "liked head"}) {
		t.Errorf("unchanged sync read %q", got)
	}
	// A new album moves the newest: albums are read in full.
	client.albums = append([]catalog.AlbumRecord{album("a3", day.Add(2*time.Hour))}, client.albums...)
	if got := sync(); !slices.Equal(got, []string{"albums head", "albums", "liked head"}) {
		t.Errorf("after adding an album read %q", got)
	}
	// A removed liked track changes the count.
	client.liked = client.liked[:1]
	if got := sync(); !slices.Equal(got, []string{"albums head", "liked head", "liked"}) {
		t.Errorf("after unliking read %q", got)
	}
	if tracks, _ := store.LikedTracks(ctx, catalog.Spotify); len(tracks) != 1 {
		t.Errorf("liked = %d tracks, want 1", len(tracks))
	}
	// A day on, both are read in full again.
	now = now.Add(fullReadEvery + time.Minute)
	if got := sync(); !slices.Equal(got, []string{"albums", "liked"}) {
		t.Errorf("a day later read %q", got)
	}
}
