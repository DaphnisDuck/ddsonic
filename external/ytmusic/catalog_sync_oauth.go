// ddsonic: whole-collection fetchers for the catalog sync in OAuth mode,
// through the YouTube Data API. Like the cookie fetchers, each returns
// catalog records carrying YouTube IDs or an error, never a partial
// collection. Kept in its own file so upstream merges stay conflict-free.

package ytmusic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/playlist"
)

// OAuthCatalog reads a YouTube account for the catalog through the Data
// API, signed in with the stored OAuth credentials. It never signs in
// interactively: without stored credentials, or when Google refuses them,
// it fails with playlist.ErrNeedsAuth.
type OAuthCatalog struct {
	clientID, clientSecret string
	// service returns the API client and the account's classification
	// cache scope; replaced in tests.
	service func(ctx context.Context) (*youtube.Service, string, error)

	// The access token is kept and shared by every read, so a sync signs
	// in when it expires rather than once per read (a sync reads each
	// playlist on its own). Each read builds its service on it with a token
	// source on the read's own context, so a refresh is cancelled with the
	// read and nothing outlives its caller. It starts over when the stored
	// sign-in changes.
	mu       sync.Mutex
	token    *oauth2.Token
	tokenFor string // the stored refresh token token comes from
	// apiOptions are added to each read's service; tests point it at a
	// fake Data API.
	apiOptions []option.ClientOption
}

// NewOAuthCatalog returns an OAuthCatalog for the client.
func NewOAuthCatalog(clientID, clientSecret string) *OAuthCatalog {
	c := &OAuthCatalog{clientID: clientID, clientSecret: clientSecret}
	c.service = c.silentService
	return c
}

func (c *OAuthCatalog) silentService(ctx context.Context) (*youtube.Service, string, error) {
	ts, scope, err := c.tokenSource(ctx)
	if err != nil {
		return nil, "", err
	}
	svc, err := youtube.NewService(ctx, append([]option.ClientOption{option.WithTokenSource(ts)}, c.apiOptions...)...)
	if err != nil {
		return nil, "", fmt.Errorf("youtube: create service: %w", err)
	}
	return svc, scope, nil
}

// tokenSource is a read's token source: the catalog's token, refreshed on
// ctx when it has expired.
func (c *OAuthCatalog) tokenSource(ctx context.Context) (oauth2.TokenSource, string, error) {
	creds, err := loadCreds()
	if err != nil || creds.RefreshToken == "" {
		return nil, "", fmt.Errorf("youtube: %w: no stored sign-in", playlist.ErrNeedsAuth)
	}
	c.mu.Lock()
	if c.token == nil || c.tokenFor != creds.RefreshToken {
		c.token, c.tokenFor = &oauth2.Token{RefreshToken: creds.RefreshToken}, creds.RefreshToken
	}
	token := c.token
	c.mu.Unlock()
	conf := googleOAuthConfig(c.clientID, c.clientSecret)
	ts := &catalogTokenSource{c: c, from: creds.RefreshToken,
		src: oauth2.ReuseTokenSource(token, conf.TokenSource(tokenContext(ctx), token))}
	return ts, oauthCacheScope(c.clientID, creds.RefreshToken), nil
}

// catalogTokenSource keeps each new access token for the next read, saves
// a refresh token Google rotated, and reports a refused grant as
// playlist.ErrNeedsAuth (through signInError) at whatever read hits it.
// It belongs to the sign-in it was made from: once that is no longer the
// catalog's (a new sign-in), it still serves its own read but neither
// shares its token nor saves anything, so it cannot undo the new sign-in.
type catalogTokenSource struct {
	c    *OAuthCatalog
	from string // the stored refresh token this source was made from
	src  oauth2.TokenSource
}

func (s *catalogTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.src.Token()
	if err != nil {
		err = signInError(err)
		if errors.Is(err, playlist.ErrNeedsAuth) {
			// Google's auth adapter rebuilds an oauth2.RetrieveError in its
			// own error type, dropping whatever wraps it; so the refusal goes
			// out as text, and ErrNeedsAuth reaches the read's caller.
			return nil, fmt.Errorf("%w: %v", playlist.ErrNeedsAuth, err)
		}
		return nil, err
	}
	s.c.mu.Lock()
	defer s.c.mu.Unlock()
	if s.from != s.c.tokenFor {
		return tok, nil // a newer sign-in replaced this one
	}
	if tok.RefreshToken != "" && tok.RefreshToken != s.from {
		// Google rotated the refresh token.
		if err := saveCreds(&storedCreds{RefreshToken: tok.RefreshToken}); err == nil {
			s.c.tokenFor, s.from = tok.RefreshToken, tok.RefreshToken
		}
	}
	s.c.token = tok
	return tok, nil
}

