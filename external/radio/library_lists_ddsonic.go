// ddsonic: what the library needs to know about the radio lists it shows.
// Kept in its own file so upstream merges of the provider stay
// conflict-free.

package radio

import (
	"fmt"

	"github.com/bjarneo/cliamp/provider"
)

// StationSelection says which of the directory's stations a country's or a
// tag's list and a directory search hold: the most voted, up to the limit
// each query asks for (countryTrackLimit, tagTrackLimit, searchLimit, all
// the same). The library lists them A–Z and shows this above them.
func (*Provider) StationSelection() string {
	return fmt.Sprintf("Top %d by votes", countryTrackLimit)
}

// GenreName is the tag's name as its row shows it, without the station
// count Genres appends: the label Genres builds from the tag (the ID) is
// GenreName, " (", the count and ")". The library lists the tags A–Z by
// it. The country browser has no GenreName: its places stay grouped by
// region, most stations first.
func (*tagBrowser) GenreName(g provider.GenreInfo) string { return sanitizeTagLabel(g.ID) }
