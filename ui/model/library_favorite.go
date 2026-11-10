package model

// ddsonic: f stars and unstars the directory station the view in front has
// selected: the highlighted row of a station list (a country, a tag, the
// directory search, Favorites, search results), or the queue's selected
// track while the queue has the screen. It is the library's own key: the
// gate never hands f to cliamp's star, which acts on the queue's selection
// whatever is on screen. The favorites are cliamp's store, shared with the
// radio provider (SetRadioFavorites). Saving waits for a file lock and the
// disk, so it never runs on the UI goroutine: f starts a command, and
// libFavoriteSavedMsg brings the outcome.

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/playlist"
)

// f's row in a view's key table, by what it would do to the selection.
var (
	libFavoriteKey   = libKeyHelp{[]string{"f"}, "f", "Favorite"}
	libUnfavoriteKey = libKeyHelp{[]string{"f"}, "f", "Unfavorite"}
)

// libSelectedStation is the directory station, or live cliamp radio
// channel (#135), selected in the view in front, as the directory or the
// channel list names it: never a row's decorated title or the song a stream
// is playing. ok is false for everything else (a menu row, a cliamp radio
// song channel, a radios.toml station, any other stream or file) and on a
// list that is loading or failed. While the query is being typed
// no row is selected: the input takes f as text and its key table is its
// own (handleLibraryKey, libraryKeyView).
func (m Model) libSelectedStation() (station radio.CatalogStation, ok bool) {
	if m.radioFavorites == nil || !m.libraryEnabled() {
		return station, false
	}
	if !m.lib.visible {
		// The track says whether it is a directory station, however it was
		// queued. cliamp's own star (selectedPlaylistStarAction) leaves a
		// queue a provider loaded to its bookmarks; that rule is not the
		// library's.
		if m.playlist == nil {
			return station, false
		}
		track, ok := m.playlist.Track(m.plCursor)
		if !ok {
			return station, false
		}
		return radio.StationFromTrack(track)
	}
	f := m.libTop()
	if f.loading() || f.err != nil || f.cursor < 0 || f.cursor >= len(f.entries) {
		return station, false
	}
	if track := f.entries[f.cursor].Track; track != nil {
		return radio.StationFromTrack(*track)
	}
	return station, false
}

// libFavoriteKeys is f's row for the selection, or none when the selection
// is not a directory station, or is one whose toggle is still being saved:
// f does nothing on it until then.
func (m Model) libFavoriteKeys() []libKeyHelp {
	station, ok := m.libSelectedStation()
	switch {
	case !ok || m.lib.favSaving[station.URL]:
		return nil
	case m.radioFavorites.Contains(station.URL):
		return []libKeyHelp{libUnfavoriteKey}
	}
	return []libKeyHelp{libFavoriteKey}
}

// libFavoriteSavedMsg reports the end of the save libToggleFavorite started.
type libFavoriteSavedMsg struct {
	station radio.CatalogStation
	added   bool
	err     error
}

// libToggleFavorite stars the selected station, or unstars it. The save
// waits for the favorites file, which another ddsonic may hold, and for the
// disk, so it runs as a command, on the station as selected now; this
// handler only starts it. What the listener sees changes when it lands
// (libFavoriteSaved). A second f on the same station before then is
// ignored: one press, one toggle. Other stations' toggles go ahead; the
// store takes them one at a time.
func (m *Model) libToggleFavorite() tea.Cmd {
	station, ok := m.libSelectedStation()
	if !ok || m.lib.favSaving[station.URL] {
		return nil
	}
	if m.lib.favSaving == nil {
		m.lib.favSaving = map[string]bool{}
	}
	m.lib.favSaving[station.URL] = true
	favorites := m.radioFavorites
	return func() tea.Msg {
		added, err := favorites.Toggle(station)
		return libFavoriteSavedMsg{station: station, added: added, err: err}
	}
}

// libFavoriteSaved shows the outcome of a save and rereads what lists
// radio's stations: the Favorites list at once, from the store, which has
// the change by now. The store has told the catalog, and search follows
// when that sync has written it (CatalogChangedMsg). A failed save changed
// nothing, in the store or on disk.
func (m *Model) libFavoriteSaved(msg libFavoriteSavedMsg) tea.Cmd {
	delete(m.lib.favSaving, msg.station.URL)
	switch {
	case msg.err != nil:
		m.status.Errorf(statusTTLDefault, "Favorite save failed: %s", msg.err)
		return nil
	case msg.added:
		m.status.Showf(statusTTLMedium, "Favorited station: %s", msg.station.Name)
	default:
		m.status.Showf(statusTTLMedium, "Removed station: %s", msg.station.Name)
	}
	return m.libraryCatalogChanged(catalog.Radio)
}

// The queue's star with the library on, where f stars a directory station
// in any queue. A queue the library played marks the favorites, as cliamp
// does. In a queue that a playlist or a provider loaded (over IPC), cliamp
// keeps the star for bookmarks; there the library adds the favorites to
// them: a track's bookmark keeps its star, and a starred directory station
// has one too. Nothing but radio.StationFromTrack makes a track a station.

// libLoadedStationStarred reports whether track is a starred directory
// station in such a loaded queue (playlistTrackStarred; the star count
// looks at each track of one, radioMarkerCache).
func (m Model) libLoadedStationStarred(track playlist.Track) bool {
	if !m.libraryEnabled() || m.loadedPlaylist == "" || m.radioFavorites == nil {
		return false
	}
	station, ok := radio.StationFromTrack(track)
	return ok && m.radioFavorites.Contains(station.URL)
}
