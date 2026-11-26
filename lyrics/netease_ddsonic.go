package lyrics

// ddsonic: the NetEase Cloud Music lookup is off unless the config turns it
// on ([ddsonic] netease_lyrics = true, #136). It goes over plain HTTP, so
// the artist and title it searches for, and the lyrics it gets back, cross
// the network unencrypted, readable and changeable by anyone on the way.

import "sync/atomic"

var netEaseFallback atomic.Bool

// SetNetEaseFallback turns the NetEase lookup, after LRCLIB finds nothing,
// on or off. It is off until set.
func SetNetEaseFallback(on bool) { netEaseFallback.Store(on) }
