package library

import (
	"context"
	"fmt"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// LocalCatalog returns the Local menu. Albums, Artists and Genres come from
// the catalog's index of dir, so they open instantly; Folders opens the file
// browser; Playlists are the local provider's saved playlists. Every catalog
// level reloads when an index of the folder lands.
func LocalCatalog(cat catalog.Catalog, prov playlist.Provider, dir string) Level {
	b := localBrowser(cat, prov, dir)
	return local(prov,
		Entry{Title: "Albums", Open: b.albumsList("Albums", nil, b.localAlbums)},
		Entry{Title: "Artists", Open: b.list("Artists", b.localArtists)},
		Entry{Title: "Genres", Open: b.list("Genres", b.localGenres)},
	)
}

func localBrowser(cat catalog.Catalog, prov playlist.Provider, dir string) *catalogBrowser {
	return &catalogBrowser{cat: cat, prov: prov, provider: catalog.Local,
		pending: "Indexing your music… (press r to retry if this persists)",
		none:    "No music found in " + dir}
}

func (b *catalogBrowser) localAlbums(ctx context.Context) ([]Entry, error) {
	albums, err := b.cat.Albums(ctx, b.provider, catalog.AlbumOrder(b.albumOrder.Load()))
	if err != nil {
		return nil, err
	}
	return b.syncingPlaceholder(ctx, b.localAlbumEntries(albums))
}

func (b *catalogBrowser) localAlbumEntries(albums []catalog.Album) []Entry {
	entries := make([]Entry, len(albums))
	for i, a := range albums {
		entries[i] = b.localAlbumEntry(a)
	}
	return entries
}

func (b *catalogBrowser) localAlbumEntry(a catalog.Album) Entry {
	detail := a.Artist
	if a.Year > 0 {
		detail = fmt.Sprintf("%s · %d", detail, a.Year)
	}
	return Entry{ID: catalogID(a.ID), Title: a.Title, Detail: detail, Open: b.list(a.Title, func(ctx context.Context) ([]Entry, error) {
		tracks, _, err := b.cat.AlbumTracksByRef(ctx, a.Ref)
		if err != nil {
			return nil, notInLibrary(err)
		}
		return catalogTrackEntries(tracks), nil
	})}
}

func (b *catalogBrowser) localArtists(ctx context.Context) ([]Entry, error) {
	artists, err := b.cat.Artists(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(artists))
	for i, a := range artists {
		entries[i] = b.localArtistEntry(a)
	}
	return b.syncingPlaceholder(ctx, entries)
}

func (b *catalogBrowser) localArtistEntry(a catalog.Artist) Entry {
	return Entry{ID: catalogID(a.ID), Title: a.Name, Open: b.list(a.Name, func(ctx context.Context) ([]Entry, error) {
		albums, err := b.cat.ArtistAlbumsByRef(ctx, a.Ref)
		if err != nil {
			return nil, notInLibrary(err)
		}
		return b.localAlbumEntries(albums), nil
	})}
}

func (b *catalogBrowser) localGenres(ctx context.Context) ([]Entry, error) {
	genres, err := b.cat.Genres(ctx, b.provider)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, len(genres))
	for i, g := range genres {
		entries[i] = Entry{ID: g.Name, Title: g.Name, Detail: albumCount(g.AlbumCount), Open: b.list(g.Name, func(ctx context.Context) ([]Entry, error) {
			albums, err := b.cat.GenreAlbums(ctx, b.provider, g.Name)
			if err != nil {
				return nil, err
			}
			return b.localAlbumEntries(albums), nil
		})}
	}
	return b.syncingPlaceholder(ctx, entries)
}

func albumCount(n int) string {
	if n == 1 {
		return "1 album"
	}
	return fmt.Sprintf("%d albums", n)
}
