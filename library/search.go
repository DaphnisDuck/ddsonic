package library

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// SearchLevel is the search screen: its rows are the results for Query. The
// UI swaps in WithQuery's level as the user types, so a level never changes
// while it loads.
type SearchLevel interface {
	Level
	Query() string
	WithQuery(query string) SearchLevel
}

// Search sections, in the order they are shown, and how many results each
// shows before a "More…" row.
var searchSections = []struct {
	kind    catalog.SearchKind
	heading string
	limit   int
}{
	{catalog.SearchArtist, "Artists", 5},
	{catalog.SearchAlbum, "Albums", 8},
	{catalog.SearchTrack, "Tracks", 20},
	{catalog.SearchPlaylist, "Playlists", 5},
	{catalog.SearchStation, "Stations", 5},
}

// searchFetch is one more than the largest section, so a full section
// knows it has more.
var searchFetch = func() int {
	n := 0
	for _, sec := range searchSections {
		n = max(n, sec.limit)
	}
	return n + 1
}()

const (
	// moreLimit caps a "More…" list.
	moreLimit = 200

	beyondSection = "Beyond your library"
	noMatches     = "No matches in your library"
)

// stationSearcher is the radio provider's directory search.
type stationSearcher interface {
	SearchStationTracks(query string) ([]playlist.Track, error)
}

// catalogView reads the catalog across sources, for search and the Library
// menu, and builds rows with each source's own builders, so a row opens
// exactly as it does while browsing that source.
type catalogView struct {
	cat    catalog.Catalog
	synced map[string]*catalogBrowser // by catalog provider name
	local  *catalogBrowser
	// Beyond the catalog: the providers with their own live search, by
	// name, and the radio directory.
	liveSearch []liveSearcher
	radioProv  playlist.Provider
	// albumOrder is the Library Albums list's order, a catalog.AlbumOrder.
	albumOrder atomic.Int32
}

func newCatalogView(cat catalog.Catalog, src Sources) *catalogView {
	s := &catalogView{cat: cat, synced: map[string]*catalogBrowser{},
		local: localBrowser(cat, src.Local, src.MusicDir), radioProv: src.Radio}
	for _, ss := range src.Synced {
		s.synced[ss.Provider] = syncedBrowser(cat, ss)
		s.addLiveSearch(ss.Title, ss.Player)
	}
	if s.synced[catalog.Spotify] == nil {
		s.addLiveSearch("Spotify", src.Spotify) // browsed live, searched live
	}
	return s
}

// liveSearcher is a provider that searches its own service live.
type liveSearcher struct {
	name string
	prov playlist.Provider
}

func (s *catalogView) addLiveSearch(name string, prov playlist.Provider) {
	if _, ok := prov.(provider.Searcher); ok {
		s.liveSearch = append(s.liveSearch, liveSearcher{name, prov})
	}
}

func (s *catalogView) level(query string) SearchLevel { return &searchLevel{s: s, query: query} }

type searchLevel struct {
	s     *catalogView
	query string
}

// CatalogProvider makes search a catalog level of every source: its results
// come from the catalog alone, so a sync rereads them.
func (l *searchLevel) CatalogProvider() string { return "" }

func (l *searchLevel) Title() string                      { return "Search" }
func (l *searchLevel) Query() string                      { return l.query }
func (l *searchLevel) WithQuery(query string) SearchLevel { return l.s.level(query) }
func (l *searchLevel) Load(ctx context.Context) ([]Entry, error) {
	return l.s.results(ctx, l.query)
}

// results is the search screen for query: each kind's best results under
// its heading, then rows that search beyond the catalog.
func (s *catalogView) results(ctx context.Context, query string) ([]Entry, error) {
	q := catalog.ParseQuery(query)
	if q.Empty() {
		return nil, nil
	}
	var found catalog.SearchResults
	if s.restrict(&q) {
		var err error
		if found, err = s.cat.Search(ctx, q, searchFetch); err != nil {
			return nil, err
		}
	}
	var entries []Entry
	for _, sec := range searchSections {
		rs := found[sec.kind]
		for _, e := range s.rows(rs[:min(len(rs), sec.limit)]) {
			e.Section = sec.heading
			entries = append(entries, e)
		}
		if len(rs) > sec.limit {
			entries = append(entries, Entry{
				ID: "more:" + string(sec.kind), Section: sec.heading,
				Title: "More " + strings.ToLower(sec.heading) + "…", Open: s.more(q, sec.kind, sec.heading),
			})
		}
	}
	if len(entries) == 0 {
		entries = append(entries, Entry{Title: noMatches})
	}
	return append(entries, s.beyond(q)...), nil
}

