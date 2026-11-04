package library

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// catalogLevel is a top-level list read from the catalog, so it loads
// instantly and offline. The UI reloads it when a sync of its provider
// lands. Levels beneath it (album tracks, discographies) are ordinary levels:
// some read the network, and a sync must not re-run those.
type catalogLevel struct {
	funcLevel
	provider string
	note     []string // what the list holds, when its title could mislead
}

func (l *catalogLevel) CatalogProvider() string { return l.provider }

// Note implements NotedLevel; most lists have none.
func (l *catalogLevel) Note() []string { return l.note }

// catalogAlbumsLevel is a catalog Albums list, sorted by title or by artist. The
// order is the browser's, so it holds while the list is reopened.
type catalogAlbumsLevel struct {
	catalogLevel
	order *atomic.Int32 // a catalog.AlbumOrder
}

// The lists of a source with ListsFromTracks are not "Albums" and "Artists":
// its saved albums and followed artists are not synced, and the lists must
// not read as if they were.
const (
	AlbumsFromTracks  = "Albums from tracks"
	ArtistsFromTracks = "Artists from tracks"
	// FromTracks labels such a source's album and artist rows where
	// sources mix (All Music, search), after the source's name.
	FromTracks = "from tracks"
)

// tracksList describes a list a ListsFromTracks source builds from its
// tracks: its title, what the account has that it is not, and its row when
// empty.
type tracksList struct {
	title     string
	notSynced string // has one %s, the source's title
	caveat    string // ends the note's second sentence, or ""
	none      string
}

var (
	albumsFromTracks = tracksList{AlbumsFromTracks, "Saved %s Library Albums are not synchronized.",
		"; albums may be incomplete", "No albums found in your tracks yet"}
	artistsFromTracks = tracksList{ArtistsFromTracks, "Followed %s artists are not synchronized.",
		"", "No artists found in your tracks yet"}
)

// note says what the list holds, a sentence a line; the UI wraps them to
// its panel.
func (l tracksList) note(source, liked string) []string {
	return []string{
		fmt.Sprintf(l.notSynced, source),
		"Grouped from " + liked + " and synced playlists" + l.caveat + ".",
	}
}

var albumOrderNames = map[catalog.AlbumOrder]string{catalog.ByTitle: "by title", catalog.ByArtist: "by artist"}

func (l *catalogAlbumsLevel) OrderName() string {
	return albumOrderNames[catalog.AlbumOrder(l.order.Load())]
}

func (l *catalogAlbumsLevel) NextOrder() string {
	next := catalog.ByArtist
	if catalog.AlbumOrder(l.order.Load()) == catalog.ByArtist {
		next = catalog.ByTitle
	}
	l.order.Store(int32(next))
	return albumOrderNames[next]
}

// SyncedSource is a provider the catalog syncs, as its menu needs it.
// Collections are what its sync provides (catalog.Collection*): the menu
// offers a list for each, so a source that has no albums shows none.
type SyncedSource struct {
	Provider    string // catalog provider name, e.g. catalog.Spotify
	Title       string // menu title, e.g. "Spotify"
	Collections []string
	// Player plays the source's tracks and fills gaps the catalog cannot:
	// an album whose tracks are not cached (when the catalog is no
	// catalog.AlbumTrackFetcher), a playlist whose items could not be
	// synced, and an artist's full discography, which only its live API has.
	Player playlist.Provider
	// LikedTitle names the liked list; "Liked Songs" when empty.
	LikedTitle string
	// ListsFromTracks marks a source whose album and artist lists are built
	// from the tracks the catalog has (YouTube's, derived from enriched
	// tracks): an album holds only those tracks and opens and plays as it
	// is, never fetched whole, and its artists are derived the same way. Its lists are titled AlbumsFromTracks and ArtistsFromTracks
	// and carry a note saying so, and its album and artist rows are
	// labelled FromTracks where sources mix.
	ListsFromTracks bool
}

// SyncedMenu returns a synced source's menu, backed by the catalog. It
// offers, in this order, Albums, Artists, Playlists and the liked list, each
// only if the source syncs that collection.
func SyncedMenu(cat catalog.Catalog, src SyncedSource) Level {
	b := syncedBrowser(cat, src)
	liked := cmp.Or(src.LikedTitle, "Liked Songs")
	albums, artists := tracksList{title: "Albums"}, tracksList{title: "Artists"}
	var albumsNote, artistsNote []string
	if src.ListsFromTracks {
		albums, artists = albumsFromTracks, artistsFromTracks
		albumsNote, artistsNote = albums.note(src.Title, liked), artists.note(src.Title, liked)
	}
	lists := []struct {
		collection string
		entry      Entry
	}{
		{catalog.CollectionAlbums, Entry{Title: albums.title, Open: b.albumsList(albums.title, albumsNote, b.albums)}},
		{catalog.CollectionArtists, Entry{Title: artists.title, Open: b.notedList(artists.title, artistsNote, b.artists)}},
		{catalog.CollectionPlaylists, Entry{Title: "Playlists", Open: b.list("Playlists", b.playlists)}},
		{catalog.CollectionLiked, Entry{Title: liked, Open: b.list(liked, b.liked)}},
	}
	var entries []Entry
	for _, l := range lists {
		if slices.Contains(src.Collections, l.collection) {
			entries = append(entries, l.entry)
		}
	}
	return &sourceMenu{Menu(src.Title, entries...), src.Provider}
}

