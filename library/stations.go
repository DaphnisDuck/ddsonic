package library

import (
	"cmp"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// sortedStations sorts radio stations A–Z by the name each row shows, the
// way every other library list sorts: catalog.SortKey folds case and
// accents and drops leading punctuation. No locale rules apply; scripts
// order by code point (Latin, then Greek, Cyrillic, Arabic, CJK). Ties
// fall to the name exactly as written, then the stream URL, so the order
// does not depend on the order the stations arrived in; stations equal in
// both look and play the same and keep their arrival order. Names are
// never changed.
func sortedStations(tracks []playlist.Track) []playlist.Track {
	type keyed struct {
		key, name string
		track     playlist.Track
	}
	rows := make([]keyed, len(tracks))
	for i, t := range tracks {
		name := t.DisplayName()
		rows[i] = keyed{catalog.SortKey(name), name, t}
	}
	slices.SortStableFunc(rows, func(a, b keyed) int {
		return cmp.Or(
			strings.Compare(a.key, b.key),
			strings.Compare(a.name, b.name),
			strings.Compare(a.track.Path, b.track.Path),
		)
	})
	for i, r := range rows {
		tracks[i] = r.track
	}
	return tracks
}
