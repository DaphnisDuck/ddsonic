package model

// ddsonic: f favorites and unfavorites the cliamp radio channel the library
// has selected (issue #135): a channel row in Browse Stations > cliamp
// radio, a channel favorite in Radio > Favorites or in search's Stations.
// The channel favorites are their own store (radio.ChannelFavorites), apart
// from the station favorites; saving runs as a command, as a station's does
// (library_favorite.go).

import (
	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/library"
)

// SetChannelFavorites shares the channel favorites store the library's
// Favorites and search read.
func (m *Model) SetChannelFavorites(favorites *radio.ChannelFavorites) {
	m.lib.channelFavs = favorites
}

// libSelectedChannel is the channel of the library row selected in front,
// or nil: the queue has no channel rows, and a list that is loading or
// failed selects none.
func (m Model) libSelectedChannel() *library.ChannelRef {
	if m.lib.channelFavs == nil || !m.libraryEnabled() || !m.lib.visible {
		return nil
	}
	f := m.libTop()
	if f.loading() || f.err != nil || f.cursor < 0 || f.cursor >= len(f.entries) {
		return nil
	}
	return f.entries[f.cursor].Channel
}

// libChannelFavoriteKeys is f's row for a selected channel; ok is false when
// no channel is selected. A channel whose toggle is still being saved has
// no f until then.
func (m Model) libChannelFavoriteKeys() (keys []libKeyHelp, ok bool) {
	c := m.libSelectedChannel()
	switch {
	case c == nil:
		return nil, false
	case m.lib.chanSaving[c.ID]:
		return nil, true
	case m.lib.channelFavs.Contains(c.ID):
		return []libKeyHelp{libUnfavoriteKey}, true
	}
	return []libKeyHelp{libFavoriteKey}, true
}

// libChannelFavoriteSavedMsg reports the end of a channel favorite's save.
type libChannelFavoriteSavedMsg struct {
	channel library.ChannelRef
	added   bool
	err     error
}

// libToggleChannelFavorite starts the save of the selected channel's
// favorite, by its ID under the name the row shows; ok is false when no
// channel is selected. A second f before the save lands is ignored.
func (m *Model) libToggleChannelFavorite() (cmd tea.Cmd, ok bool) {
	c := m.libSelectedChannel()
	if c == nil {
		return nil, false
	}
	if m.lib.chanSaving[c.ID] {
		return nil, true
	}
	if m.lib.chanSaving == nil {
		m.lib.chanSaving = map[string]bool{}
	}
	m.lib.chanSaving[c.ID] = true
	favorites, channel := m.lib.channelFavs, *c
	return func() tea.Msg {
		added, err := favorites.Toggle(radio.ChannelFavorite{ID: channel.ID, Name: channel.Name})
		return libChannelFavoriteSavedMsg{channel: channel, added: added, err: err}
	}, true
}

// libChannelFavoriteSaved shows the outcome and rereads Favorites and
// search, which list the channel favorites. A failed save changed nothing.
func (m *Model) libChannelFavoriteSaved(msg libChannelFavoriteSavedMsg) tea.Cmd {
	delete(m.lib.chanSaving, msg.channel.ID)
	switch {
	case msg.err != nil:
		m.status.Errorf(statusTTLDefault, "Favorite save failed: %s", msg.err)
		return nil
	case msg.added:
		m.status.Showf(statusTTLMedium, "Favorited channel: %s", msg.channel.Name)
	default:
		m.status.Showf(statusTTLMedium, "Removed channel: %s", msg.channel.Name)
	}
	return m.libraryCatalogChanged(catalog.Radio)
}
