// ddsonic: settings for ddsonic' own features, from the [ddsonic] section of
// config.toml. Load hands the section's keys to parseKey through one tagged
// hook, so upstream merges of config.go stay cheap.

package config

import "time"

// Defaults for how old a source's last sync may get before startup syncs
// again: Spotify rate-limits development-mode apps, YouTube's reads cost
// quota or yt-dlp time; `r` syncs on demand.
const (
	DefaultSpotifyRefresh = 2 * time.Hour
	DefaultYouTubeRefresh = 2 * time.Hour
)

// DdsonicConfig is the [ddsonic] section.
type DdsonicConfig struct {
	// SpotifyRefresh is how old the last successful Spotify sync may get
	// before startup syncs again; 0 syncs at every startup.
	SpotifyRefresh time.Duration
	// YouTubeRefresh is the same for YouTube Music.
	YouTubeRefresh time.Duration
	// YouTubePlaylists are other people's playlists to sync, as links or
	// IDs: YouTube lists the playlists you save nowhere a sync can read.
	YouTubePlaylists []string
	// Border draws a border around the screen and rules between its parts.
	Border bool
	// Artwork shows album artwork in the track info view, in terminals
	// that can draw images.
	Artwork bool
}

func defaultDdsonicConfig() DdsonicConfig {
	return DdsonicConfig{SpotifyRefresh: DefaultSpotifyRefresh, YouTubeRefresh: DefaultYouTubeRefresh, Border: true, Artwork: true}
}

// parseKey applies one key of the [ddsonic] section. Invalid values keep
// the default.
func (c *DdsonicConfig) parseKey(key, val string) {
	switch key {
	case "spotify_refresh":
		setDuration(&c.SpotifyRefresh, val)
	case "youtube_refresh":
		setDuration(&c.YouTubeRefresh, val)
	case "youtube_playlists":
		c.YouTubePlaylists = parseStringSlice(val)
	case "border":
		setBool(&c.Border, val)
	case "artwork":
		setBool(&c.Artwork, val)
	}
}

// setDuration sets d from a duration value, keeping d when it is invalid
// or negative.
func setDuration(d *time.Duration, val string) {
	if v, err := time.ParseDuration(parseString(val)); err == nil && v >= 0 {
		*d = v
	}
}

// setBool sets b from a true or false value, keeping b otherwise.
func setBool(b *bool, val string) {
	switch parseString(val) {
	case "true":
		*b = true
	case "false":
		*b = false
	}
}
