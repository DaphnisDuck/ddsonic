package library

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// Sources are the providers the Music root is built from. A nil provider hides
// its source.
type Sources struct {
	Spotify playlist.Provider
	Local   playlist.Provider
	Radio   playlist.Provider
	// Channels is the built-in "cliamp radio" channel list, shown under
	// Radio → Browse Stations.
	Channels playlist.Provider
	// MusicDir is the folder the catalog indexes for Local's Albums,
	// Artists and Genres.
	MusicDir string
	// Catalog, when set, backs Spotify browsing with the synced catalog
	// instead of live provider calls, and Local's Albums, Artists and
	// Genres with its index of MusicDir. Without it Local has only Folders
	// and Playlists.
	Catalog catalog.Catalog
	// Synced are the providers the catalog syncs, each with its own menu
	// built from what its sync provides. They need Catalog.
	Synced []SyncedSource
}

// Root returns the top of the hierarchy: Music.
func Root(src Sources) Level {
	var entries []Entry
	var search *catalogView
	if src.Catalog != nil {
		search = newCatalogView(src.Catalog, src)
		entries = append(entries, Entry{Title: "All Music", Open: search.libraryMenu()})
	}
	synced := map[string]bool{}
	if src.Catalog != nil {
		for _, s := range src.Synced {
			entries = append(entries, Entry{Title: s.Title, Open: SyncedMenu(src.Catalog, s), Source: s.Provider})
			synced[s.Provider] = true
		}
	}
	// Without a catalog sync, Spotify browses live.
	if src.Spotify != nil && !synced[catalog.Spotify] {
		entries = append(entries, Entry{Title: "Spotify", Open: Spotify(src.Spotify), Source: catalog.Spotify})
	}
	if src.Catalog != nil && src.MusicDir != "" {
		entries = append(entries, Entry{Title: "Local", Open: LocalCatalog(src.Catalog, src.Local, src.MusicDir), Source: catalog.Local})
	} else if src.Local != nil {
		entries = append(entries, Entry{Title: "Local", Open: local(src.Local), Source: catalog.Local})
	}
	if src.Radio != nil || src.Channels != nil {
		entries = append(entries, Entry{Title: "Radio", Open: radio(src.Radio, src.Channels), Source: catalog.Radio})
	}
	// With a catalog, Search is the unified search screen; without, the
	// provider's own search.
	searchEntry := Entry{Title: "Search", Intent: IntentSearch, Provider: src.Spotify}
	if search != nil {
		searchEntry = Entry{Title: "Search", Open: search.level("")}
	} else if searchEntry.Provider == nil {
		searchEntry.Provider = src.Local
	}
	entries = append(entries, searchEntry)
	return Menu("Music", entries...)
}

// — Spotify —

// The Spotify provider folds Liked Songs and playlists into one Playlists()
// result distinguished by section. These values mirror
// external/spotify/provider_shared.go and are pinned by a test there.
const (
	SpotifyLikedSongsID             = "YOUR MUSIC"
	SpotifyOwnPlaylistsSection      = "Your playlists"
	SpotifyFollowedPlaylistsSection = "Followed playlists"
)

// Spotify returns the Spotify source menu. Albums and Artists appear when the
// provider advertises them.
func Spotify(prov playlist.Provider) Level {
	var entries []Entry
	if ab, ok := prov.(provider.AlbumBrowser); ok {
		entries = append(entries, Entry{Title: "Albums", Open: albumsLevel(prov, ab)})
	}
	if ab, ok := prov.(provider.ArtistBrowser); ok {
		entries = append(entries, Entry{Title: "Artists", Open: artistsLevel(prov, ab)})
	}
	entries = append(entries,
		Entry{Title: "Playlists", Open: playlistsLevel("Playlists", prov, func(info playlist.PlaylistInfo) bool {
			return info.Section == SpotifyOwnPlaylistsSection || info.Section == SpotifyFollowedPlaylistsSection
		}, playlistEntry(prov))},
		Entry{Title: "Liked Songs", Open: TrackLevel("Liked Songs", prov, func(context.Context) ([]playlist.Track, error) {
			return prov.Tracks(SpotifyLikedSongsID)
		})},
	)
	return Menu("Spotify", entries...)
}