// sourceMenu is a synced source's menu (SourceMenu).
type sourceMenu struct {
	Level
	provider string
}

func (m *sourceMenu) SourceProvider() string { return m.provider }

func syncedBrowser(cat catalog.Catalog, src SyncedSource) *catalogBrowser {
	return &catalogBrowser{cat: cat, prov: src.Player, provider: src.Provider, listsFromTracks: src.ListsFromTracks,
		pending: "Syncing your library… (press r to retry if this persists)"}
}

// catalogBrowser builds one provider's catalog-backed levels and entries,
// for its browse menu and for search results.
type catalogBrowser struct {
	cat      catalog.Catalog
	prov     playlist.Provider
	provider string
	pending  string // shown in an empty list before the first sync
	none     string // shown in an empty list after it; "" shows nothing
	// albumOrder is the Albums list's order, a catalog.AlbumOrder.
	albumOrder atomic.Int32
	// listsFromTracks: albums and artists come from the catalog's tracks (SyncedSource).
	listsFromTracks bool
}

// albumsList is the albums list, which the UI can reorder.
func (b *catalogBrowser) albumsList(title string, note []string, load func(ctx context.Context) ([]Entry, error)) Level {
	return &catalogAlbumsLevel{catalogLevel{funcLevel{title, load}, b.provider, note}, &b.albumOrder}
}

// list is a top-level catalog list, refreshed after syncs.
func (b *catalogBrowser) list(title string, load func(ctx context.Context) ([]Entry, error)) Level {
	return b.notedList(title, nil, load)
}

// notedList is a list with a note saying what it holds.
func (b *catalogBrowser) notedList(title string, note []string, load func(ctx context.Context) ([]Entry, error)) Level {
	return &catalogLevel{funcLevel{title, load}, b.provider, note}
}

// level is a level beneath the lists. It may fall back to the provider, so
// it offers sign-in and is not reloaded by syncs.
func (b *catalogBrowser) level(title string, load func(ctx context.Context) ([]Entry, error)) Level {
	return providerLevel(title, b.prov, load)
}

// cachedLevel is a level beneath the lists that a sync rereads from the
// catalog alone (library.CachedLevel); like level, it may fall back to the
// provider and offers sign-in.
func (b *catalogBrowser) cachedLevel(title string, load func(ctx context.Context) ([]Entry, error),
	cached func(ctx context.Context) ([]Entry, bool, error)) Level {
	l := cachedFuncLevel{funcLevel{title, load}, b.provider, cached}
	if a, ok := b.prov.(playlist.Authenticator); ok {
		return &cachedSignInLevel{l, a}
	}
	return &l
}

type cachedFuncLevel struct {
	funcLevel
	provider string
	cached   func(ctx context.Context) ([]Entry, bool, error)
}

func (l *cachedFuncLevel) CachedProvider() string { return l.provider }
func (l *cachedFuncLevel) LoadCached(ctx context.Context) ([]Entry, bool, error) {
	return l.cached(ctx)
}

type cachedSignInLevel struct {
	cachedFuncLevel
	auth playlist.Authenticator
}

func (l *cachedSignInLevel) Authenticator() playlist.Authenticator { return l.auth }

// cachedRows turns a catalog reread's error into its result: an entity a
// sync removed reports so in place of its rows; any other failure leaves the
// rows shown.
func cachedRows(err error) ([]Entry, bool, error) {
	if errors.Is(err, catalog.ErrNotFound) {
		return nil, true, notInLibrary(err)
	}
	return nil, false, nil
}

