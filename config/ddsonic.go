// ddsonic: settings for ddsonic' own features, from the [ddsonic] section of
// config.toml. Load hands the section's keys to parseKey through one tagged
// hook, so upstream merges of config.go stay cheap.

package config

import (
	"strconv"
	"time"
)

// Defaults for how old a source's last sync may get before startup syncs
// again: Spotify rate-limits development-mode apps, YouTube's reads cost
// quota or yt-dlp time; `r` syncs on demand.
const (
	DefaultSpotifyRefresh = 2 * time.Hour
	DefaultYouTubeRefresh = 2 * time.Hour
	// DefaultSleepFade is the sleep timer's fade-out unless the config
	// says otherwise.
	DefaultSleepFade = 30 * time.Second
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
	// NetEaseLyrics looks lyrics up on NetEase Cloud Music when LRCLIB has
	// none. Off by default: the lookup is plain HTTP, so the artist and
	// title go over the network unencrypted (#136).
	NetEaseLyrics bool
	// SleepFade is how long the sleep timer fades out before it pauses
	// playback; 0 pauses at once (sleep_fade_seconds, #35).
	SleepFade time.Duration
}

func defaultDdsonicConfig() DdsonicConfig {
	return DdsonicConfig{SpotifyRefresh: DefaultSpotifyRefresh, YouTubeRefresh: DefaultYouTubeRefresh, Border: true, Artwork: true, SleepFade: DefaultSleepFade}
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
	case "netease_lyrics":
		setBool(&c.NetEaseLyrics, val)
	case "sleep_fade_seconds":
		setSeconds(&c.SleepFade, val)
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

// setSeconds sets d from a whole number of seconds, keeping d when it is
// invalid or negative.
func setSeconds(d *time.Duration, val string) {
	if v, err := strconv.Atoi(parseString(val)); err == nil && v >= 0 {
		*d = time.Duration(v) * time.Second
	}
}
