package library

import (
	"context"

	"github.com/bjarneo/cliamp/catalog"
)

// emptyLibrary stands in for an empty unified list.
const emptyLibrary = "Your library is empty. Press r to sync."

// libraryMenu is Music → All Music: every source's albums and artists in one
// list each. Rows are labelled with their source and open that source's own
// level; the same album or artist in two sources stays two rows, sorted
// side by side. Its lists reload after any source syncs.
func (s *catalogView) libraryMenu() Level {
	return Menu("All Music",
		Entry{Title: "Albums", Open: &catalogAlbumsLevel{catalogLevel{funcLevel{"Albums", s.allAlbums}, anyProvider, nil}, &s.albumOrder}},
		Entry{Title: "Artists", Open: &catalogLevel{funcLevel{"Artists", s.allArtists}, anyProvider, nil}},
	)
}

// anyProvider is the CatalogProvider of a level that lists every source.
const anyProvider = ""

func (s *catalogView) allAlbums(ctx context.Context) ([]Entry, error) {
	albums, err := s.cat.Albums(ctx, anyProvider, catalog.AlbumOrder(s.albumOrder.Load()))
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, a := range albums {
		if e, ok := s.albumRow(a); ok {
			entries = append(entries, e)
		}
	}
	return orEmpty(entries), nil
}

func (s *catalogView) allArtists(ctx context.Context) ([]Entry, error) {
	artists, err := s.cat.Artists(ctx, anyProvider)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, a := range artists {
		if e, ok := s.artistRow(a); ok {
			entries = append(entries, e)
		}
	}
	return orEmpty(entries), nil
}

func orEmpty(entries []Entry) []Entry {
	if len(entries) == 0 {
		return []Entry{{Title: emptyLibrary}}
	}
	return entries
}
