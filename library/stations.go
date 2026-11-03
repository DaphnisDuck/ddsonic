package library

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
)

// sortedByName sorts rows A–Z by the name each shows, the way every other
// library list sorts: catalog.SortKey folds case and accents and drops
// leading punctuation. No locale rules apply; scripts order by code point
// (Latin, then Greek, Cyrillic, Arabic, CJK). Ties fall to the name exactly
// as written, then to id (a stream URL, a channel or tag ID), so the order
// does not depend on the order the rows arrived in; rows equal in both keep
// their arrival order. It returns a new slice: rows, which may be a
// provider's own, is left as it is, and no name is changed.
func sortedByName[T any](rows []T, name, id func(T) string) []T {
	type keyed struct {
		key, name, id string
		row           T
	}
	ks := make([]keyed, len(rows))
	for i, r := range rows {
		n := name(r)
		ks[i] = keyed{catalog.SortKey(n), n, id(r), r}
	}
	slices.SortStableFunc(ks, func(a, b keyed) int {
		return cmp.Or(
			strings.Compare(a.key, b.key),
			strings.Compare(a.name, b.name),
			strings.Compare(a.id, b.id),
		)
	})
	out := make([]T, len(ks))
	for i, k := range ks {
		out[i] = k.row
	}
	return out
}

// namedGenres is implemented by a category browser whose categories the
// library lists A–Z by name (radio's tags) instead of in the browser's
// order (radio's countries keep theirs: grouped by region, most stations
// first). GenreName is a category's name as its row shows it, without what
// the label adds to it, such as a station count.
type namedGenres interface {
	GenreName(provider.GenreInfo) string
}

// sortedGenres sorts a namedGenres browser's categories A–Z by name, then
// by ID. Their labels are left as they are.
func sortedGenres(genres []provider.GenreInfo, ng namedGenres) []provider.GenreInfo {
	return sortedByName(genres, ng.GenreName, func(g provider.GenreInfo) string { return g.ID })
}

// sortedStations sorts radio stations A–Z by the name each row shows, then
// by stream URL.
func sortedStations(tracks []playlist.Track) []playlist.Track {
	return sortedByName(tracks, playlist.Track.DisplayName, func(t playlist.Track) string { return t.Path })
}

// stationSelection is implemented by a radio provider whose directory
// station lists (a country's, a tag's, a directory search) hold a selection
// of the directory rather than all of it. StationSelection says which, in a
// few words ("Top 200 by votes").
type stationSelection interface {
	StationSelection() string
}

// selectedStationsLevel is a list of directory stations whose note says
// which of the directory's stations it holds. It has Level's methods and
// Note only: radio providers do not sign in, so no AuthLevel is hidden.
type selectedStationsLevel struct {
	Level
	note []string
}

// Note implements NotedLevel.
func (l *selectedStationsLevel) Note() []string { return l.note }

// directoryStationsLevel lists the directory stations load returns, A–Z,
// with the provider's selection as the list's note when it has one.
func directoryStationsLevel(title string, prov playlist.Provider, load func() ([]playlist.Track, error)) Level {
	l := TrackLevel(title, prov, func(context.Context) ([]playlist.Track, error) {
		tracks, err := load()
		return sortedStations(tracks), err
	})
	sel, ok := prov.(stationSelection)
	if !ok || sel.StationSelection() == "" {
		return l
	}
	return &selectedStationsLevel{l, []string{sel.StationSelection() + ", listed A–Z."}}
}

// sortedPlaylists is a provider whose Playlists come A–Z by the name each
// row shows, then by ID: the cliamp radio channels, as Browse Stations
// lists them.
type sortedPlaylists struct{ playlist.Provider }

func (p sortedPlaylists) Playlists() ([]playlist.PlaylistInfo, error) {
	lists, err := p.Provider.Playlists()
	return sortedByName(lists, func(l playlist.PlaylistInfo) string { return l.Name }, func(l playlist.PlaylistInfo) string { return l.ID }), err
}
