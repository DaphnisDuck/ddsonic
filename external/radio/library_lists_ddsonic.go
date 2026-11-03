// ddsonic: what the library needs to know about the radio lists it shows.
// Kept in its own file so upstream merges of the provider stay
// conflict-free.

package radio

import "fmt"

// StationSelection says which of the directory's stations a country's or a
// tag's list and a directory search hold: the most voted, up to the limit
// each query asks for (countryTrackLimit, tagTrackLimit, searchLimit, all
// the same). The library lists them A–Z and shows this above them.
func (*Provider) StationSelection() string {
	return fmt.Sprintf("Top %d by votes", countryTrackLimit)
}
