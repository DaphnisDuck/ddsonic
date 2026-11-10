// Package library is ddsonic' application-owned navigation model: the
// Music → source → concept → item hierarchy the UI walks. It knows nothing
// about Bubbletea; ui/model renders Levels and acts on Entries.
//
// Providers own content and capabilities; adapters in this package translate
// those capabilities (provider/interfaces.go) into Levels. From Milestone 2 the
// adapters read the catalog instead of calling providers, and the UI does not
// change — Level is that seam.
package library

import (
	"context"

	"github.com/bjarneo/cliamp/playlist"
)

// Level is one screen of the navigation hierarchy.
type Level interface {
	// Title names the level in the breadcrumb, e.g. "Albums".
	Title() string
	// Load returns the level's rows. It may block on the network and is
	// always called off the UI goroutine. Cancelling ctx is best-effort until
	// Milestone 2: provider interfaces take no context, so an abandoned
	// provider call runs to its own timeout and its result is discarded.
	Load(ctx context.Context) ([]Entry, error)
}

// AuthLevel is implemented by levels whose Load can fail with
// playlist.ErrNeedsAuth; the UI uses the authenticator to sign in and retry.
type AuthLevel interface {
	Authenticator() playlist.Authenticator
}

// SourceMenu is implemented by a synced source's own menu (Spotify's
// Albums, Artists, Playlists, Liked Songs). The UI offers sign-in there when
// the source needs it, whatever the catalog holds.
type SourceMenu interface {
	SourceProvider() string
}

// CatalogLevel is implemented by levels read from the catalog. The UI
// reloads them when a sync of CatalogProvider changes the catalog; an empty
// CatalogProvider lists every source and reloads after any sync.
type CatalogLevel interface {
	CatalogProvider() string
}

// CachedLevel is implemented by levels beneath the catalog lists whose rows
// the catalog backs but whose Load may also reach the provider: an album not
// cached yet, a playlist the sync could not read. When a sync of
// CachedProvider changes the catalog, the UI rereads them with LoadCached,
// which reads the catalog alone, so a sync never sets off live calls. ok
// false means the rows did not come from the catalog (a live fallback), and
// they stay as they are.
type CachedLevel interface {
	CachedProvider() string
	LoadCached(ctx context.Context) (entries []Entry, ok bool, err error)
}

// OrderedLevel is implemented by levels that can list their rows in more
// than one order. The UI's order key calls NextOrder and reloads the level.
type OrderedLevel interface {
	// OrderName describes the current order, e.g. "by title".
	OrderName() string
	// NextOrder switches to the next order and returns its name.
	NextOrder() string
}

// NotedLevel is implemented by levels whose title alone could mislead about
// what their rows are. The UI shows the note above the rows, in a populated
// list and an empty one alike, each line wrapped to the panel. No lines
// means no note.
type NotedLevel interface {
	Note() []string
}

// Intent is a UI action an entry requests instead of navigating.
type Intent int

const (
	IntentNone Intent = iota
	// IntentSearch opens the provider's own live search: with Query set,
	// already run for it (search's "Search Spotify for …" row); without, the
	// search screen of a library that has no catalog.
	IntentSearch
	// IntentFolders opens the file browser.
	IntentFolders
	// IntentSignIn signs in to the entry's Source, a synced source whose
	// player can sign in from the library (SourceMenu).
	IntentSignIn
	// IntentPending marks the row that stands in for a synced list with
	// nothing cached yet. Its Source is the list's catalog provider; the UI
	// says why the list is empty (still syncing, sign-in required, the sync
	// failed) where it knows.
	IntentPending
)

