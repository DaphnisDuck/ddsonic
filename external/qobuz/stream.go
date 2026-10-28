package qobuz

import "sync"

// streamURLs records the signed CDN URLs that the provider has resolved via
// track/getFileUrl. The player consults IsStreamURL through a registered
// buffered-URL matcher so Qobuz FLAC streams are routed through the
// buffer-while-playing + ffmpeg pipeline (which auto-detects the codec and
// supports seeking), exactly like Navidrome's raw streams.
var streamURLs sync.Map // map[string]struct{}

// registerStreamURL marks u as a Qobuz stream URL.
func registerStreamURL(u string) {
	if u == "" {
		return
	}
	streamURLs.Store(u, struct{}{})
}

// IsStreamURL reports whether u is a Qobuz signed stream URL previously
// resolved by the provider. It is registered with the player's buffered-URL
// matcher in main.go.
func IsStreamURL(u string) bool {
	_, ok := streamURLs.Load(u)
	return ok
}
