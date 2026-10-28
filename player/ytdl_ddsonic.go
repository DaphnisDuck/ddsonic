package player

// ddsonic: upstream 70e5f79e's clock for a yt-dlp seek by restart, ported
// without the player refactors it sits on upstream.

import (
	"time"

	"github.com/bjarneo/cliamp/applog"
)

// ytdlPlayedPosition is the position of the audio that played. A prefetched
// yt-dlp decoder reads ahead of the speaker, so a seek by restart counts from
// the prefetch, as Position does. The caller holds the speaker lock.
// ytdlRelease is how removing a playback run's temporary directory went. err
// is written before the run's done channel closes and read after.
type ytdlRelease struct{ err error }

// result is the error of the cleanup, nil for a streamer built without one.
func (r *ytdlRelease) result() error {
	if r == nil {
		return nil
	}
	return r.err
}

// reportYTDLRelease tells the user of a temporary directory that could not
// be removed, since most of playback's callers have nowhere to return it.
func reportYTDLRelease(err error) error {
	if err != nil {
		applog.UserError("%v", err)
	}
	return err
}

// afterExit returns a channel that closes once exited has and f has run.
func afterExit(exited <-chan struct{}, f func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-exited
		f()
	}()
	return done
}

func ytdlPlayedPosition(cur *trackPipeline) time.Duration {
	if cur.livePrefetch != nil {
		return cur.livePrefetch.Position() + cur.streamOffset
	}
	return cur.format.SampleRate.D(cur.decoder.Position()) + cur.streamOffset
}
