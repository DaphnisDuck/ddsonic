package provider

// ddsonic: one user action on a provider's account.

import (
	"context"
	"errors"
	"fmt"

	"github.com/bjarneo/cliamp/playlist"
)

// ActionScoper is implemented by providers whose writes belong to the
// sign-in that stood when the user asked for them (Spotify). WithSignIn marks
// a context as one user action, begun now. The provider then sends none of
// that action's writes under a sign-in that replaced that one, whether the
// sign-in comes before the action's first write or between two of them. It
// does not undo what the action had written, and does not resume it.
type ActionScoper interface {
	WithSignIn(ctx context.Context) context.Context
}

// ActionContext is the context for one user action on p's account: every
// write the action makes is given it. Whoever takes up a request that writes
// (one track added, several, a playlist created, or created and then added
// to) calls it once, when the request arrives and before anything is waited
// for, and passes the result to every write. For a provider that does not
// scope its writes it is ctx.
func ActionContext(ctx context.Context, p any) context.Context {
	if scoper, ok := p.(ActionScoper); ok {
		return scoper.WithSignIn(ctx)
	}
	return ctx
}

// ErrNotApplied marks a write that certainly did not take effect: it was not
// sent, or the provider refused it. A write that failed in any other way (a
// timeout, a dropped connection, an unreadable answer) may have been applied.
var ErrNotApplied = errors.New("the change was not made")

// NotApplied reports whether err says for certain that a write did not take
// effect: it was never sent or was refused (ErrNotApplied), or the sign-in
// was required before it could be (playlist.ErrNeedsAuth).
func NotApplied(err error) bool {
	return errors.Is(err, ErrNotApplied) || errors.Is(err, playlist.ErrNeedsAuth)
}

// PartialError reports an action of several writes that stopped at one of
// them, after Done of Total. What was written stays written: nothing is
// undone, and nothing is written again unless the user asks. Unconfirmed
// says the write it stopped at may have been applied all the same.
type PartialError struct {
	Done, Total int
	Unconfirmed bool
	Err         error
}

func (e *PartialError) Error() string {
	next := "the next was not added"
	if e.Unconfirmed {
		next = "the next may or may not have been added (check the playlist)"
	}
	return fmt.Sprintf("added %d of %d tracks, which stay added; %s, and the rest were not tried: %v", e.Done, e.Total, next, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// AddTracks adds tracks to a playlist one by one, every one with ctx, which
// the caller made the request's action context (ActionContext) when the
// request arrived. It stops at the first failure and says what is known of
// how far it got (PartialError): always when a track had been added, and
// also for a first track whose add may have happened.
func AddTracks(ctx context.Context, w PlaylistWriter, playlistID string, tracks []playlist.Track) error {
	for i, track := range tracks {
		if err := w.AddTrackToPlaylist(ctx, playlistID, track); err != nil {
			unconfirmed := !NotApplied(err)
			if i == 0 && !unconfirmed {
				return err
			}
			return &PartialError{Done: i, Total: len(tracks), Unconfirmed: unconfirmed, Err: err}
		}
	}
	return nil
}
