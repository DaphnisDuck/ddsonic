package sqlite

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bjarneo/cliamp/catalog"
)

func sref(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Spotify, ProviderID: id} }

func artistRec(id, name string) catalog.ArtistRecord {
	return catalog.ArtistRecord{Ref: sref(id), Name: name}
}

func apply(t *testing.T, s *Store, snap catalog.Snapshot) {
	t.Helper()
	if snap.Provider == "" {
		snap.Provider = catalog.Spotify
	}
	if err := s.ApplySnapshot(context.Background(), snap); err != nil {
		t.Fatalf("ApplySnapshot(%s): %v", snap.Collection, err)
	}
}

func TestApplySnapshotWritesCreditsAndSortKeys(t *testing.T) {
	s := openTemp(t)
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{{
		Ref: sref("al1"), Title: "The Planets", Year: 1987, TrackCount: 7,
		Artists: []catalog.ArtistRecord{artistRec("ar1", "Dvořák Ensemble"), artistRec("ar2", "Holst")},
	}}})
	var credit, sortArtist, sortTitle string
	if err := s.db.QueryRow(`SELECT artist_credit, sort_artist, sort_title FROM albums`).Scan(&credit, &sortArtist, &sortTitle); err != nil {
		t.Fatal(err)
	}
	if credit != "Dvořák Ensemble, Holst" || sortArtist != "dvorak ensemble" || sortTitle != "the planets" {
		t.Errorf("credit=%q sortArtist=%q sortTitle=%q", credit, sortArtist, sortTitle)
	}
	albums, _ := s.ArtistAlbums(context.Background(), 2)
	if len(albums) != 1 {
		t.Errorf("second credited artist has %d albums, want 1", len(albums))
	}
}

// An album seen only through a liked track carries less detail than the
// saved album; the thinner record must not erase what is stored.
func TestUpsertKeepsKnownDetail(t *testing.T) {
	s := openTemp(t)
	full := catalog.AlbumRecord{Ref: sref("al1"), Title: "Mahler 5", Year: 1990, TrackCount: 5,
		ArtworkURL: "https://img/1", Artists: []catalog.ArtistRecord{artistRec("ar1", "Ozawa")}}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{full}})
	thin := catalog.AlbumRecord{Ref: sref("al1"), Title: "Mahler 5"}
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{{
		Ref: sref("t1"), Title: "I.", Album: &thin, PlayableURI: "spotify:track:t1",
	}}})
	got, err := s.Albums(context.Background(), catalog.Spotify, catalog.ByTitle)
	if err != nil || len(got) != 1 {
		t.Fatalf("Albums() = %+v, %v", got, err)
	}
	if a := got[0]; a.Year != 1990 || a.TrackCount != 5 || a.ArtworkURL != "https://img/1" || a.Artist != "Ozawa" {
		t.Errorf("album after thin upsert = %+v", a)
	}
}

func TestAddedAtIsKeptAcrossSyncs(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	older, newer := time.UnixMilli(1_000), time.UnixMilli(2_000)
	track := func(id string, added time.Time) catalog.TrackRecord {
		return catalog.TrackRecord{Ref: sref(id), Title: id, PlayableURI: "spotify:track:" + id, AddedAt: added}
	}
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{track("a", older), track("b", newer)}})
	// A later sync that does not report added times keeps the stored ones.
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{track("a", time.Time{}), track("b", time.Time{})}})
	liked, _ := s.LikedTracks(ctx, catalog.Spotify)
	if len(liked) != 2 || liked[0].Title != "b" {
		t.Errorf("LikedTracks() = %+v, want b (liked later) first", liked)
	}
}

