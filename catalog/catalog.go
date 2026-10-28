// Package catalog is ddsonic' music catalog: the albums, artists, tracks
// and playlists known across providers, and which of them are in the user's
// library. It defines the types and the interface the UI reads through; it
// holds no SQL and no Bubbletea. catalog/sqlite implements it, and
// catalogsync keeps it current from the providers.
//
// Every object has an internal ID owned by the catalog. The provider's own ID
// (Provider, ProviderID) is kept alongside it and is unique, but is never the
// catalog's key.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound means a requested object is not in the catalog.
var ErrNotFound = errors.New("not in catalog")

// ErrForbidden means a provider refuses this client access to one item, for
// example a Spotify playlist it may list but not read. A sync skips the item
// and keeps what the catalog already holds for it.
var ErrForbidden = errors.New("provider refused access")

// RateLimitError means a provider asked the client to slow down. RetryAfter
// is the provider's requested wait, or zero when it gave none. Until, when
// set, is when a long block ends, for display.
type RateLimitError struct {
	RetryAfter time.Duration
	Until      time.Time
}

func (e *RateLimitError) Error() string {
	switch {
	case !e.Until.IsZero():
		return "rate limited until " + e.Until.Local().Format("Jan 2 15:04")
	case e.RetryAfter > 0:
		return fmt.Sprintf("provider rate limit (retry after %v)", e.RetryAfter)
	}
	return "provider rate limit"
}

// Provider names used in the catalog.
const (
	Spotify = "spotify"
	Local   = "local"
	YouTube = "youtube"
)

// Collections a provider's sync can offer. The library menu of a synced
// source offers a list for each one its sync provides.
const (
	CollectionAlbums    = "albums"
	CollectionArtists   = "artists"
	CollectionPlaylists = "playlists"
	CollectionLiked     = "liked"
)

// Kind is what a library membership row refers to.
type Kind string

// Library membership kinds. For Spotify: saved albums, followed artists,
// liked tracks and library playlists. For Local: indexed albums and artists.
const (
	KindAlbum    Kind = "album"
	KindArtist   Kind = "artist"
	KindTrack    Kind = "track"
	KindPlaylist Kind = "playlist"
)

// Ref is a provider's identity for an object.
type Ref struct {
	Provider   string
	ProviderID string
}

// Artist is a catalog artist.
type Artist struct {
	ID       int64
	Ref      Ref
	Name     string
	ImageURL string
}

// Album is a catalog album. Artist is the display credit, joined from the
// album's artists in order.
type Album struct {
	ID         int64
	Ref        Ref
	Title      string
	Artist     string
	Year       int
	TrackCount int
	ArtworkURL string
	// TracksCached reports whether the album's track list is in the catalog.
	TracksCached bool
}

// Track is a catalog track. PlayableURI is what the player is given
// (spotify:track:…, a file path, …); Artist is the joined display credit.
type Track struct {
	ID          int64
	Ref         Ref
	Title       string
	Artist      string
	AlbumID     int64 // 0 when the track has no catalog album
	AlbumTitle  string
	Disc        int
	TrackNo     int
	Duration    time.Duration
	PlayableURI string
	Genre       string
	Year        int
	ArtworkURL  string // the album's cover, when known
}

// Playlist is a catalog playlist.
type Playlist struct {
	ID         int64
	Ref        Ref
	Name       string
	Own        bool // owned by the user rather than followed
	TrackCount int
}

// Genre is a genre tag and how many library albums carry it.
type Genre struct {
	Name       string
	AlbumCount int
}

// CollectionStatus is the last sync outcome for one provider collection.
type CollectionStatus struct {
	Collection  string
	LastAttempt time.Time // zero if never attempted
	LastSuccess time.Time // zero if never succeeded
	LastError   string    // from the last attempt; empty when it succeeded
}

// AlbumOrder is how a library album list is sorted.
type AlbumOrder int

const (
	ByTitle  AlbumOrder = iota // title, then artist
	ByArtist                   // artist, then title
)

// Catalog is the read side the UI uses. Every method is a local query: none
// reaches the network.
type Catalog interface {
	// Albums returns provider's library albums in order; an empty
	// provider means every provider's.
	Albums(ctx context.Context, provider string, order AlbumOrder) ([]Album, error)
	// Album returns one album by its catalog ID, or ErrNotFound.
	Album(ctx context.Context, id int64) (Album, error)
	// AlbumTracks returns an album's tracks in disc and track order, and
	// whether its track list has been cached at all.
	AlbumTracks(ctx context.Context, albumID int64) (tracks []Track, cached bool, err error)
	// Artists returns provider's library artists, by name; an empty
	// provider means every provider's.
	Artists(ctx context.Context, provider string) ([]Artist, error)
	// ArtistAlbums returns every catalog album credited to the artist,
	// newest first.
	ArtistAlbums(ctx context.Context, artistID int64) ([]Album, error)
	// Playlists returns provider's library playlists: the user's own first,
	// then followed, each by name.
	Playlists(ctx context.Context, provider string) ([]Playlist, error)
	// PlaylistTracks returns a playlist's tracks in playlist order.
	PlaylistTracks(ctx context.Context, playlistID int64) ([]Track, error)
	// Genres returns the genres of provider's library albums' tracks, by
	// name, ignoring case.
	Genres(ctx context.Context, provider string) ([]Genre, error)
	// GenreAlbums returns provider's library albums with a track of genre
	// (ignoring case), by title.
	GenreAlbums(ctx context.Context, provider, genre string) ([]Album, error)
	// LikedTracks returns provider's liked tracks, most recently liked first.
	LikedTracks(ctx context.Context, provider string) ([]Track, error)
	// Search returns up to limit results of each kind q wants, best first.
	// An empty query finds nothing.
	Search(ctx context.Context, q Query, limit int) (SearchResults, error)
	// SyncStatus returns the last sync outcome of each of provider's
	// collections.
	SyncStatus(ctx context.Context, provider string) ([]CollectionStatus, error)

	// AlbumTracksByRef, ArtistAlbumsByRef and PlaylistTracksByRef read like
	// AlbumTracks, ArtistAlbums and PlaylistTracks, finding the entity by
	// ref within the same read, or failing with ErrNotFound. IDs are not
	// forever: once a sync removes an entity, a new one may be given its ID,
	// so an action kept from an earlier listing reads its entity by ref.
	AlbumTracksByRef(ctx context.Context, ref Ref) (tracks []Track, cached bool, err error)
	ArtistAlbumsByRef(ctx context.Context, ref Ref) ([]Album, error)
	PlaylistTracksByRef(ctx context.Context, ref Ref) ([]Track, error)
}

// AlbumTrackFetcher is an optional capability of a Catalog: fetching an
// album's tracks from its provider when the catalog has not cached them, and
// caching them so the album opens offline from then on.
type AlbumTrackFetcher interface {
	// FetchAlbumTracks returns the album's tracks, in disc and track order,
	// as the catalog now stores them.
	FetchAlbumTracks(ctx context.Context, album Album) ([]Track, error)
}
