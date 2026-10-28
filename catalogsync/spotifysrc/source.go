// Package spotifysrc is the catalog sync source for a Spotify library:
// saved albums, followed artists, liked tracks and library playlists.
package spotifysrc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync"
)

// Collection names, as recorded in the catalog's sync state.
const (
	Albums    = catalog.CollectionAlbums
	Artists   = catalog.CollectionArtists
	Liked     = catalog.CollectionLiked
	Playlists = catalog.CollectionPlaylists
)

// Client is the part of the Spotify provider the source uses. Each method
// returns a whole collection or an error, never part of one.
type Client interface {
	SavedAlbumRecords(ctx context.Context) ([]catalog.AlbumRecord, error)
	FollowedArtistRecords(ctx context.Context) ([]catalog.ArtistRecord, error)
	LikedTrackRecords(ctx context.Context) ([]catalog.TrackRecord, error)
	PlaylistRecords(ctx context.Context) ([]catalog.PlaylistRecord, error)
	PlaylistTrackRecords(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error)
	// AlbumTrackRecords retries rate limits; AlbumTrackRecordsOnce returns
	// a *catalog.RateLimitError instead.
	AlbumTrackRecords(ctx context.Context, albumID string) ([]catalog.TrackRecord, error)
	AlbumTrackRecordsOnce(ctx context.Context, albumID string) ([]catalog.TrackRecord, error)
	// SavedAlbumsHead and LikedTracksHead read a collection's count and
	// newest item in one request.
	SavedAlbumsHead(ctx context.Context) (catalog.CollectionHead, error)
	LikedTracksHead(ctx context.Context) (catalog.CollectionHead, error)
}

// fullReadEvery is how long saved albums and liked tracks may go on being
// found unchanged by their head alone before they are read in full again,
// so changed titles or artwork of stored items still land.
const fullReadEvery = 24 * time.Hour

// Source syncs one Spotify account.
type Source struct {
	client Client
	now    func() time.Time // replaced in tests
}

var (
	_ catalogsync.Source      = (*Source)(nil)
	_ catalogsync.AlbumSource = (*Source)(nil)
)

// New returns a Source reading through client.
func New(client Client) *Source { return &Source{client: client, now: time.Now} }

// Provider implements catalogsync.Source.
func (*Source) Provider() string { return catalog.Spotify }

// Collections implements catalogsync.Source. Playlists come last: they are
// the slowest and the least likely to change.
func (*Source) Collections() []string { return []string{Albums, Artists, Liked, Playlists} }

// Fetch implements catalogsync.Source.
func (s *Source) Fetch(ctx context.Context, collection string, known catalogsync.Known) (catalog.Snapshot, error) {
	snap := catalog.Snapshot{Provider: catalog.Spotify, Collection: collection}
	var err error
	switch collection {
	case Albums:
		if s.unchanged(ctx, s.client.SavedAlbumsHead, known.Collections[Albums]) {
			return catalog.Snapshot{}, catalogsync.ErrUnchanged
		}
		snap.Albums, err = s.client.SavedAlbumRecords(ctx)
	case Artists:
		snap.Artists, err = s.client.FollowedArtistRecords(ctx)
	case Liked:
		if s.unchanged(ctx, s.client.LikedTracksHead, known.Collections[Liked]) {
			return catalog.Snapshot{}, catalogsync.ErrUnchanged
		}
		snap.Tracks, err = s.client.LikedTrackRecords(ctx)
	case Playlists:
		snap.Playlists, err = s.playlists(ctx, known.PlaylistSnapshots)
	default:
		err = fmt.Errorf("unknown collection %q", collection)
	}
	if err != nil {
		return catalog.Snapshot{}, err
	}
	return snap, nil
}

// unchanged reports whether a collection read in full within fullReadEvery
// still matches its head, so the full read can be skipped. A failed head
// read answers no: the full read then reports what went wrong.
func (s *Source) unchanged(ctx context.Context, head func(context.Context) (catalog.CollectionHead, error), stored catalog.CollectionState) bool {
	if stored.AppliedAt.IsZero() || s.now().Sub(stored.AppliedAt) >= fullReadEvery {
		return false
	}
	h, err := head(ctx)
	return err == nil && stored.Matches(h)
}

// playlists lists the library playlists and fetches tracks only for those
// whose Spotify snapshot changed since the catalog stored them. A playlist
// Spotify will not let this app read keeps its stored tracks; any other
// failure fails the whole collection.
func (s *Source) playlists(ctx context.Context, known map[string]string) ([]catalog.PlaylistRecord, error) {
	lists, err := s.client.PlaylistRecords(ctx)
	if err != nil {
		return nil, err
	}
	for i := range lists {
		p := &lists[i]
		if p.Snapshot != "" && known[p.Ref.ProviderID] == p.Snapshot {
			continue // unchanged: keep the stored tracks
		}
		tracks, err := s.client.PlaylistTrackRecords(ctx, p.Ref.ProviderID)
		if errors.Is(err, catalog.ErrForbidden) {
			applog.Info("catalog sync: skipping tracks of playlist %q: %v", p.Name, err)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("playlist %q: %w", p.Name, err)
		}
		p.Tracks, p.TracksFetched = tracks, true
	}
	return lists, nil
}

// AlbumTracks implements catalogsync.AlbumSource.
func (s *Source) AlbumTracks(ctx context.Context, album catalog.Ref) ([]catalog.TrackRecord, error) {
	return s.client.AlbumTrackRecords(ctx, album.ProviderID)
}

// AlbumTracksOnce implements catalogsync.AlbumSource.
func (s *Source) AlbumTracksOnce(ctx context.Context, album catalog.Ref) ([]catalog.TrackRecord, error) {
	return s.client.AlbumTrackRecordsOnce(ctx, album.ProviderID)
}
