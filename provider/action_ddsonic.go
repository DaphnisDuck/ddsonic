package provider

// ddsonic: one user action on a provider's account.

import (
	"context"
	"fmt"

	"github.com/bjarneo/cliamp/playlist"
)

// ActionScoper is implemented by providers whose writes belong to the
// sign-in they were asked under (Spotify). WithSignIn marks a context as one
// user action; the provider then refuses to continue that action under a
// sign-in that replaced the one it began on.
type ActionScoper interface {
	WithSignIn(ctx context.Context) context.Context
}

// ActionContext is the context for one user action on p's account: every
// write the action makes is given it. Whoever makes more than one write for
// one request (several tracks added, a playlist created and then added to)
// calls it once, before the first. For a provider that does not scope its
// writes it is ctx.
func ActionContext(ctx context.Context, p any) context.Context {
	if scoper, ok := p.(ActionScoper); ok {
		return scoper.WithSignIn(ctx)
	}
	return ctx
}

// PartialError reports an action that stopped after done of total writes.
// What was written stays written: nothing is undone, and nothing is written
// again unless the user asks.
type PartialError struct {
	Done, Total int
	Err         error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("added %d of %d tracks, which stay added; then stopped: %v", e.Done, e.Total, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// AddTracks adds tracks to a playlist one by one as one action. It stops at
// the first failure and says how far it got (PartialError) when it had begun.
func AddTracks(ctx context.Context, w PlaylistWriter, playlistID string, tracks []playlist.Track) error {
	ctx = ActionContext(ctx, w)
	for i, track := range tracks {
		if err := w.AddTrackToPlaylist(ctx, playlistID, track); err != nil {
			if i == 0 {
				return err
			}
			return &PartialError{Done: i, Total: len(tracks), Err: err}
		}
	}
	return nil
}