// syncingPlaceholder stands in for an empty list: before the first sync has
// finished, so an empty cache reads as "coming" rather than "you have none",
// and after it when the browser names what is missing.
func (b *catalogBrowser) syncingPlaceholder(ctx context.Context, entries []Entry) ([]Entry, error) {
	if len(entries) > 0 {
		return entries, nil
	}
	status, err := b.cat.SyncStatus(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	for _, st := range status {
		if !st.LastSuccess.IsZero() {
			if b.none != "" {
				return []Entry{{Title: b.none}}, nil
			}
			return entries, nil
		}
	}
	return []Entry{{Title: b.pending, Intent: IntentPending, Source: b.provider}}, nil
}

func (b *catalogBrowser) albums(ctx context.Context) ([]Entry, error) {
	albums, err := b.cat.Albums(ctx, b.provider, catalog.AlbumOrder(b.albumOrder.Load()))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(albums))
	for i, a := range albums {
		entries[i] = b.albumEntry(a, a.Artist)
	}
	return b.fromTracksPlaceholder(ctx, entries, albumsFromTracks)
}

// fromTracksPlaceholder is syncingPlaceholder for a list that a
// ListsFromTracks source builds from its tracks: synced and still empty, it
// says that no track read so far has what the list groups by.
func (b *catalogBrowser) fromTracksPlaceholder(ctx context.Context, entries []Entry, list tracksList) ([]Entry, error) {
	entries, err := b.syncingPlaceholder(ctx, entries)
	if err == nil && len(entries) == 0 && b.listsFromTracks {
		entries = []Entry{{Title: list.none}}
	}
	return entries, err
}

func (b *catalogBrowser) albumEntry(a catalog.Album, detail string) Entry {
	cached := func(ctx context.Context) ([]Entry, bool, error) {
		tracks, cached, err := b.cat.AlbumTracksByRef(ctx, a.Ref)
		if err != nil {
			return cachedRows(err)
		}
		if !cached && !b.listsFromTracks {
			return nil, false, nil // the rows were fetched live
		}
		return catalogTrackEntries(tracks), true, nil
	}
	return Entry{ID: catalogID(a.ID), Title: a.Title, Detail: detail, Open: b.cachedLevel(a.Title, func(ctx context.Context) ([]Entry, error) {
		tracks, cached, err := b.cat.AlbumTracksByRef(ctx, a.Ref)
		if err != nil {
			return nil, notInLibrary(err)
		}
		if cached || b.listsFromTracks {
			return catalogTrackEntries(tracks), nil
		}
		// Not cached yet: fetch and cache it, or at least show it live.
		if f, ok := b.cat.(catalog.AlbumTrackFetcher); ok {
			fetched, err := f.FetchAlbumTracks(ctx, a)
			if err != nil {
				return nil, err
			}
			return catalogTrackEntries(fetched), nil
		}
		loader, ok := b.prov.(provider.AlbumTrackLoader)
		if !ok {
			return catalogTrackEntries(tracks), nil
		}
		live, err := loader.AlbumTracks(a.Ref.ProviderID)
		if err != nil {
			return nil, err
		}
		return trackEntries(live), nil
	}, cached)}
}

func (b *catalogBrowser) artists(ctx context.Context) ([]Entry, error) {
	artists, err := b.cat.Artists(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(artists))
	for i, a := range artists {
		entries[i] = b.artistEntry(a)
	}
	return b.fromTracksPlaceholder(ctx, entries, artistsFromTracks)
}

func (b *catalogBrowser) artistEntry(a catalog.Artist) Entry {
	ab, live := b.prov.(provider.ArtistBrowser)
	// The page is the albums the catalog knows, at once, and for a provider
	// with a live discography a row that loads it when chosen. When the
	// catalog has none of the artist's albums the page is the provider's
	// list itself (fromProvider), which a sync leaves alone.
	var fromProvider atomic.Bool
	page := func(albums []catalog.Album) []Entry {
		entries := b.catalogArtistAlbums(albums)
		if live {
			entries = append(entries, b.discographyRow(ab, a))
		}
		return entries
	}
	load := func(ctx context.Context) ([]Entry, error) {
		albums, err := b.cat.ArtistAlbumsByRef(ctx, a.Ref)
		if live && (errors.Is(err, catalog.ErrNotFound) || err == nil && len(albums) == 0) {
			fromProvider.Store(true)
			return liveDiscography(b.prov, ab, a)
		}
		if err != nil {
			return nil, notInLibrary(err)
		}
		fromProvider.Store(false)
		return page(albums), nil
	}
	cached := func(ctx context.Context) ([]Entry, bool, error) {
		// The provider's full list stays, even once the catalog has some
		// of the albums: reopening the page shows the catalog first.
		if fromProvider.Load() {
			return nil, false, nil
		}
		albums, err := b.cat.ArtistAlbumsByRef(ctx, a.Ref)
		if err != nil {
			return cachedRows(err)
		}
		return page(albums), true, nil
	}
	return Entry{ID: catalogID(a.ID), Title: a.Name, Open: b.cachedLevel(a.Name, load, cached)}
}