// signInError wraps a failed silent sign-in in playlist.ErrNeedsAuth only
// when signing in again would help: Google refused the stored grant (expired
// or revoked, as a Testing-status app's are weekly) or the client. Other
// failures, such as being offline, keep their cause.
func signInError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "invalid_grant", "invalid_client", "unauthorized_client":
			return fmt.Errorf("youtube: %w: %w", playlist.ErrNeedsAuth, err)
		}
	}
	return fmt.Errorf("youtube: sign in: %w", err)
}

// PlaylistRecords returns the account's music playlists, without tracks,
// classified as in cliamp's OAuth mode by a sampled video's category.
// Snapshot is the item count and the playlist's etag, so an unchanged
// playlist is not reread.
func (c *OAuthCatalog) PlaylistRecords(ctx context.Context, synced map[string]string) ([]catalog.PlaylistRecord, error) {
	svc, scope, err := c.service(ctx)
	if err != nil {
		return nil, err
	}
	type listed struct {
		entry playlistEntry
		etag  string
	}
	var all []listed
	for page := ""; ; {
		call := svc.Playlists.List([]string{"snippet", "contentDetails"}).Mine(true).MaxResults(50).Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("youtube: list playlists: %w", err)
		}
		for _, p := range resp.Items {
			if p.Id == "" || notOwnLists[p.Id] {
				continue
			}
			var count int
			if p.ContentDetails != nil {
				count = int(p.ContentDetails.ItemCount)
			}
			all = append(all, listed{playlistEntry{ID: p.Id, Name: p.Snippet.Title, TrackCount: count}, p.Etag})
		}
		if page = resp.NextPageToken; page == "" {
			break
		}
	}
	ids := make([]string, len(all))
	for i, l := range all {
		ids[i] = l.entry.ID
	}
	music, err := classifyForSync(ctx, svc, ids, scope)
	if err != nil {
		return nil, err
	}
	var records []catalog.PlaylistRecord
	for _, l := range all {
		// A synced playlist that cannot be classified yet stays.
		isMusic, known := music[l.entry.ID]
		if _, ok := synced[l.entry.ID]; isMusic || (!known && ok) {
			records = append(records, catalog.PlaylistRecord{
				Ref: youtubeRef(l.entry.ID), Name: l.entry.Name, Own: true, TrackCount: l.entry.TrackCount,
				Snapshot: fmt.Sprintf("%d:%s", l.entry.TrackCount, l.etag),
			})
		}
	}
	return records, nil
}

// classifyForSync reports which playlists are music, sharing cliamp's
// cache. Unlike cliamp's classifyPlaylists, a failed read is an error, so a
// sync keeps the playlists it has, and a playlist with no video to sample
// (empty, say) is left out uncached, so a later sync tries again.
func classifyForSync(ctx context.Context, svc *youtube.Service, ids []string, scope string) (map[string]bool, error) {
	music := loadClassification(scope)
	if music == nil {
		music = map[string]bool{}
	}
	sampled := map[string][]string{} // video ID → playlist IDs
	var videos []string
	for _, id := range ids {
		if _, known := music[id]; known {
			continue
		}
		resp, err := svc.PlaylistItems.List([]string{"contentDetails"}).PlaylistId(id).MaxResults(1).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("youtube: classify playlist %s: %w", id, apiError(err))
		}
		if len(resp.Items) == 0 || resp.Items[0].ContentDetails == nil || resp.Items[0].ContentDetails.VideoId == "" {
			continue
		}
		// Playlists can start with the same video: each takes its category.
		v := resp.Items[0].ContentDetails.VideoId
		if _, dup := sampled[v]; !dup {
			videos = append(videos, v)
		}
		sampled[v] = append(sampled[v], id)
	}
	if len(videos) == 0 {
		return music, nil
	}
	for batch := range slices.Chunk(videos, youtubeAPIBatchSize) {
		resp, err := svc.Videos.List([]string{"snippet"}).Id(batch...).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("youtube: classify playlists: %w", err)
		}
		// A sampled video the API no longer returns leaves its playlist
		// unknown.
		for _, v := range resp.Items {
			if v.Snippet == nil {
				continue
			}
			for _, id := range sampled[v.Id] {
				music[id] = v.Snippet.CategoryId == musicCategoryID
			}
		}
	}
	saveClassification(scope, music)
	return music, nil
}

// PlaylistRecord returns one playlist, by ID, without tracks: for a
// playlist someone else owns, which no list of yours includes. A playlist
// the API does not return fails with an error wrapping catalog.ErrForbidden.
func (c *OAuthCatalog) PlaylistRecord(ctx context.Context, playlistID string) (catalog.PlaylistRecord, error) {
	svc, _, err := c.service(ctx)
	if err != nil {
		return catalog.PlaylistRecord{}, err
	}
	resp, err := svc.Playlists.List([]string{"snippet", "contentDetails"}).Id(playlistID).Context(ctx).Do()
	if err != nil {
		return catalog.PlaylistRecord{}, fmt.Errorf("youtube: playlist %s: %w", playlistID, apiError(err))
	}
	if len(resp.Items) == 0 {
		return catalog.PlaylistRecord{}, fmt.Errorf("youtube: playlist %s: %w", playlistID, catalog.ErrForbidden)
	}
	p := resp.Items[0]
	var count int
	if p.ContentDetails != nil {
		count = int(p.ContentDetails.ItemCount)
	}
	return catalog.PlaylistRecord{Ref: youtubeRef(playlistID), Name: p.Snippet.Title, TrackCount: count,
		Snapshot: fmt.Sprintf("%d:%s", count, p.Etag)}, nil
}

