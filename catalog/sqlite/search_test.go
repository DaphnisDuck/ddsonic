package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// search runs a typed query and returns each kind's titles, best first.
func search(t *testing.T, s *Store, query string) map[catalog.SearchKind][]string {
	t.Helper()
	res, err := s.Search(context.Background(), catalog.ParseQuery(query), 10)
	if err != nil {
		t.Fatalf("Search(%q): %v", query, err)
	}
	out := map[catalog.SearchKind][]string{}
	for kind, rs := range res {
		for _, r := range rs {
			if r.Kind != kind {
				t.Errorf("result %+v listed under %s", r, kind)
			}
			var title string
			switch {
			case r.Artist != nil:
				title = r.Artist.Name
			case r.Album != nil:
				title = r.Album.Title
			case r.Track != nil:
				title = r.Track.Title
			case r.Playlist != nil:
				title = r.Playlist.Name
			}
			out[kind] = append(out[kind], title)
		}
	}
	return out
}

func ref(provider, id string) catalog.Ref { return catalog.Ref{Provider: provider, ProviderID: id} }

// seedSearch holds a small two-source library: Spotify saved albums,
// followed artists, liked tracks and a playlist, plus local files.
func seedSearch(t *testing.T) *Store {
	t.Helper()
	s := openTemp(t)
	dvorak := catalog.ArtistRecord{Ref: sref("dvorak"), Name: "Antonín Dvořák"}
	holst := catalog.ArtistRecord{Ref: sref("holst"), Name: "Gustav Holst"}
	marsVolta := catalog.ArtistRecord{Ref: sref("mv"), Name: "The Mars Volta"}
	newWorld := catalog.AlbumRecord{Ref: sref("nw"), Title: "Symphony No. 9 \"From the New World\"", Artists: []catalog.ArtistRecord{dvorak}, Year: 1893}
	planets := catalog.AlbumRecord{Ref: sref("pl"), Title: "The Planets", Artists: []catalog.ArtistRecord{holst}}
	hits := catalog.AlbumRecord{Ref: sref("hits-saved"), Title: "Greatest Hits", Artists: []catalog.ArtistRecord{holst}}
	otherHits := catalog.AlbumRecord{Ref: sref("hits-other"), Title: "Greatest Hits", Artists: []catalog.ArtistRecord{marsVolta}}
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{newWorld, planets, hits}})
	apply(t, s, catalog.Snapshot{Collection: "artists", Artists: []catalog.ArtistRecord{holst}})
	apply(t, s, catalog.Snapshot{Collection: "liked", Tracks: []catalog.TrackRecord{
		{Ref: sref("mars"), Title: "Mars, the Bringer of War", Artists: []catalog.ArtistRecord{holst}, Album: &planets, PlayableURI: "spotify:track:mars"},
		{Ref: sref("cygnus"), Title: "Cygnus", Artists: []catalog.ArtistRecord{marsVolta}, Album: &otherHits, PlayableURI: "spotify:track:cygnus"},
		{Ref: sref("help"), Title: "Help Me Rhonda", Artists: []catalog.ArtistRecord{marsVolta}, PlayableURI: "spotify:track:hmr"},
		{Ref: sref("help2"), Title: "Help", Artists: []catalog.ArtistRecord{marsVolta}, PlayableURI: "spotify:track:help"},
		{Ref: sref("adagietto"), Title: "Adagietto", Artists: []catalog.ArtistRecord{{Ref: sref("mahler"), Name: "Gustav Mahler"}}, PlayableURI: "spotify:track:ad"},
	}})
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{
		{Ref: sref("p1"), Name: "Planets for Work", TracksFetched: true},
	}})
	local := catalog.AlbumRecord{Ref: ref(catalog.Local, "/m/holst"), Title: "The Planets", Artists: []catalog.ArtistRecord{{Ref: ref(catalog.Local, "holst"), Name: "Holst"}}}
	apply(t, s, catalog.Snapshot{Provider: catalog.Local, Collection: "files", Files: true,
		Albums:  []catalog.AlbumRecord{local},
		Artists: local.Artists,
		Tracks: []catalog.TrackRecord{{Ref: ref(catalog.Local, "/m/holst/1.flac"), Title: "Jupiter", Genre: "Classical",
			Artists: local.Artists, Album: &local, PlayableURI: "/m/holst/1.flac",
			File: &catalog.FileStat{Path: "/m/holst/1.flac", Size: 1, MTimeNS: 1}}},
	})
	return s
}

