// Package radiosrc is the catalog sync source for your radio stations: the
// ones you starred, and the built-in and radios.toml stations. Both come
// from local files, so a sync is instant and needs no network. The catalog
// holds them so search finds them; browsing Radio still reads the provider.
package radiosrc

import (
	"context"
	"fmt"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/playlist"
)

// Collection names. Search ranks stations in Favorites first.
const (
	Favorites = "favorites"
	Custom    = "custom"
)

// Client is the part of the radio provider the source reads.
type Client interface {
	FavoriteTracks() []playlist.Track
	LocalStationTracks() []playlist.Track
}

// Source syncs one radio provider's stations.
type Source struct {
	client Client
}

var _ catalogsync.Source = (*Source)(nil)

// New returns a Source reading client.
func New(client Client) *Source { return &Source{client: client} }

// Provider implements catalogsync.Source.
func (*Source) Provider() string { return catalog.Radio }

// Collections implements catalogsync.Source.
func (*Source) Collections() []string { return []string{Favorites, Custom} }

// Fetch implements catalogsync.Source.
func (s *Source) Fetch(_ context.Context, collection string, _ catalogsync.Known) (catalog.Snapshot, error) {
	var tracks []playlist.Track
	switch collection {
	case Favorites:
		tracks = s.client.FavoriteTracks()
	case Custom:
		tracks = s.client.LocalStationTracks()
	default:
		return catalog.Snapshot{}, fmt.Errorf("unknown collection %q", collection)
	}
	snap := catalog.Snapshot{Provider: catalog.Radio, Collection: collection}
	for _, t := range tracks {
		if t.Path == "" {
			continue
		}
		// A station is identified by its stream URL, as favorites are.
		snap.Tracks = append(snap.Tracks, catalog.TrackRecord{
			Ref:         catalog.Ref{Provider: catalog.Radio, ProviderID: t.Path},
			Title:       t.Title,
			Genre:       t.Genre,
			PlayableURI: t.Path,
		})
	}
	return snap, nil
}