// more lists up to moreLimit results of one kind.
func (s *catalogView) more(q catalog.Query, kind catalog.SearchKind, heading string) Level {
	q.Kinds = []catalog.SearchKind{kind}
	return &catalogLevel{funcLevel{heading, func(ctx context.Context) ([]Entry, error) {
		q := q // loads of this level can overlap; each restricts its own copy
		if !s.restrict(&q) {
			return nil, nil
		}
		found, err := s.cat.Search(ctx, q, moreLimit)
		if err != nil {
			return nil, err
		}
		return s.rows(found[kind]), nil
	}}, "", nil} // read from the catalog alone, so a sync rereads it
}

// restrict limits q to the configured sources, so results of a source that
// is no longer set up (its rows still in the catalog) neither show nor take
// the places of results that would. An explicit source: keeps only its
// configured sources. It reports false when none is left to search.
func (s *catalogView) restrict(q *catalog.Query) bool {
	var sources []string
	for _, p := range s.sources() {
		if len(q.Providers) == 0 || slices.Contains(q.Providers, p) {
			sources = append(sources, p)
		}
	}
	q.Providers = sources
	return len(sources) > 0
}

// sources are the catalog providers whose rows can open or play: the
// synced ones, Local, and Radio when the radio provider is set up.
func (s *catalogView) sources() []string {
	out := []string{catalog.Local}
	for p := range s.synced {
		out = append(out, p)
	}
	if s.radioProv != nil {
		out = append(out, catalog.Radio)
	}
	slices.Sort(out)
	return out
}

// rows builds the results' rows, skipping those of unconfigured sources.
func (s *catalogView) rows(results []catalog.SearchResult) []Entry {
	var entries []Entry
	for _, r := range results {
		if e, ok := s.entry(r); ok {
			entries = append(entries, e)
		}
	}
	return entries
}

// beyond is the rows that search outside the catalog: each provider's own
// live search (Spotify, YouTube Music in cookie mode) and the radio
// directory, run only when chosen.
func (s *catalogView) beyond(q catalog.Query) []Entry {
	text := q.Text()
	if text == "" {
		return nil
	}
	var entries []Entry
	for _, ls := range s.liveSearch {
		entries = append(entries, Entry{Section: beyondSection, Title: "Search " + ls.name + " for “" + text + "”",
			Intent: IntentSearch, Provider: ls.prov, Query: text})
	}
	if st, ok := s.radioProv.(stationSearcher); ok {
		entries = append(entries, Entry{Section: beyondSection, Title: "Search the radio directory for “" + text + "”",
			Open: directoryStationsLevel("Radio directory", s.radioProv, func() ([]playlist.Track, error) {
				return st.SearchStationTracks(text)
			}), Source: catalog.Radio})
	}
	return entries
}

// entry builds a result's row with its source's builder, labelled with the
// source. ok is false for a result whose source is not configured.
func (s *catalogView) entry(r catalog.SearchResult) (e Entry, ok bool) {
	switch {
	case r.Artist != nil:
		e, ok = s.artistRow(*r.Artist)
	case r.Album != nil:
		e, ok = s.albumRow(*r.Album)
	case r.Playlist != nil && s.synced[r.Playlist.Ref.Provider] != nil:
		e, ok = s.synced[r.Playlist.Ref.Provider].playlistEntry(*r.Playlist), true
		e.Detail = joinDetail(SourceLabel(r.Playlist.Ref.Provider), e.Detail)
		e.Source = r.Playlist.Ref.Provider
	case r.Track != nil:
		e, ok = s.trackRow(*r.Track, r.Kind == catalog.SearchStation), slices.Contains(s.sources(), r.Track.Ref.Provider)
	}
	// Kinds share catalog IDs; the kind keeps a row's ID unique.
	e.ID = string(r.Kind) + ":" + e.ID
	return e, ok
}

// trackRow is a searched track's row, labelled with its source: Enter plays
// its album from it, or plays a station alone as a live stream. The label
// is the row's, not the track's: what plays and what is queued is the track
// as its source holds it.
func (s *catalogView) trackRow(t catalog.Track, station bool) Entry {
	track := PlayableTrack(t)
	e := Entry{ID: catalogID(t.ID), Title: t.Title, Detail: SourceLabel(t.Ref.Provider), Track: &track,
		PlayFrom: s.albumFrom(t), Source: t.Ref.Provider}
	if station {
		track.Stream, track.Realtime = true, true
		track.ProviderMeta = s.starredStationMeta(track.Path)
		e.PlayFrom = func(context.Context) ([]playlist.Track, int, error) {
			return []playlist.Track{track}, 0, nil
		}
	}
	return e
}