func TestSearchFindsEveryKind(t *testing.T) {
	s := seedSearch(t)
	tests := []struct {
		query string
		kind  catalog.SearchKind
		want  []string
	}{
		// Accents fold, and every word matches as a prefix.
		{"dvorak new", catalog.SearchAlbum, []string{"Symphony No. 9 \"From the New World\""}},
		{"antonin", catalog.SearchArtist, []string{"Antonín Dvořák"}},
		{"sym wor", catalog.SearchAlbum, []string{"Symphony No. 9 \"From the New World\""}},
		{"jup", catalog.SearchTrack, []string{"Jupiter"}},
		{"planets work", catalog.SearchPlaylist, []string{"Planets for Work"}},
		// A track's album and genre are searchable.
		{"planets bringer", catalog.SearchTrack, []string{"Mars, the Bringer of War"}},
		{"classical", catalog.SearchTrack, []string{"Jupiter"}},
	}
	for _, tt := range tests {
		if got := search(t, s, tt.query)[tt.kind]; !slices.Equal(got, tt.want) {
			t.Errorf("%q %ss = %q, want %q", tt.query, tt.kind, got, tt.want)
		}
	}
	if got := search(t, s, "zzz"); len(got) != 0 {
		t.Errorf("nonsense found %v", got)
	}
}

func TestSearchOperators(t *testing.T) {
	s := seedSearch(t)
	tests := []struct {
		query string
		want  map[catalog.SearchKind][]string
	}{
		// artist: limits to the artist field: The Mars Volta's tracks, not
		// Holst's "Mars".
		{"artist:mars", map[catalog.SearchKind][]string{
			catalog.SearchArtist: {"The Mars Volta"},
			catalog.SearchAlbum:  {"Greatest Hits"},
			catalog.SearchTrack:  {"Help", "Cygnus", "Help Me Rhonda"},
		}},
		{"title:mars", map[catalog.SearchKind][]string{
			catalog.SearchArtist: {"The Mars Volta"},
			catalog.SearchTrack:  {"Mars, the Bringer of War"},
		}},
		{"planets source:local", map[catalog.SearchKind][]string{
			catalog.SearchAlbum: {"The Planets"},
			catalog.SearchTrack: {"Jupiter"},
		}},
		{"planets type:playlist", map[catalog.SearchKind][]string{
			catalog.SearchPlaylist: {"Planets for Work"},
		}},
		// genre: only tracks carry genres.
		{"genre:class", map[catalog.SearchKind][]string{
			catalog.SearchTrack: {"Jupiter"},
		}},
		{`album:"the planets" artist:holst`, map[catalog.SearchKind][]string{
			catalog.SearchAlbum: {"The Planets", "The Planets"},
			catalog.SearchTrack: {"Mars, the Bringer of War", "Jupiter"},
		}},
	}
	for _, tt := range tests {
		got := search(t, s, tt.query)
		if len(got) != len(tt.want) {
			t.Errorf("%q = %q, want %q", tt.query, got, tt.want)
			continue
		}
		for kind, want := range tt.want {
			if !slices.Equal(sortedIfTied(got[kind], want), want) {
				t.Errorf("%q %ss = %q, want %q", tt.query, kind, got[kind], want)
			}
		}
	}
}

// sortedIfTied lets tests list results whose order the ranking does not
// fix in any order: it returns got reordered to want when both hold the
// same items.
func sortedIfTied(got, want []string) []string {
	a, b := slices.Clone(got), slices.Clone(want)
	slices.Sort(a)
	slices.Sort(b)
	if slices.Equal(a, b) {
		return want
	}
	return got
}