// Entry is one row of a Level. Exactly one action field is set, except that
// a Track row may also have PlayFrom.
type Entry struct {
	// ID identifies the row across reloads, so a refreshed list keeps the
	// cursor on the same item. Empty for rows without a stable identity.
	ID    string
	Title string
	// Detail is secondary text shown right-aligned (artist, count, year).
	Detail string
	// Section groups consecutive rows under a heading. Empty means none.
	Section string
	// Favorite marks a pinned or starred row; the UI draws the marker.
	Favorite bool

	// Open drills into a child level.
	Open Level
	// Track plays this track, with the level's other tracks as context.
	Track *playlist.Track
	// PlayFrom, set on a Track row, replaces that context: Enter plays the
	// tracks it returns from index (a searched track plays its album).
	PlayFrom func(ctx context.Context) (tracks []playlist.Track, index int, err error)
	// Play resolves tracks and plays them from the first, replacing the
	// queue. Used for rows that are playable but not browsable (stations).
	Play func(ctx context.Context) ([]playlist.Track, error)
	// FavoriteTrack, when set, is the station f stars on this row (a live
	// cliamp radio channel, #135). It is for favorites only: Enter and
	// playback never read it.
	FavoriteTrack *playlist.Track
	// Intent asks the UI to do something outside the hierarchy.
	Intent Intent
	// Provider owns the entry's content; set for Intent rows that need one.
	Provider playlist.Provider
	// Query is the text an IntentSearch row searches for.
	Query string
	// Source is the catalog provider (catalog.Spotify, catalog.Radio, …)
	// whose content the row holds. Rows beneath inherit it, so it is set
	// only where the source is decided: a source's menu entry, and the rows
	// of lists that mix sources (All Music, search).
	Source string
}

// Tracks returns the tracks of the Track entries in entries, in order, and the
// index in that slice of entries[i] (or -1 when entries[i] is not a track).
func Tracks(entries []Entry, i int) ([]playlist.Track, int) {
	var tracks []playlist.Track
	at := -1
	for j, e := range entries {
		if e.Track == nil {
			continue
		}
		if j == i {
			at = len(tracks)
		}
		tracks = append(tracks, *e.Track)
	}
	return tracks, at
}

// funcLevel is a Level backed by a load function.
type funcLevel struct {
	title string
	load  func(ctx context.Context) ([]Entry, error)
}

func (l *funcLevel) Title() string { return l.title }

func (l *funcLevel) Load(ctx context.Context) ([]Entry, error) { return l.load(ctx) }

// signInLevel is a funcLevel whose provider can sign in interactively.
type signInLevel struct {
	funcLevel
	auth playlist.Authenticator
}

func (l *signInLevel) Authenticator() playlist.Authenticator { return l.auth }

// NewLevel returns a Level whose rows come from load.
func NewLevel(title string, load func(ctx context.Context) ([]Entry, error)) Level {
	return &funcLevel{title: title, load: load}
}

// providerLevel is NewLevel for provider-backed levels: it implements
// AuthLevel when prov can sign in interactively.
func providerLevel(title string, prov playlist.Provider, load func(ctx context.Context) ([]Entry, error)) Level {
	if a, ok := prov.(playlist.Authenticator); ok {
		return &signInLevel{funcLevel{title, load}, a}
	}
	return NewLevel(title, load)
}

// Menu returns a Level with fixed rows.
func Menu(title string, entries ...Entry) Level {
	return NewLevel(title, func(context.Context) ([]Entry, error) { return entries, nil })
}

// TrackLevel returns a Level listing tracks, each playable in context.
func TrackLevel(title string, prov playlist.Provider, load func(ctx context.Context) ([]playlist.Track, error)) Level {
	return providerLevel(title, prov, func(ctx context.Context) ([]Entry, error) {
		tracks, err := load(ctx)
		if err != nil {
			return nil, err
		}
		return trackEntries(tracks), nil
	})
}

func trackEntries(tracks []playlist.Track) []Entry {
	entries := make([]Entry, len(tracks))
	for i := range tracks {
		entries[i] = Entry{Title: tracks[i].DisplayName(), Track: &tracks[i]}
	}
	return entries
}