// starredStationMeta is the station streaming from url as the favorites hold
// it, or nil when it is not starred. The catalog keeps a station's name and
// stream alone, and its stations are the starred ones and radios.toml's; the
// favorite's own track says which directory station a result is, so it can
// be unstarred from the results and from the queue it plays into.
func (s *catalogView) starredStationMeta(url string) map[string]string {
	fs, ok := s.radioProv.(favoriteStations)
	if !ok {
		return nil
	}
	for _, t := range fs.FavoriteTracks() {
		if t.Path == url {
			return t.ProviderMeta
		}
	}
	return nil
}

// artistRow is an artist's row, built by its source and labelled with it.
// ok is false when the source is not configured.
func (s *catalogView) artistRow(a catalog.Artist) (Entry, bool) {
	var e Entry
	switch b := s.synced[a.Ref.Provider]; {
	case b != nil:
		e = b.artistEntry(a)
	case a.Ref.Provider == catalog.Local:
		e = s.local.localArtistEntry(a)
	default:
		return Entry{}, false
	}
	e.Detail = s.sourceLabel(a.Ref.Provider)
	e.Source = a.Ref.Provider
	return e, true
}

// sourceLabel labels an album's or artist's row with its source. A
// PartialAlbums source's are not saved albums or followed artists, only
// what its tracks name, and the label says so.
func (s *catalogView) sourceLabel(provider string) string {
	label := SourceLabel(provider)
	if b := s.synced[provider]; b != nil && b.partialAlbums {
		label = joinDetail(label, FromTracks)
	}
	return label
}

// albumRow is an album's row, built by its source and labelled with it.
// ok is false when the source is not configured.
func (s *catalogView) albumRow(a catalog.Album) (Entry, bool) {
	var e Entry
	switch b := s.synced[a.Ref.Provider]; {
	case b != nil:
		e = b.albumEntry(a, a.Artist)
	case a.Ref.Provider == catalog.Local:
		e = s.local.localAlbumEntry(a)
	default:
		return Entry{}, false
	}
	// The source comes first so a long credit cannot truncate it away.
	e.Detail = joinDetail(s.sourceLabel(a.Ref.Provider), e.Detail)
	e.Source = a.Ref.Provider
	return e, true
}

// albumFrom plays a searched track's album from that track: the catalog's
// track list, fetched and cached first for an uncached Spotify album. When
// the album cannot be had (offline, no album) it plays just the track.
func (s *catalogView) albumFrom(t catalog.Track) func(ctx context.Context) ([]playlist.Track, int, error) {
	return func(ctx context.Context) ([]playlist.Track, int, error) {
		single := []playlist.Track{PlayableTrack(t)}
		if t.AlbumID == 0 {
			return single, 0, nil
		}
		tracks, cached, err := s.cat.AlbumTracks(ctx, t.AlbumID)
		if err != nil {
			return single, 0, nil
		}
		// An uncached Spotify album holds only the tracks the catalog met
		// elsewhere (liked, in a playlist); playing those as the album would
		// silently skip the rest. A local album is always whole, and a
		// partial source's album is all there is.
		b := s.synced[t.Ref.Provider]
		if !cached && t.Ref.Provider != catalog.Local && (b == nil || !b.partialAlbums) {
			tracks = s.fetchAlbum(ctx, t.AlbumID)
		}
		// The track is found by ref, not ID: if a sync replaced the album
		// and the track since, their old IDs may now be other entities'.
		for i, at := range tracks {
			if at.Ref == t.Ref {
				out := make([]playlist.Track, len(tracks))
				for j, tr := range tracks {
					out[j] = PlayableTrack(tr)
				}
				return out, i, nil
			}
		}
		return single, 0, nil
	}
}

// fetchAlbum fetches and caches an album's tracks, or returns none when it
// cannot (offline, or a catalog without a fetcher).
func (s *catalogView) fetchAlbum(ctx context.Context, albumID int64) []catalog.Track {
	f, ok := s.cat.(catalog.AlbumTrackFetcher)
	if !ok {
		return nil
	}
	album, err := s.cat.Album(ctx, albumID)
	if err != nil {
		return nil
	}
	tracks, err := f.FetchAlbumTracks(ctx, album)
	if err != nil {
		return nil
	}
	return tracks
}

// sourceLabels spells the providers whose names do not simply capitalize.
var sourceLabels = map[string]string{"youtube": "YouTube"}

// SourceLabel names a catalog provider for display: "spotify" → "Spotify",
// "youtube" → "YouTube".
func SourceLabel(provider string) string {
	if label, ok := sourceLabels[provider]; ok {
		return label
	}
	if provider == "" {
		return ""
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}

func joinDetail(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}