func TestSearchRanking(t *testing.T) {
	s := seedSearch(t)
	// A title match beats an artist match: Holst's "Mars" before The Mars
	// Volta's tracks.
	if got := search(t, s, "mars")[catalog.SearchTrack]; len(got) < 2 || got[0] != "Mars, the Bringer of War" {
		t.Errorf("mars tracks = %q, want the title match first", got)
	}
	// An exact title beats a longer one.
	if got := search(t, s, "help")[catalog.SearchTrack]; !slices.Equal(got, []string{"Help", "Help Me Rhonda"}) {
		t.Errorf("help tracks = %q", got)
	}
	// A saved album beats an unsaved one with the same title.
	res, err := s.Search(context.Background(), catalog.ParseQuery("greatest hits"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if albums := res[catalog.SearchAlbum]; len(albums) != 2 || albums[0].Album.Ref.ProviderID != "hits-saved" ||
		albums[0].Score >= albums[1].Score {
		t.Errorf("greatest hits albums = %+v, want the saved one first", albums)
	}
	// A followed artist beats one merely credited on a track.
	if got := search(t, s, "gustav")[catalog.SearchArtist]; !slices.Equal(got, []string{"Gustav Holst", "Gustav Mahler"}) {
		t.Errorf("gustav artists = %q, want the followed artist first", got)
	}
	// An exact name beats a longer one, across sources.
	if got := search(t, s, "holst")[catalog.SearchArtist]; !slices.Equal(got, []string{"Holst", "Gustav Holst"}) {
		t.Errorf("holst artists = %q, want the exact name first", got)
	}
}

func TestSearchStationsFavoritesFirst(t *testing.T) {
	s := openTemp(t)
	station := func(id, name, tags string) catalog.TrackRecord {
		return catalog.TrackRecord{Ref: ref(catalog.Radio, id), Title: name, Genre: tags, PlayableURI: "https://stream/" + id}
	}
	for _, snap := range []catalog.Snapshot{
		// The custom station matches "jazz" better (in its name) than the
		// favorite (only in its tags), yet the favorite comes first.
		{Provider: catalog.Radio, Collection: "custom", Tracks: []catalog.TrackRecord{station("c", "Jazz Jazz Jazz", "jazz")}},
		{Provider: catalog.Radio, Collection: "favorites", Tracks: []catalog.TrackRecord{station("f", "WBGO Newark", "jazz")}},
	} {
		apply(t, s, snap)
	}
	got := search(t, s, "jazz")
	if !slices.Equal(got[catalog.SearchStation], []string{"WBGO Newark", "Jazz Jazz Jazz"}) || len(got[catalog.SearchTrack]) != 0 {
		t.Errorf("jazz = %q, want the favorite station first and no tracks", got)
	}
}

// The index follows every write: upserts, renames, sweeps, cached album
// tracks and album retitles.
func TestSearchIndexFollowsWrites(t *testing.T) {
	s := seedSearch(t)
	ctx := context.Background()
	// Rename: the old title stops matching.
	apply(t, s, catalog.Snapshot{Collection: "playlists", Playlists: []catalog.PlaylistRecord{
		{Ref: sref("p1"), Name: "Deep Focus", TracksFetched: true},
	}})
	if got := search(t, s, "planets type:playlist"); len(got) != 0 {
		t.Errorf("renamed playlist still found: %q", got)
	}
	if got := search(t, s, "focus")[catalog.SearchPlaylist]; !slices.Equal(got, []string{"Deep Focus"}) {
		t.Errorf("focus = %q", got)
	}
	// Sweep: un-liked tracks leave the index.
	apply(t, s, catalog.Snapshot{Collection: "liked"})
	sweep(t, s)
	if got := search(t, s, "cygnus"); len(got) != 0 {
		t.Errorf("swept track still found: %q", got)
	}
	// Cached album tracks become searchable.
	if err := s.CacheAlbumTracks(ctx, sref("nw"), []catalog.TrackRecord{
		{Ref: sref("largo"), Title: "II. Largo", PlayableURI: "spotify:track:largo"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := search(t, s, "largo dvorak")[catalog.SearchTrack]; len(got) != 0 {
		t.Errorf("track matched on an artist it is not credited to: %q", got)
	}
	if got := search(t, s, `largo album:"new world"`)[catalog.SearchTrack]; !slices.Equal(got, []string{"II. Largo"}) {
		t.Errorf("cached album track = %q", got)
	}
	// Retitling an album retitles its tracks' album field.
	apply(t, s, catalog.Snapshot{Collection: "albums", Albums: []catalog.AlbumRecord{
		{Ref: sref("nw"), Title: "New World Symphony"}, {Ref: sref("pl"), Title: "The Planets"}, {Ref: sref("hits-saved"), Title: "Greatest Hits"},
	}})
	if got := search(t, s, "album:symphony largo")[catalog.SearchTrack]; !slices.Equal(got, []string{"II. Largo"}) {
		t.Errorf("after retitle = %q", got)
	}
	if got := search(t, s, `album:"from the"`)[catalog.SearchTrack]; len(got) != 0 {
		t.Errorf("old album title still matches tracks: %q", got)
	}
}

// Hostile or half-typed input never reaches FTS as syntax.
func TestSearchQuotesUserInput(t *testing.T) {
	s := seedSearch(t)
	for _, q := range []string{`"`, `NEAR(a b)`, `a OR b`, `-planets`, `planets*`, `"planets" AND "x`, `^mars`, `col:x`, `{title}:x`, `title:"`} {
		if _, err := s.Search(context.Background(), catalog.ParseQuery(q), 10); err != nil {
			t.Errorf("Search(%q): %v", q, err)
		}
	}
	if got := search(t, s, "planets*")[catalog.SearchPlaylist]; !slices.Equal(got, []string{"Planets for Work"}) {
		t.Errorf("planets* = %q, want the * searched as text", got)
	}
	// Punctuation does not make a one-letter word a prefix: "h!" is the
	// whole word "h", which no title has, not every "h…".
	if got := search(t, s, "h!"); len(got) != 0 {
		t.Errorf("h! = %q, want nothing", got)
	}
}

// Upgrading a v1 catalog indexes what it already holds.
func TestSearchMigrationBackfills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	v1, err := sql.Open("sqlite", path+"?"+dsnParams)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.ReadFile(migrationFS, "migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), v1, fstest.MapFS{"migrations/001_initial.sql": {Data: first}}); err != nil {
		t.Fatal(err)
	}
	// Written as a v1 writer would: today's writer needs later columns.
	if _, err := v1.Exec(`INSERT INTO albums (provider, provider_id, title, sort_title, updated_at)
		VALUES ('spotify', 'a', 'Kind of Blue', 'kind of blue', 0)`); err != nil {
		t.Fatal(err)
	}
	v1.Close()

	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := search(t, s, "kind blue")[catalog.SearchAlbum]; !slices.Equal(got, []string{"Kind of Blue"}) {
		t.Errorf("after upgrade = %q, want the stored album found", got)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	s := seedSearch(t)
	for _, q := range []string{"", "  ", "type:album", "source:local", `""`} {
		res, err := s.Search(context.Background(), catalog.ParseQuery(q), 10)
		if err != nil || len(res) != 0 {
			t.Errorf("Search(%q) = %v, %v; want nothing", q, res, err)
		}
	}
}

// BenchmarkSearch searches a 100k-track catalog. Run with
// go test ./catalog/sqlite -run '^$' -bench Search -benchtime 200x.
func BenchmarkSearch(b *testing.B) {
	s, err := Open(context.Background(), filepath.Join(b.TempDir(), "library.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	words := []string{"symphony", "concerto", "sonata", "quartet", "suite", "overture", "prelude", "nocturne", "etude", "fugue"}
	names := []string{"Holst", "Dvořák", "Mahler", "Ravel", "Respighi", "Sibelius", "Bruckner", "Elgar", "Brahms", "Bartók"}
	start := time.Now()
	for batch := range 20 {
		snap := catalog.Snapshot{Provider: catalog.Local, Collection: fmt.Sprintf("files%d", batch)}
		for i := range 5000 {
			n := batch*5000 + i
			album := catalog.AlbumRecord{Ref: ref(catalog.Local, fmt.Sprintf("al%d", n/10)),
				Title: fmt.Sprintf("%s No. %d", words[n/10%len(words)], n/10), Artists: []catalog.ArtistRecord{{Ref: ref(catalog.Local, names[n%len(names)]), Name: names[n%len(names)]}}}
			snap.Tracks = append(snap.Tracks, catalog.TrackRecord{Ref: ref(catalog.Local, fmt.Sprintf("t%d", n)),
				Title: fmt.Sprintf("%s %s %d", words[n%len(words)], words[(n/7)%len(words)], n), Artists: album.Artists,
				Album: &album, PlayableURI: fmt.Sprintf("/m/%d.flac", n), Genre: "Classical"})
		}
		if err := s.ApplySnapshot(context.Background(), snap); err != nil {
			b.Fatal(err)
		}
	}
	b.Logf("indexed 100k tracks in %v", time.Since(start))
	for _, query := range []string{"s", "sym", "symphony mahler", "dvorak con", "artist:ravel suite", `"string quartet"`, "noct 4242"} {
		b.Run(query, func(b *testing.B) {
			q := catalog.ParseQuery(query)
			for i := 0; i < b.N; i++ {
				if _, err := s.Search(context.Background(), q, 20); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