func TestPlaylistSnapshotAdvancesOnlyWithTracks(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	pl := func(snapshot string, fetched bool, trackIDs ...string) catalog.PlaylistRecord {
		p := catalog.PlaylistRecord{Ref: sref("pl"), Name: "Mine", Own: true, Snapshot: snapshot, TracksFetched: fetched}
		for _, id := range trackIDs {
			p.Tracks = append(p.Tracks, catalog.TrackRecord{Ref: sref(id), Title: id, PlayableURI: "spotify:track:" + id})
		}
		return p
	}
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{pl("s1", true, "a", "b")}})

	// Unchanged: the source skipped the tracks, so the stored list stays.
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{pl("s1", false)}})
	pls, _ := s.Playlists(ctx, catalog.Spotify)
	if tracks, _ := s.PlaylistTracks(ctx, pls[0].ID); len(tracks) != 2 {
		t.Fatalf("unchanged playlist has %d tracks, want 2", len(tracks))
	}
	// A new marker without fetched tracks must not be recorded.
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{pl("s2", false)}})
	if snaps, _ := s.PlaylistSnapshots(ctx, catalog.Spotify); snaps["pl"] != "s1" {
		t.Errorf("snapshot = %q, want s1 kept until its tracks are fetched", snaps["pl"])
	}
	// Changed and fetched: the list is replaced and the marker advances.
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{pl("s2", true, "c")}})
	tracks, _ := s.PlaylistTracks(ctx, pls[0].ID)
	if len(tracks) != 1 || tracks[0].Title != "c" {
		t.Errorf("changed playlist tracks = %+v, want [c]", tracks)
	}
	if snaps, _ := s.PlaylistSnapshots(ctx, catalog.Spotify); snaps["pl"] != "s2" {
		t.Errorf("snapshot = %q, want s2", snaps["pl"])
	}
}

func TestRecordSyncFailureKeepsLastSuccess(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	apply(t, s, catalog.Snapshot{Collection: "albums"})
	before, _ := s.SyncStatus(ctx, catalog.Spotify)
	if err := s.RecordSyncFailure(ctx, catalog.Spotify, "albums", errors.New("http status 429")); err != nil {
		t.Fatal(err)
	}
	after, _ := s.SyncStatus(ctx, catalog.Spotify)
	if len(after) != 1 || after[0].LastError != "http status 429" || !after[0].LastSuccess.Equal(before[0].LastSuccess) {
		t.Errorf("status after failure = %+v (before %+v)", after, before)
	}
	// The next success clears the error.
	apply(t, s, catalog.Snapshot{Collection: "albums"})
	if st, _ := s.SyncStatus(ctx, catalog.Spotify); st[0].LastError != "" {
		t.Errorf("error not cleared by success: %+v", st[0])
	}
}

func TestApplySnapshotRequiresIdentity(t *testing.T) {
	s := openTemp(t)
	if err := s.ApplySnapshot(context.Background(), catalog.Snapshot{Provider: catalog.Spotify}); err == nil {
		t.Error("ApplySnapshot without a collection succeeded")
	}
}

func sweep(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Sweep(context.Background(), catalog.Spotify); err != nil {
		t.Fatal(err)
	}
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A cached album keeps its whole track list after leaving the library, even
// when one of its tracks is still liked: the cache is never left partial.
func TestSweepKeepsCachedLibraryAlbumsWhole(t *testing.T) {
	s := openTemp(t)
	album := catalog.AlbumRecord{Ref: sref("al"), Title: "Mahler 5", Artists: []catalog.ArtistRecord{artistRec("ar", "Ozawa")}}
	tracks := func(ids ...string) (out []catalog.TrackRecord) {
		for _, id := range ids {
			out = append(out, catalog.TrackRecord{Ref: sref(id), Title: id, Album: &album, PlayableURI: "spotify:track:" + id})
		}
		return out
	}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{album}})
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: tracks("t1", "t2", "t3")})
	if err := s.CacheAlbumTracks(context.Background(), sref("al"), tracks("t1", "t2", "t3")); err != nil {
		t.Fatal(err)
	}
	// Only t1 stays liked: the saved, cached album keeps its whole list.
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: tracks("t1")})
	sweep(t, s)
	if got := count(t, s, "tracks"); got != 3 {
		t.Errorf("tracks after sweep = %d, want the cached album's 3", got)
	}

	// The album leaves the library: its cache goes, and only the liked t1
	// (and the album row it needs) stays.
	apply(t, s, catalog.Snapshot{Collection: "albums"})
	sweep(t, s)
	var cached int
	if err := s.db.QueryRow(`SELECT count(*) FROM albums WHERE tracks_cached_at IS NOT NULL`).Scan(&cached); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, "tracks"); got != 1 || count(t, s, "albums") != 1 || cached != 0 {
		t.Errorf("after un-saving: tracks %d, albums %d, cached %d; want t1, its album, uncached",
			got, count(t, s, "albums"), cached)
	}

	// Once unreferenced, everything goes, credits included.
	apply(t, s, catalog.Snapshot{Collection: "liked"})
	sweep(t, s)
	for _, table := range []string{"tracks", "albums", "artists"} {
		if got := count(t, s, table); got != 0 {
			t.Errorf("%s after sweep = %d, want 0", table, got)
		}
	}
}

