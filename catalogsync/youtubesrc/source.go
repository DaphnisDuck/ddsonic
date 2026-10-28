// Package youtubesrc is the catalog sync source for a YouTube Music
// account: its music playlists and Liked Music.
package youtubesrc

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/playlist"
)

// Collection names, as recorded in the catalog's sync state.
const (
	Liked     = catalog.CollectionLiked
	Playlists = catalog.CollectionPlaylists
)

// Client reads the account. Each method returns a whole collection or an
// error, never part of one.
type Client interface {
	// PlaylistRecords lists the music playlists, without tracks. A record
	// with a Snapshot is refetched only when it changes. synced holds the
	// catalog's playlists by ID: one of them that cannot be classified as
	// music or not right now is listed anyway, so it is not reconciled away.
	PlaylistRecords(ctx context.Context, synced map[string]string) ([]catalog.PlaylistRecord, error)
	// PlaylistRecord reads one playlist by ID, without tracks.
	PlaylistRecord(ctx context.Context, playlistID string) (catalog.PlaylistRecord, error)
	PlaylistTrackRecords(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error)
	LikedTrackRecords(ctx context.Context) ([]catalog.TrackRecord, error)
}

// Source syncs one YouTube Music account.
type Source struct {
	client Client
	extra  []string // other people's playlists to sync, by ID
}

var _ catalogsync.Source = (*Source)(nil)

// New returns a Source reading through client. extra are other people's
// playlists to sync besides the account's own, as links or IDs: YouTube
// lists the playlists you save nowhere a sync can read.
func New(client Client, extra ...string) *Source {
	s := &Source{client: client}
	for _, e := range extra {
		if id := PlaylistID(e); id != "" && !slices.Contains(s.extra, id) {
			s.extra = append(s.extra, id)
		} else if id == "" {
			applog.Warn("youtube: %q is not a playlist link or ID", e)
		}
	}
	return s
}

// PlaylistID returns the playlist ID of a playlist link (its list=
// parameter, on youtube.com or music.youtube.com) or of a bare ID, and ""
// for anything else.
func PlaylistID(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return u.Query().Get("list")
	}
	if s == "" || strings.ContainsAny(s, "/?&= ") {
		return ""
	}
	return s
}

// Provider implements catalogsync.Source.
func (*Source) Provider() string { return catalog.YouTube }

// Collections implements catalogsync.Source. Liked Music first: it is one
// read, while playlists take one each.
func (*Source) Collections() []string { return []string{Liked, Playlists} }

// Fetch implements catalogsync.Source.
func (s *Source) Fetch(ctx context.Context, collection string, known catalogsync.Known) (catalog.Snapshot, error) {
	snap := catalog.Snapshot{Provider: catalog.YouTube, Collection: collection}
	var err error
	switch collection {
	case Liked:
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

// playlists lists the music playlists and fetches the tracks of each whose
// change marker is missing or changed. A playlist YouTube will not show
// keeps its stored tracks; any other failure fails the whole collection.
func (s *Source) playlists(ctx context.Context, known map[string]string) ([]catalog.PlaylistRecord, error) {
	lists, err := s.client.PlaylistRecords(ctx, known)
	if err != nil {
		return nil, err
	}
	// Other people's playlists, unless the account's own list has them.
	for _, id := range s.extra {
		if slices.ContainsFunc(lists, func(p catalog.PlaylistRecord) bool { return p.Ref.ProviderID == id }) {
			continue
		}
		p, err := s.client.PlaylistRecord(ctx, id)
		if errors.Is(err, catalog.ErrForbidden) {
			applog.Info("catalog sync: skipping playlist %s: %v", id, err)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("playlist %s: %w", id, err)
		}
		p.Own = false // followed, not yours
		lists = append(lists, p)
	}
	for i := range lists {
		p := &lists[i]
		if p.Snapshot != "" && known[p.Ref.ProviderID] == p.Snapshot {
			continue
		}
		tracks, err := s.client.PlaylistTrackRecords(ctx, p.Ref.ProviderID)
		if errors.Is(err, catalog.ErrForbidden) {
			applog.Info("catalog sync: skipping tracks of playlist %q: %v", p.Name, err)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("playlist %q: %w", p.Name, err)
		}
		p.Tracks, p.TracksFetched, p.TrackCount = tracks, true, len(tracks)
	}
	return lists, nil
}

// Mixed reads an account signed in both ways, each collection through the
// mode that reads it best: Liked Music through OAuth (the official API,
// with cleaner artist names) and playlists through cookies (the API lists
// none of the playlists saved from other channels, and for some accounts
// none at all). When OAuth needs signing in again, as a Testing-status
// Google app does every few days, Liked Music falls back to cookies.
type Mixed struct {
	OAuth, Cookies Client
}

var _ Client = Mixed{}

// PlaylistRecords implements Client through cookies.
func (m Mixed) PlaylistRecords(ctx context.Context, synced map[string]string) ([]catalog.PlaylistRecord, error) {
	return m.Cookies.PlaylistRecords(ctx, synced)
}

// PlaylistRecord implements Client through cookies.
func (m Mixed) PlaylistRecord(ctx context.Context, playlistID string) (catalog.PlaylistRecord, error) {
	return m.Cookies.PlaylistRecord(ctx, playlistID)
}

// PlaylistTrackRecords implements Client through cookies.
func (m Mixed) PlaylistTrackRecords(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error) {
	return m.Cookies.PlaylistTrackRecords(ctx, playlistID)
}

// LikedTrackRecords implements Client through OAuth, or cookies when OAuth
// needs signing in or refuses (its daily quota spent, say).
func (m Mixed) LikedTrackRecords(ctx context.Context) ([]catalog.TrackRecord, error) {
	tracks, err := m.OAuth.LikedTrackRecords(ctx)
	if errors.Is(err, playlist.ErrNeedsAuth) || errors.Is(err, catalog.ErrForbidden) {
		applog.Info("youtube: reading Liked Music through cookies: %v", err)
		return m.Cookies.LikedTrackRecords(ctx)
	}
	return tracks, err
}
