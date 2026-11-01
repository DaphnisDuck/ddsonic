// ddsonic: whole-collection fetchers for the catalog sync. Each pages one
// library collection with the caller's context and returns catalog records
// carrying Spotify IDs, or an error: never a partial collection. Kept in its
// own file so upstream merges of provider.go stay conflict-free.
//
// The response types below are separate from spotifyItem/spotifyArtist in
// provider_shared.go because the catalog needs the IDs those omit, and adding
// them upstream would conflict on every merge.

package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/provider"
)

// catalogPageSize is the largest page the library endpoints accept.
// /v1/playlists/{id}/items silently truncates larger pages, so 50 holds
// there too (see spotifyTrackPageSize).
const catalogPageSize = 50

// playlistItemFields limits playlist item pages to what the catalog stores.
const playlistItemFields = "items(added_at,item(id,name,type,uri,duration_ms,track_number,disc_number,is_local," +
	"artists(id,name),album(id,name,release_date,total_tracks,images,artists(id,name)))),total"

// ErrIncomplete means a paged read ended with a different number of items
// than Spotify reported, usually because the library changed mid-read.
// Applying it could drop items, so the sync fails and retries later.
var ErrIncomplete = errors.New("spotify: library changed while reading")

type catalogArtist struct {
	ID     string         `json:"id"`
	Name   string         `json:"name"`
	Images []spotifyImage `json:"images"`
}

type catalogAlbum struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	ReleaseDate string          `json:"release_date"`
	TotalTracks int             `json:"total_tracks"`
	Images      []spotifyImage  `json:"images"`
	Artists     []catalogArtist `json:"artists"`
}

type catalogTrack struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	URI         string          `json:"uri"`
	DurationMS  int             `json:"duration_ms"`
	TrackNumber int             `json:"track_number"`
	DiscNumber  int             `json:"disc_number"`
	IsLocal     bool            `json:"is_local"`
	Artists     []catalogArtist `json:"artists"`
	Album       *catalogAlbum   `json:"album"`
}

// savedEntry is one saved album or liked track. Playlist items name the track
// "item" (older responses: "track").
type savedEntry struct {
	AddedAt string        `json:"added_at"`
	Album   *catalogAlbum `json:"album"`
	Track   *catalogTrack `json:"track"`
	Item    *catalogTrack `json:"item"`
}

func (e savedEntry) track() *catalogTrack {
	if e.Item != nil {
		return e.Item
	}
	return e.Track
}

// SavedAlbumRecords returns every saved album.
func (p *SpotifyProvider) SavedAlbumRecords(ctx context.Context) ([]catalog.AlbumRecord, error) {
	return pageRecords(ctx, p, p.webAPI, "/v1/me/albums", nil, func(e savedEntry) (catalog.AlbumRecord, bool) {
		return albumRecord(e.Album, e.AddedAt)
	})
}

// SavedAlbumsHead returns the saved albums' count and newest album, in
// one request.
func (p *SpotifyProvider) SavedAlbumsHead(ctx context.Context) (catalog.CollectionHead, error) {
	return p.collectionHead(ctx, "/v1/me/albums", func(e savedEntry) (string, bool) {
		r, ok := albumRecord(e.Album, e.AddedAt)
		return r.Ref.ProviderID, ok
	})
}

// LikedTracksHead returns the liked tracks' count and newest track, in one
// request.
func (p *SpotifyProvider) LikedTracksHead(ctx context.Context) (catalog.CollectionHead, error) {
	return p.collectionHead(ctx, "/v1/me/tracks", func(e savedEntry) (string, bool) {
		r, ok := trackRecord(e.track(), e.AddedAt)
		return r.Ref.ProviderID, ok
	})
}

