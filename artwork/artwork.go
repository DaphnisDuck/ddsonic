// Package artwork resolves a track to its album artwork and loads it: from a
// remote URL through an on-disk cache, from a local image file, or from a
// local audio file's embedded picture or its folder's cover file. It knows
// nothing of terminals or layout; the UI asks it for an image.
package artwork

import (
	"errors"
	"net/url"
	"strings"

	"github.com/bjarneo/cliamp/playlist"
)

// ErrNone means the track has no artwork ddsonic can find.
var ErrNone = errors.New("no artwork")

// Ref locates a track's artwork. Exactly one of URL, File and Audio is set;
// Key identifies the artwork for caches and for matching a load's result to
// the track it was for.
type Ref struct {
	Key   string
	URL   string // a remote image
	File  string // a local image file
	Audio string // a local audio file: its embedded picture, or its folder's cover file
}

// IsZero reports whether r locates no artwork.
func (r Ref) IsZero() bool { return r.Key == "" }

// Resolve returns where t's artwork is, or the zero Ref. Providers record
// artwork as the track's AlbumArtURL (an http(s) URL, or a file:// URL for
// embedded art cliamp extracted); a local file with none is looked up from
// the file itself when loaded. Streams and tracks addressed by a URI (a
// provider's, like spotify:track:…) have none unless their provider set it.
func Resolve(t playlist.Track) Ref {
	art := strings.TrimSpace(t.AlbumArtURL)
	switch {
	case strings.HasPrefix(art, "https://"), strings.HasPrefix(art, "http://"):
		return Ref{Key: art, URL: art}
	case strings.HasPrefix(art, "file://"):
		if u, err := url.Parse(art); err == nil && u.Path != "" {
			return Ref{Key: "file:" + u.Path, File: u.Path}
		}
	}
	if t.Path != "" && !t.Stream && !hasScheme(t.Path) {
		return Ref{Key: "audio:" + t.Path, Audio: t.Path}
	}
	return Ref{}
}

// hasScheme reports whether path starts with a URI scheme ("spotify:",
// "https:"), as a file path does not. A one-letter scheme is a Windows
// drive.
func hasScheme(path string) bool {
	scheme, _, ok := strings.Cut(path, ":")
	if !ok || len(scheme) < 2 {
		return false
	}
	for i, r := range scheme {
		letter := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || !(r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.')) {
			return false
		}
	}
	return true
}