// PlaylistTrackRecords returns a playlist's tracks in order. A playlist
// that is gone or private fails with an error wrapping catalog.ErrForbidden.
func (c *OAuthCatalog) PlaylistTrackRecords(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error) {
	return c.playlistTracks(ctx, playlistID)
}

// LikedTrackRecords returns the account's Liked Music.
func (c *OAuthCatalog) LikedTrackRecords(ctx context.Context) ([]catalog.TrackRecord, error) {
	return c.playlistTracks(ctx, likedMusicID)
}

func (c *OAuthCatalog) playlistTracks(ctx context.Context, playlistID string) ([]catalog.TrackRecord, error) {
	svc, _, err := c.service(ctx)
	if err != nil {
		return nil, err
	}
	var tracks []catalog.TrackRecord
	var items []itemInfo
	read, total := 0, -1
	for page := ""; ; {
		call := svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).PlaylistId(playlistID).MaxResults(50).Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("youtube: playlist %s: %w", playlistID, apiError(err))
		}
		if resp.PageInfo != nil {
			if total >= 0 && int(resp.PageInfo.TotalResults) != total {
				return nil, fmt.Errorf("youtube: playlist %s: %w", playlistID, ErrIncomplete)
			}
			total = int(resp.PageInfo.TotalResults)
		}
		// Every item counts toward completeness, playable or not.
		read += len(resp.Items)
		for _, it := range resp.Items {
			rec, ok := itemRecord(it)
			if !ok {
				continue
			}
			tracks = append(tracks, rec)
			items = append(items, itemInfo{videoID: rec.Ref.ProviderID})
		}
		if page = resp.NextPageToken; page == "" || len(resp.Items) == 0 {
			break
		}
	}
	if total >= 0 && read != total {
		return nil, fmt.Errorf("youtube: playlist %s: read %d of %d: %w", playlistID, read, total, ErrIncomplete)
	}
	// Durations are a separate call per 50 videos; one that fails leaves
	// its tracks' durations unknown rather than failing the playlist.
	// fetchDurations never reads its receiver, so a nil one serves.
	durations := (*baseProvider)(nil).fetchDurations(ctx, svc, items)
	for i := range tracks {
		tracks[i].Duration = time.Duration(durations[tracks[i].Ref.ProviderID]) * time.Second
	}
	return tracks, nil
}

// itemRecord maps a playlist item. Private and deleted videos have no
// track.
func itemRecord(it *youtube.PlaylistItem) (catalog.TrackRecord, bool) {
	if it == nil || it.Snippet == nil || it.ContentDetails == nil || it.ContentDetails.VideoId == "" {
		return catalog.TrackRecord{}, false
	}
	title := it.Snippet.Title
	if title == "Private video" || title == "Deleted video" {
		return catalog.TrackRecord{}, false
	}
	id := it.ContentDetails.VideoId
	if !validID(id) {
		return catalog.TrackRecord{}, false
	}
	rec := catalog.TrackRecord{Ref: youtubeRef(id), Title: title, PlayableURI: watchURL(id)}
	if artist, ok := channelArtist(it.Snippet.VideoOwnerChannelId, it.Snippet.VideoOwnerChannelTitle); ok {
		rec.Artists = []catalog.ArtistRecord{artist}
	}
	if added, err := time.Parse(time.RFC3339, it.Snippet.PublishedAt); err == nil {
		rec.AddedAt = added
	}
	return rec, true
}

// apiError marks a playlist the API will not show (gone, or private to
// someone else) as catalog.ErrForbidden, so a sync keeps what it had. A 403
// for running out of quota says nothing about the playlist: it stays a
// plain error, which fails the collection rather than dropping a playlist.
func apiError(err error) error {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return err
	}
	if gerr.Code == http.StatusNotFound || gerr.Code == http.StatusForbidden && !quotaError(gerr) {
		return fmt.Errorf("%w: %w", catalog.ErrForbidden, err)
	}
	return err
}

// quotaError reports whether a Data API error is a quota or rate limit.
func quotaError(gerr *googleapi.Error) bool {
	for _, e := range gerr.Errors {
		switch e.Reason {
		case "quotaExceeded", "dailyLimitExceeded", "rateLimitExceeded", "userRateLimitExceeded":
			return true
		}
	}
	return false
}