// discographyRow opens the artist's full discography, loaded from the
// provider when chosen.
func (b *catalogBrowser) discographyRow(ab provider.ArtistBrowser, a catalog.Artist) Entry {
	return Entry{ID: "discography:" + a.Ref.ProviderID, Title: "Full discography…",
		Open: b.level("Full discography", func(context.Context) ([]Entry, error) {
			return liveDiscography(b.prov, ab, a)
		})}
}

// liveDiscography is the artist's albums and singles as the provider lists
// them.
func liveDiscography(prov playlist.Provider, ab provider.ArtistBrowser, a catalog.Artist) ([]Entry, error) {
	albums, err := ab.ArtistAlbums(a.Ref.ProviderID)
	if err != nil {
		return nil, err
	}
	return artistAlbumEntries(prov, albums), nil
}

// notInLibrary explains an entity a listed action can no longer find. The
// actions read their entity by ref, never by the ID it was listed with: a
// sync may have removed it since, and SQLite may have given its old ID to
// another entity, which the action must not open in its place.
func notInLibrary(err error) error {
	if errors.Is(err, catalog.ErrNotFound) {
		return fmt.Errorf("no longer in the library: %w", err)
	}
	return err
}

func (b *catalogBrowser) catalogArtistAlbums(albums []catalog.Album) []Entry {
	entries := make([]Entry, len(albums))
	for i, al := range albums {
		entries[i] = b.albumEntry(al, yearDetail(al.Year))
	}
	return entries
}

func (b *catalogBrowser) playlists(ctx context.Context) ([]Entry, error) {
	pls, err := b.cat.Playlists(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(pls))
	for i, p := range pls {
		entries[i] = b.playlistEntry(p)
	}
	return b.syncingPlaceholder(ctx, entries)
}

func (b *catalogBrowser) playlistEntry(p catalog.Playlist) Entry {
	section := SpotifyFollowedPlaylistsSection
	if p.Own {
		section = SpotifyOwnPlaylistsSection
	}
	// fetchedLive records whether the rows shown came from the provider: an
	// empty catalog list then means the sync could not read the playlist,
	// and the live rows stay; otherwise empty is the playlist now.
	var fetchedLive atomic.Bool
	cached := func(ctx context.Context) ([]Entry, bool, error) {
		tracks, err := b.cat.PlaylistTracksByRef(ctx, p.Ref)
		if err != nil {
			return cachedRows(err)
		}
		if len(tracks) == 0 && fetchedLive.Load() {
			return nil, false, nil
		}
		fetchedLive.Store(false)
		return catalogTrackEntries(tracks), true, nil
	}
	e := Entry{ID: catalogID(p.ID), Title: p.Name, Section: section, Open: b.cachedLevel(p.Name, func(ctx context.Context) ([]Entry, error) {
		tracks, err := b.cat.PlaylistTracksByRef(ctx, p.Ref)
		if err != nil {
			return nil, notInLibrary(err)
		}
		// Items the sync could not read (Spotify refuses some followed
		// playlists) are fetched live instead.
		if len(tracks) == 0 && p.TrackCount > 0 {
			live, err := b.prov.Tracks(p.Ref.ProviderID)
			if err != nil {
				return nil, err
			}
			fetchedLive.Store(true)
			return trackEntries(live), nil
		}
		fetchedLive.Store(false)
		return catalogTrackEntries(tracks), nil
	}, cached)}
	if p.TrackCount > 0 {
		e.Detail = fmt.Sprintf("%d tracks", p.TrackCount)
	}
	return e
}

func (b *catalogBrowser) liked(ctx context.Context) ([]Entry, error) {
	tracks, err := b.cat.LikedTracks(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	return b.syncingPlaceholder(ctx, catalogTrackEntries(tracks))
}

func catalogID(id int64) string { return strconv.FormatInt(id, 10) }

// PlayableTrack converts a catalog track into what the player plays. The
// path is the provider's playable URI, so playback routes through the
// provider exactly as it does for live browsing.
func PlayableTrack(t catalog.Track) playlist.Track {
	return playlist.Track{
		Path:         t.PlayableURI,
		Title:        CleanText(t.Title),
		Artist:       CleanText(t.Artist),
		Album:        CleanText(t.AlbumTitle),
		AlbumArtURL:  t.ArtworkURL,
		Year:         t.Year,
		Genre:        CleanText(t.Genre),
		TrackNumber:  t.TrackNo,
		DurationSecs: int(t.Duration.Seconds()),
	}
}

func catalogTrackEntries(tracks []catalog.Track) []Entry {
	out := make([]playlist.Track, len(tracks))
	for i, t := range tracks {
		out[i] = PlayableTrack(t)
	}
	entries := trackEntries(out)
	for i, t := range tracks {
		entries[i].ID = catalogID(t.ID)
	}
	return entries
}