// playlistsLevel lists prov's playlists that keep accepts (all when keep is
// nil), each turned into a row by toEntry.
func playlistsLevel(title string, prov playlist.Provider, keep func(playlist.PlaylistInfo) bool, toEntry func(playlist.PlaylistInfo) Entry) Level {
	return providerLevel(title, prov, func(context.Context) ([]Entry, error) {
		lists, err := prov.Playlists()
		if err != nil {
			return nil, err
		}
		var entries []Entry
		for _, info := range lists {
			if keep == nil || keep(info) {
				entries = append(entries, toEntry(info))
			}
		}
		return entries, nil
	})
}

// playlistEntry makes a playlist row that opens its tracks.
func playlistEntry(prov playlist.Provider) func(playlist.PlaylistInfo) Entry {
	return func(info playlist.PlaylistInfo) Entry {
		e := Entry{Title: info.Name, Section: info.Section, Open: TrackLevel(info.Name, prov, func(context.Context) ([]playlist.Track, error) {
			return prov.Tracks(info.ID)
		})}
		if info.TrackCount > 0 {
			e.Detail = fmt.Sprintf("%d tracks", info.TrackCount)
		}
		return e
	}
}

// albumPageSize is the page AlbumList is asked for; a shorter page ends it.
const albumPageSize = 50

// albumsLevel lists every album an AlbumBrowser has, by artist then title.
func albumsLevel(prov playlist.Provider, ab provider.AlbumBrowser) Level {
	return providerLevel("Albums", prov, func(ctx context.Context) ([]Entry, error) {
		var albums []provider.AlbumInfo
		// Page until an empty page, advancing by what came back: a provider
		// may clamp the page below albumPageSize.
		for {
			page, err := ab.AlbumList(ab.DefaultAlbumSort(), len(albums), albumPageSize)
			if err != nil {
				return nil, err
			}
			albums = append(albums, page...)
			if len(page) == 0 || ctx.Err() != nil {
				break
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		slices.SortStableFunc(albums, func(x, y provider.AlbumInfo) int {
			return cmp.Or(
				cmp.Compare(strings.ToLower(x.Artist), strings.ToLower(y.Artist)),
				cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)),
			)
		})
		entries := make([]Entry, len(albums))
		for i, a := range albums {
			entries[i] = albumEntry(prov, a, a.Artist)
		}
		return entries, nil
	})
}

// albumEntry makes an album row that opens its tracks when the provider can
// load them.
func albumEntry(prov playlist.Provider, a provider.AlbumInfo, detail string) Entry {
	e := Entry{Title: a.Name, Detail: detail}
	if loader, ok := prov.(provider.AlbumTrackLoader); ok {
		e.Open = TrackLevel(a.Name, prov, func(context.Context) ([]playlist.Track, error) {
			return loader.AlbumTracks(a.ID)
		})
	}
	return e
}

// artistsLevel lists an ArtistBrowser's artists; each opens its albums.
func artistsLevel(prov playlist.Provider, ab provider.ArtistBrowser) Level {
	return providerLevel("Artists", prov, func(context.Context) ([]Entry, error) {
		artists, err := ab.Artists()
		if err != nil {
			return nil, err
		}
		entries := make([]Entry, len(artists))
		for i, a := range artists {
			entries[i] = Entry{Title: a.Name, Open: artistAlbumsLevel(prov, ab, a)}
			if a.AlbumCount > 0 {
				entries[i].Detail = fmt.Sprintf("%d albums", a.AlbumCount)
			}
		}
		return entries, nil
	})
}

func artistAlbumsLevel(prov playlist.Provider, ab provider.ArtistBrowser, artist provider.ArtistInfo) Level {
	return providerLevel(artist.Name, prov, func(context.Context) ([]Entry, error) {
		albums, err := ab.ArtistAlbums(artist.ID)
		if err != nil {
			return nil, err
		}
		return artistAlbumEntries(prov, albums), nil
	})
}

// artistAlbumEntries lists a discography with release years as detail.
func artistAlbumEntries(prov playlist.Provider, albums []provider.AlbumInfo) []Entry {
	entries := make([]Entry, len(albums))
	for i, a := range albums {
		entries[i] = albumEntry(prov, a, yearDetail(a.Year))
	}
	return entries
}