// collectionHead reads the first item of a saved collection, which Spotify
// lists newest first, with its total. An item the sync would skip (a local
// file, say) leaves NewestID empty, so the head matches nothing stored.
func (p *SpotifyProvider) collectionHead(ctx context.Context, path string, id func(savedEntry) (string, bool)) (catalog.CollectionHead, error) {
	if err := p.ensureSession(); err != nil {
		return catalog.CollectionHead{}, err
	}
	resp, err := p.webAPI(ctx, "GET", path, url.Values{"limit": {"1"}})
	if err != nil {
		return catalog.CollectionHead{}, fmt.Errorf("spotify: %s: %w", path, unreadable(err))
	}
	var page struct {
		Items []savedEntry `json:"items"`
		Total int          `json:"total"`
	}
	if err := decodeBody(resp, &page); err != nil {
		return catalog.CollectionHead{}, fmt.Errorf("spotify: parse %s: %w", path, err)
	}
	head := catalog.CollectionHead{Total: page.Total}
	if len(page.Items) > 0 {
		if newest, ok := id(page.Items[0]); ok {
			head.NewestID, head.NewestAt = newest, parseAdded(page.Items[0].AddedAt)
		}
	}
	return head, nil
}

// LikedTrackRecords returns every liked track with its album and artists.
func (p *SpotifyProvider) LikedTrackRecords(ctx context.Context) ([]catalog.TrackRecord, error) {
	return pageRecords(ctx, p, p.webAPI, "/v1/me/tracks", nil, func(e savedEntry) (catalog.TrackRecord, bool) {
		return trackRecord(e.track(), e.AddedAt)
	})
}

// PlaylistTrackRecords returns a playlist's tracks in order. A playlist this
// app may list but not read (403) or that no longer resolves (404) fails
// with an error wrapping catalog.ErrForbidden.
func (p *SpotifyProvider) PlaylistTrackRecords(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error) {
	path := "/v1/playlists/" + url.PathEscape(playlistID) + "/items"
	return pageRecords(ctx, p, p.webAPI, path, url.Values{"fields": {playlistItemFields}}, func(e savedEntry) (catalog.TrackRecord, bool) {
		return trackRecord(e.track(), e.AddedAt)
	})
}

// PlaylistRecords returns the user's library playlists without tracks. Own
// is set for playlists the user owns; Snapshot is Spotify's version token.
func (p *SpotifyProvider) PlaylistRecords(ctx context.Context) ([]catalog.PlaylistRecord, error) {
	userID, err := p.userIDForSync(ctx)
	if err != nil {
		return nil, err
	}
	return pageRecords(ctx, p, p.webAPI, "/v1/me/playlists", nil, func(it spotifyPlaylistItem) (catalog.PlaylistRecord, bool) {
		if it.ID == "" {
			return catalog.PlaylistRecord{}, false
		}
		count := 0
		if it.Items != nil {
			count = it.Items.Total
		}
		return catalog.PlaylistRecord{
			Ref: spotifyRef(it.ID), Name: it.Name, Own: it.Owner.ID == userID,
			Snapshot: it.SnapshotID, TrackCount: count,
		}, true
	})
}