func TestSweepKeepsPlaylistAndLocalFileTracks(t *testing.T) {
	s := openTemp(t)
	track := catalog.TrackRecord{Ref: sref("t1"), Title: "t1", PlayableURI: "spotify:track:t1"}
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{{
		Ref: sref("pl"), Name: "Mine", Snapshot: "s1", TracksFetched: true, Tracks: []catalog.TrackRecord{track},
	}}})
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{{Ref: sref("t2"), Title: "t2", PlayableURI: "u"}}})
	if _, err := s.db.Exec(`INSERT INTO local_files (path, size, mtime_ns, track_id) SELECT '/m/x.flac', 1, 1, id FROM tracks WHERE provider_id = 't2'`); err != nil {
		t.Fatal(err)
	}
	apply(t, s, catalog.Snapshot{Collection: "liked"})
	sweep(t, s)
	if got := count(t, s, "tracks"); got != 2 {
		t.Errorf("tracks = %d, want the playlist's and the indexed file's", got)
	}
	// Leaving the library removes the playlist and then its track.
	apply(t, s, catalog.Snapshot{Collection: "playlists"})
	sweep(t, s)
	if got := count(t, s, "playlists"); got != 0 {
		t.Errorf("playlists = %d, want 0", got)
	}
	if got := count(t, s, "tracks"); got != 1 {
		t.Errorf("tracks = %d, want only the indexed file's", got)
	}
}

func TestApplySnapshotRejectsForeignAndAnonymousRecords(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	tests := map[string]catalog.AlbumRecord{
		"other provider": {Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "x"}, Title: "X"},
		"no id":          {Ref: catalog.Ref{Provider: catalog.Spotify}, Title: "X"},
	}
	for name, rec := range tests {
		err := s.ApplySnapshot(ctx, catalog.Snapshot{Provider: catalog.Spotify, Collection: "albums", Albums: []catalog.AlbumRecord{rec}})
		if err == nil {
			t.Errorf("%s: ApplySnapshot accepted it", name)
		}
	}
	if got := count(t, s, "albums"); got != 0 {
		t.Errorf("albums = %d, want the rejected snapshots rolled back", got)
	}
}

func TestPlayableURIIsNeverBlanked(t *testing.T) {
	s := openTemp(t)
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{{Ref: sref("t1"), Title: "t1", PlayableURI: "spotify:track:t1"}}})
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{{Ref: sref("t1"), Title: "t1"}}})
	liked, _ := s.LikedTracks(context.Background(), catalog.Spotify)
	if len(liked) != 1 || liked[0].PlayableURI != "spotify:track:t1" {
		t.Errorf("liked = %+v, want the URI kept", liked)
	}
}

func TestRecordSyncFailureTruncates(t *testing.T) {
	s := openTemp(t)
	long := make([]byte, 2*maxErrorLen)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.RecordSyncFailure(context.Background(), catalog.Spotify, "albums", errors.New(string(long))); err != nil {
		t.Fatal(err)
	}
	st, _ := s.SyncStatus(context.Background(), catalog.Spotify)
	if n := len(st[0].LastError); n > maxErrorLen+len("…") {
		t.Errorf("stored error length = %d, want at most %d", n, maxErrorLen)
	} // Multi-byte text is cut between characters, never inside one.
	wide := strings.Repeat("é", maxErrorLen) // 2 bytes each; the cap falls mid-rune on odd offsets
	if err := s.RecordSyncFailure(context.Background(), catalog.Spotify, "albums", errors.New("x"+wide)); err != nil {
		t.Fatal(err)
	}
	st, _ = s.SyncStatus(context.Background(), catalog.Spotify)
	if !utf8.ValidString(st[0].LastError) {
		t.Errorf("stored error %q is not valid UTF-8", st[0].LastError)
	}
}

