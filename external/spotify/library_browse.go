// ddsonic: saved-album and followed-artist browsing for the library
// navigation. Kept in its own file so upstream merges of provider.go stay
// conflict-free.

package spotify

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/provider"
)

var (
	_ provider.AlbumBrowser     = (*SpotifyProvider)(nil)
	_ provider.AlbumTrackLoader = (*SpotifyProvider)(nil)
	_ provider.ArtistBrowser    = (*SpotifyProvider)(nil)
)

// spotifyArtistPageSize is the maximum /v1/me/following and
// /v1/artists/{id}/albums accept per request.
const spotifyArtistPageSize = 50

// artistAlbumGroups limits an artist's discography to their own releases;
// "appears_on" and "compilation" would bury them under guest spots.
const artistAlbumGroups = "album,single"

// Artists returns the artists the user follows, sorted by name.
// Implements provider.ArtistBrowser.
func (p *SpotifyProvider) Artists() ([]provider.ArtistInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	records, err := p.FollowedArtistRecords(ctx)
	if err != nil {
		return nil, err
	}
	all := make([]provider.ArtistInfo, len(records))
	for i, a := range records {
		all[i] = provider.ArtistInfo{ID: a.Ref.ProviderID, Name: a.Name}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return strings.ToLower(all[i].Name) < strings.ToLower(all[j].Name)
	})
	return all, nil
}

// ArtistAlbums returns an artist's albums and singles, newest first. The
// returned IDs are bare album IDs accepted by AlbumTracks.
// Implements provider.ArtistBrowser.
func (p *SpotifyProvider) ArtistAlbums(artistID string) ([]provider.AlbumInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	limit := spotifyArtistPageSize
	var all []provider.AlbumInfo
	for offset := 0; ; {
		page, total, err := p.artistAlbumsPage(ctx, artistID, offset, limit)
		// Development Mode apps may accept smaller pages than the documented
		// maximum; fall back once to the search-sized page, as search does.
		if isInvalidLimit(err) && limit != devModeSearchLimit {
			limit = devModeSearchLimit
			continue
		}
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		offset += limit
		if offset >= total || len(page) == 0 {
			break
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Year > all[j].Year })
	return all, nil
}

func (p *SpotifyProvider) artistAlbumsPage(ctx context.Context, artistID string, offset, limit int) ([]provider.AlbumInfo, int, error) {
	query := url.Values{
		"include_groups": {artistAlbumGroups},
		"limit":          {strconv.Itoa(limit)},
		"offset":         {strconv.Itoa(offset)},
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/artists/"+url.PathEscape(artistID)+"/albums", query)
	if err != nil {
		return nil, 0, fmt.Errorf("spotify: artist %s albums: %w", artistID, err)
	}
	var result struct {
		Items []spotifyAlbumItem `json:"items"`
		Total int                `json:"total"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, 0, fmt.Errorf("spotify: parse artist %s albums: %w", artistID, err)
	}
	albums := make([]provider.AlbumInfo, 0, len(result.Items))
	for _, a := range result.Items {
		if a.ID == "" {
			continue
		}
		albums = append(albums, provider.AlbumInfo{
			ID:         a.ID,
			Name:       a.Name,
			Artist:     artistNames(a.Artists),
			ArtistID:   artistID,
			Year:       provider.YearFromDate(a.ReleaseDate),
			TrackCount: a.TotalTracks,
		})
	}
	return albums, result.Total, nil
}

// savedAlbumsSort is the only order /v1/me/albums offers: most recently saved
// first.
const savedAlbumsSort = "added"

// AlbumSortTypes implements provider.AlbumBrowser.
func (*SpotifyProvider) AlbumSortTypes() []provider.SortType {
	return []provider.SortType{{ID: savedAlbumsSort, Label: "Recently Saved"}}
}

// DefaultAlbumSort implements provider.AlbumBrowser.
func (*SpotifyProvider) DefaultAlbumSort() string { return savedAlbumsSort }

// AlbumList returns one page of the user's saved albums, most recently saved
// first. The IDs are bare album IDs accepted by AlbumTracks.
// Implements provider.AlbumBrowser.
func (p *SpotifyProvider) AlbumList(_ string, offset, size int) ([]provider.AlbumInfo, error) {
	if err := p.ensureSession(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	query := url.Values{
		"limit":  {strconv.Itoa(min(max(size, 1), spotifyAlbumPageSize))},
		"offset": {strconv.Itoa(offset)},
	}
	resp, err := p.webAPI(ctx, "GET", "/v1/me/albums", query)
	if err != nil {
		return nil, fmt.Errorf("spotify: list saved albums: %w", err)
	}
	var result struct {
		Items []struct {
			Album spotifyAlbumItem `json:"album"`
		} `json:"items"`
	}
	if err := decodeBody(resp, &result); err != nil {
		return nil, fmt.Errorf("spotify: parse saved albums: %w", err)
	}
	albums := make([]provider.AlbumInfo, 0, len(result.Items))
	for _, item := range result.Items {
		a := item.Album
		if a.ID == "" {
			continue // unavailable album
		}
		albums = append(albums, provider.AlbumInfo{
			ID:         a.ID,
			Name:       a.Name,
			Artist:     artistNames(a.Artists),
			Year:       provider.YearFromDate(a.ReleaseDate),
			TrackCount: a.TotalTracks,
		})
	}
	return albums, nil
}