// userIDForSync fetches the signed-in user's ID. Unlike currentUserID it
// fails instead of remembering an empty ID: every playlist's ownership
// depends on it, and a sync must not record them all as followed after a
// network blip.
func (p *SpotifyProvider) userIDForSync(ctx context.Context) (string, error) {
	if err := p.ensureSession(); err != nil {
		return "", err
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/me", nil)
	if err != nil {
		return "", fmt.Errorf("spotify: current user: %w", err)
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := decodeBody(resp, &me); err != nil {
		return "", fmt.Errorf("spotify: parse current user: %w", err)
	}
	if me.ID == "" {
		return "", errors.New("spotify: current user has no ID")
	}
	return me.ID, nil
}

// FollowedArtistRecords returns every followed artist (cursor paging).
func (p *SpotifyProvider) FollowedArtistRecords(ctx context.Context) ([]catalog.ArtistRecord, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	var out []catalog.ArtistRecord
	after, total, read := "", -1, 0
	for {
		q := url.Values{"type": {"artist"}, "limit": {strconv.Itoa(catalogPageSize)}}
		if after != "" {
			q.Set("after", after)
		}
		resp, err := p.webAPI(ctx, "GET", "/v1/me/following", q)
		if err != nil {
			return nil, fmt.Errorf("spotify: followed artists: %w", err)
		}
		var r struct {
			Artists struct {
				Items   []catalogArtist `json:"items"`
				Next    string          `json:"next"`
				Total   int             `json:"total"`
				Cursors struct {
					After string `json:"after"`
				} `json:"cursors"`
			} `json:"artists"`
		}
		if err := decodeBody(resp, &r); err != nil {
			return nil, fmt.Errorf("spotify: parse followed artists: %w", err)
		}
		if total >= 0 && r.Artists.Total != total {
			return nil, fmt.Errorf("followed artists: %w", ErrIncomplete)
		}
		total = r.Artists.Total
		read += len(r.Artists.Items)
		out = append(out, artistRecords(r.Artists.Items)...)
		// A repeated cursor would loop forever; treat it as the end.
		next := r.Artists.Cursors.After
		if r.Artists.Next == "" || next == "" || next == after || len(r.Artists.Items) == 0 {
			break
		}
		after = next
	}
	if read != total {
		return nil, fmt.Errorf("followed artists: read %d of %d: %w", read, total, ErrIncomplete)
	}
	return out, nil
}

// AlbumTrackRecords returns an album's full track list, retrying rate
// limits as webAPI does. It suits a load the user is waiting on.
func (p *SpotifyProvider) AlbumTrackRecords(ctx context.Context, albumID string) ([]catalog.TrackRecord, error) {
	return p.albumTrackRecords(ctx, p.webAPI, albumID)
}

// AlbumTrackRecordsOnce is AlbumTrackRecords for background work: a rate
// limit fails at once with a *catalog.RateLimitError, so the caller backs
// off on its own schedule instead of warning the user about retries.
func (p *SpotifyProvider) AlbumTrackRecordsOnce(ctx context.Context, albumID string) ([]catalog.TrackRecord, error) {
	return p.albumTrackRecords(ctx, p.webAPIOnce, albumID)
}

// albumTrackRecords pages /v1/albums/{id}/tracks. Its simplified tracks
// carry no album; the catalog attaches them to the album being cached.
func (p *SpotifyProvider) albumTrackRecords(ctx context.Context, get webGetter, albumID string) ([]catalog.TrackRecord, error) {
	path := "/v1/albums/" + url.PathEscape(albumID) + "/tracks"
	return pageRecords(ctx, p, get, path, nil, func(t catalogTrack) (catalog.TrackRecord, bool) {
		return trackRecord(&t, "")
	})
}

// webGetter is webAPI's signature: one Web API request.
type webGetter func(ctx context.Context, method, path string, query url.Values) (*http.Response, error)

// maxRetryAfterSecs caps the wait a rate limit asks for, so an absurd
// header cannot stall the catalog workers for weeks (or overflow the
// duration). Real blocks have run to 20h.
const maxRetryAfterSecs = 48 * 3600

// MaxRateLimit is the longest block the provider keeps (ddsonic).
const MaxRateLimit = maxRetryAfterSecs * time.Second

// webAPIOnce is webAPI without the 429 retries: a rate limit returns a
// *catalog.RateLimitError carrying Spotify's Retry-After. Other statuses
// are a *StatusError, as webAPI's are.
func (p *SpotifyProvider) webAPIOnce(ctx context.Context, method, path string, query url.Values) (*http.Response, error) {
	if err := p.rateLimited(); err != nil {
		return nil, err
	}
	// The request and what its answer says about the sign-in are the shared
	// path's (request); only the rule for rate limits below is this one's.
	resp, err := p.request(ctx, &apiCall{method: method, path: path, query: query})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		if secs, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && secs > 0 {
			return nil, p.block(time.Duration(min(secs, maxRetryAfterSecs)) * time.Second)
		}
		return nil, &catalog.RateLimitError{}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	return nil, statusError(resp.StatusCode, resp.Status, body, err)
}

// pageRecords GETs path through get with offset paging and maps each item
// with conv, which may skip items (local files, episodes). It fails unless
// every item Spotify reported was read and the total held steady across
// pages.
func pageRecords[T, R any](ctx context.Context, p *SpotifyProvider, get webGetter, path string, extra url.Values, conv func(T) (R, bool)) ([]R, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	var out []R
	read, total := 0, -1
	for {
		q := url.Values{"limit": {strconv.Itoa(catalogPageSize)}, "offset": {strconv.Itoa(read)}}
		for k, v := range extra {
			q[k] = v
		}
		resp, err := get(ctx, "GET", path, q)
		if err != nil {
			return nil, fmt.Errorf("spotify: %s: %w", path, unreadable(err))
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("spotify: read %s: %w", path, err)
		}
		var page struct {
			Items []T `json:"items"`
			Total int `json:"total"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("spotify: parse %s: %w", path, err)
		}
		if total >= 0 && page.Total != total {
			return nil, fmt.Errorf("%s: %w", path, ErrIncomplete)
		}
		total = page.Total
		read += len(page.Items)
		for _, item := range page.Items {
			if r, ok := conv(item); ok {
				out = append(out, r)
			}
		}
		if len(page.Items) == 0 || read >= total {
			break
		}
	}
	if read != total {
		return nil, fmt.Errorf("%s: read %d of %d: %w", path, read, total, ErrIncomplete)
	}
	return out, nil
}

// unreadable marks 403 and 404 as catalog.ErrForbidden: Development Mode
// apps may list followed playlists but not read their items, and removed or
// editorial playlists can 404.
func unreadable(err error) error {
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusForbidden || se.Code == http.StatusNotFound) {
		return fmt.Errorf("%w: %w", catalog.ErrForbidden, err)
	}
	return err
}

func spotifyRef(id string) catalog.Ref { return catalog.Ref{Provider: catalog.Spotify, ProviderID: id} }

func artistRecords(in []catalogArtist) []catalog.ArtistRecord {
	out := make([]catalog.ArtistRecord, 0, len(in))
	for _, a := range in {
		if a.ID != "" {
			out = append(out, catalog.ArtistRecord{Ref: spotifyRef(a.ID), Name: a.Name, ImageURL: pickCoverImage(a.Images)})
		}
	}
	return out
}

func albumRecord(a *catalogAlbum, addedAt string) (catalog.AlbumRecord, bool) {
	if a == nil || a.ID == "" {
		return catalog.AlbumRecord{}, false
	}
	return catalog.AlbumRecord{
		Ref: spotifyRef(a.ID), Title: a.Name, Artists: artistRecords(a.Artists),
		Year: provider.YearFromDate(a.ReleaseDate), TrackCount: a.TotalTracks, ArtworkURL: pickCoverImage(a.Images),
		AddedAt: parseAdded(addedAt),
	}, true
}

// trackRecord maps a track. Local files, podcast episodes and unavailable
// items have no catalog identity yet and are skipped.
func trackRecord(t *catalogTrack, addedAt string) (catalog.TrackRecord, bool) {
	if t == nil || t.ID == "" || t.IsLocal || (t.Type != "" && t.Type != "track") {
		return catalog.TrackRecord{}, false
	}
	uri := t.URI
	if uri == "" {
		uri = "spotify:track:" + t.ID
	}
	rec := catalog.TrackRecord{
		Ref: spotifyRef(t.ID), Title: t.Name, Artists: artistRecords(t.Artists),
		Disc: t.DiscNumber, TrackNo: t.TrackNumber, Duration: time.Duration(t.DurationMS) * time.Millisecond,
		PlayableURI: uri, AddedAt: parseAdded(addedAt),
	}
	if album, ok := albumRecord(t.Album, ""); ok {
		rec.Album, rec.Year = &album, album.Year
	} else if t.Album != nil {
		rec.AlbumTitle = t.Album.Name
	}
	return rec, true
}

// parseAdded reads Spotify's RFC 3339 added_at. Unknown is the zero time,
// which keeps the catalog's stored time.
func parseAdded(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