// watchWrites records every update and delete of the entity tables on the
// write connection, and returns a func listing them since the last call.
func watchWrites(t *testing.T, s *Store) func() []string {
	t.Helper()
	if _, err := s.wdb.Exec(`CREATE TEMP TABLE writes (what TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"artists", "albums", "tracks", "playlists", "album_artists", "track_artists", "playlist_tracks", "local_files"} {
		for _, op := range []string{"UPDATE", "DELETE"} {
			if _, err := s.wdb.Exec(`CREATE TEMP TRIGGER watch_` + table + `_` + op + ` AFTER ` + op + ` ON main.` + table +
				` BEGIN INSERT INTO writes VALUES ('` + op + ` ` + table + `'); END`); err != nil {
				t.Fatal(err)
			}
		}
	}
	return func() []string {
		var out []string
		rows, err := s.wdb.Query(`SELECT what FROM writes`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var w string
			rows.Scan(&w)
			out = append(out, w)
		}
		rows.Close()
		if _, err := s.wdb.Exec(`DELETE FROM writes`); err != nil {
			t.Fatal(err)
		}
		return out
	}
}

// Syncing what is already stored rewrites no row; a changed value
// rewrites only its row.
func TestUnchangedSyncRewritesNothing(t *testing.T) {
	s := openTemp(t)
	artists := []catalog.ArtistRecord{artistRec("ar1", "Ozawa"), artistRec("ar2", "BSO"), artistRec("ar1", "Ozawa")}
	album := catalog.AlbumRecord{Ref: sref("al1"), Title: "Mahler 5", Year: 1990, Artists: artists}
	track := func(id, title string) catalog.TrackRecord {
		return catalog.TrackRecord{Ref: sref(id), Title: title, Artists: artists, Album: &album, PlayableURI: "spotify:track:" + id}
	}
	snaps := func(title string) []catalog.Snapshot {
		return []catalog.Snapshot{
			{Collection: "albums", Albums: []catalog.AlbumRecord{album}},
			{Collection: "liked", Tracks: []catalog.TrackRecord{track("t1", title)}},
			{Collection: "playlists", Playlists: []catalog.PlaylistRecord{{Ref: sref("pl1"), Name: "Mix", Snapshot: "s1",
				TracksFetched: true, TrackCount: 2, Tracks: []catalog.TrackRecord{track("t2", "II."), track("t1", title)}}}},
		}
	}
	for _, snap := range snaps("I.") {
		apply(t, s, snap)
	}
	local := catalog.Snapshot{Provider: catalog.Local, Collection: "files", Files: true, Tracks: []catalog.TrackRecord{{
		Ref: catalog.Ref{Provider: catalog.Local, ProviderID: "/m/a.flac"}, Title: "A", PlayableURI: "/m/a.flac",
		File: &catalog.FileStat{Path: "/m/a.flac", Size: 10, MTimeNS: 1}}}}
	apply(t, s, local)

	writes := watchWrites(t, s)
	for _, snap := range append(snaps("I."), local) {
		apply(t, s, snap)
	}
	if got := writes(); len(got) != 0 {
		t.Errorf("unchanged sync wrote %q", got)
	}
	for _, snap := range snaps("I. Trauermarsch") {
		apply(t, s, snap)
	}
	if got := writes(); !slices.Equal(got, []string{"UPDATE tracks"}) {
		t.Errorf("a retitled track wrote %q, want its row once", got)
	}
	// A listing without tracks or a count keeps the stored count.
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{{Ref: sref("pl1"), Name: "Mix"}}})
	if pls, _ := s.Playlists(context.Background(), catalog.Spotify); len(pls) != 1 || pls[0].TrackCount != 2 {
		t.Errorf("playlists after an unfetched listing = %+v", pls)
	}
	tracks, _ := s.PlaylistTracks(context.Background(), 1)
	if len(tracks) != 2 || tracks[1].Title != "I. Trauermarsch" || tracks[1].Artist != "Ozawa, BSO, Ozawa" {
		t.Errorf("playlist tracks = %+v", tracks)
	}
}