// yearDetail shows a release year, or nothing when it is unknown.
func yearDetail(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// — Radio —

// favoriteStations is implemented by radio providers that keep favorites.
type favoriteStations interface {
	FavoriteTracks() []playlist.Track
}

// StarredStationsLevel is implemented by the list of the starred stations,
// read from the radio provider's favorites. The UI rereads it when a station
// is starred or unstarred, and when radio's catalog sync, which follows
// every such change, has run.
type StarredStationsLevel interface {
	StarredStations()
}

type starredStationsLevel struct{ funcLevel }

func (*starredStationsLevel) StarredStations() {}

// radio returns the Radio source menu: Favorites and Browse Stations.
func radio(prov, channels playlist.Provider) Level {
	var entries []Entry
	if fs, ok := prov.(favoriteStations); ok {
		entries = append(entries, Entry{Title: "Favorites", Open: &starredStationsLevel{funcLevel{"Favorites", func(context.Context) ([]Entry, error) {
			return trackEntries(sortedByTitle(fs.FavoriteTracks())), nil
		}}}})
	}
	entries = append(entries, Entry{Title: "Browse Stations", Open: browseStations(prov, channels)})
	return Menu("Radio", entries...)
}

func browseStations(prov, channels playlist.Provider) Level {
	var entries []Entry
	if channels != nil {
		entries = append(entries, Entry{Title: channels.Name(), Open: playlistsLevel(channels.Name(), channels, nil, func(info playlist.PlaylistInfo) Entry {
			return Entry{Title: info.Name, Play: func(context.Context) ([]playlist.Track, error) {
				return channels.Tracks(info.ID)
			}}
		})})
	}
	// The provider advertises its category routes (countries, tags) as
	// browse entries; each resolves to its own GenreBrowser.
	router, _ := prov.(provider.GenreBrowseRouter)
	if bep, ok := prov.(provider.BrowseEntryProvider); ok && router != nil {
		for _, be := range bep.BrowseEntries() {
			if be.Mode != provider.BrowseGenres {
				continue
			}
			if gb := router.GenreBrowserFor(be.ID); gb != nil {
				title := be.Name
				if gl, ok := gb.(provider.GenreLabeler); ok {
					title = gl.GenreLabel()
				}
				entries = append(entries, Entry{Title: title, Open: genresLevel(prov, gb, title)})
			}
		}
	}
	return Menu("Browse Stations", entries...)
}

// genresLevel lists a GenreBrowser's categories; each opens its stations.
func genresLevel(prov playlist.Provider, gb provider.GenreBrowser, title string) Level {
	sort := ""
	if types := gb.GenreSortTypes(); len(types) > 0 {
		sort = types[0].ID
	}
	return providerLevel(title, prov, func(context.Context) ([]Entry, error) {
		genres, err := gb.Genres()
		if err != nil {
			return nil, err
		}
		entries := make([]Entry, len(genres))
		for i, g := range genres {
			entries[i] = Entry{Title: g.Name, Section: g.Group, Favorite: g.Favorite, Open: TrackLevel(g.Name, prov, func(context.Context) ([]playlist.Track, error) {
				return gb.GenreTracks(g.ID, sort)
			})}
		}
		return groupBySection(entries), nil
	})
}

// groupBySection gathers rows under one heading per section, sections in
// order of first appearance, keeping row order within a section. Directory
// categories arrive sorted by size, which interleaves their groups.
func groupBySection(entries []Entry) []Entry {
	var order []string
	bySection := map[string][]Entry{}
	for _, e := range entries {
		if _, ok := bySection[e.Section]; !ok {
			order = append(order, e.Section)
		}
		bySection[e.Section] = append(bySection[e.Section], e)
	}
	out := make([]Entry, 0, len(entries))
	for _, s := range order {
		out = append(out, bySection[s]...)
	}
	return out
}

// — Local —

// local returns the Local source menu: the given catalog levels, then
// Folders, which opens the file browser, and the local provider's saved
// playlists.
func local(prov playlist.Provider, indexed ...Entry) Level {
	entries := append([]Entry{}, indexed...)
	entries = append(entries, Entry{Title: "Folders", Intent: IntentFolders, Provider: prov})
	if prov != nil {
		entries = append(entries, Entry{Title: "Playlists", Open: playlistsLevel("Playlists", prov, nil, playlistEntry(prov))})
	}
	return Menu("Local", entries...)
}

// sortedByTitle sorts tracks A–Z by title, the way catalog lists sort.
func sortedByTitle(tracks []playlist.Track) []playlist.Track {
	slices.SortStableFunc(tracks, func(a, b playlist.Track) int {
		return strings.Compare(catalog.SortKey(a.Title), catalog.SortKey(b.Title))
	})
	return tracks
}
