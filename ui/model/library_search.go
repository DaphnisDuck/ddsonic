package model

// ddsonic: the library's search screen. It is a frame on the library stack
// whose level is a library.SearchLevel: typing swaps in the level for the
// new query and reloads it after a short pause, so the usual load machinery
// supersedes stale results. Results open like any library row.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

// librarySearchDebounce lets key repeat and fast typing settle before a
// query runs; each query is local and takes a few milliseconds.
const librarySearchDebounce = 60 * time.Millisecond

// librarySearchField names the search input for the shared text editor.
const librarySearchField = "lib-search"

// librarySearchTickMsg runs the query typed before it, unless more typing
// has superseded it.
type librarySearchTickMsg struct{ gen uint64 }

// libSearchLevel returns the top frame's search level, if it is one.
func (m *Model) libSearchLevel() (library.SearchLevel, bool) {
	if !m.libraryEnabled() {
		return nil, false
	}
	sl, ok := m.libTop().level.(library.SearchLevel)
	return sl, ok
}

// librarySearch opens search from anywhere in the library, with the input
// focused. It returns to a search already on the stack rather than stacking
// another; without a catalog it opens the provider's own search.
func (m *Model) librarySearch() tea.Cmd {
	for i, f := range m.lib.stack {
		if _, ok := f.level.(library.SearchLevel); ok {
			var cmd tea.Cmd
			for len(m.lib.stack) > i+1 {
				cmd = m.libraryPop() // only the last, the new top's, matters
			}
			m.lib.searchInput = true
			return cmd
		}
	}
	for _, e := range m.lib.stack[0].entries {
		if sl, ok := e.Open.(library.SearchLevel); ok {
			return m.libraryOpenSearch(sl)
		}
		if e.Intent == library.IntentSearch {
			m.openProviderSearchWith(e.Provider)
			return nil
		}
	}
	return nil
}

// libraryOpenSearch pushes a search frame showing the last query's results.
func (m *Model) libraryOpenSearch(sl library.SearchLevel) tea.Cmd {
	m.lib.searchInput = true
	level := sl.WithQuery(m.lib.lastQuery)
	if m.lib.lastQuery == "" {
		m.lib.stack = append(m.lib.stack, libFrame{level: level})
		return nil
	}
	return m.libraryPush(level, "") // search mixes sources; each row names its own
}

// handleLibrarySearchInput edits the query while the input has focus.
func (m *Model) handleLibrarySearchInput(msg tea.KeyPressMsg, sl library.SearchLevel) tea.Cmd {
	f := m.libTop()
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.lib.searchInput = false
		return m.libraryPop()
	case "enter", "down", "tab":
		// A query still settling runs now, so the results focused are its
		// own and no late reload moves the cursor.
		if m.lib.searchPending {
			m.lib.searchPending = false
			nextRequest(&m.lib.searchGen) // drops the pending tick
			m.lib.searchInput = false
			f.cursor, f.scroll = 0, 0
			return m.libraryLoad()
		}
		if len(f.entries) > 0 {
			m.lib.searchInput = false
			f.cursor, f.scroll = 0, 0
			m.libAdjustScroll()
		}
		return nil
	}
	query := sl.Query()
	if !m.editText(librarySearchField, &query, msg) || query == sl.Query() {
		return nil
	}
	m.lib.lastQuery = query
	f.level = sl.WithQuery(query)
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
	f.gen = nextRequest(&m.lib.gen) // drops a load of the old query
	if query == "" {
		m.lib.searchPending = false
		f.err, f.cursor, f.scroll = nil, 0, 0
		f.setEntries(nil)
		return nil
	}
	m.lib.searchPending = true
	gen := nextRequest(&m.lib.searchGen)
	return tea.Tick(librarySearchDebounce, func(time.Time) tea.Msg { return librarySearchTickMsg{gen: gen} })
}

// handleLibrarySearchTick runs the settled query.
func (m *Model) handleLibrarySearchTick(msg librarySearchTickMsg) tea.Cmd {
	if msg.gen != m.lib.searchGen {
		return nil
	}
	m.lib.searchPending = false
	if _, ok := m.libSearchLevel(); !ok {
		return nil
	}
	f := m.libTop()
	f.cursor, f.scroll = 0, 0
	return m.libraryLoad()
}

// libraryPlayFrom plays what a row's PlayFrom resolves (a searched track's
// album), from the index it names.
func (m *Model) libraryPlayFrom(e library.Entry, source string) tea.Cmd {
	gen := nextRequest(&m.lib.gen)
	m.lib.playGen = gen
	playFrom, title, parent := e.PlayFrom, e.Title, m.libContext()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, libraryLoadTimeout)
		defer cancel()
		tracks, index, err := playFrom(ctx)
		return libraryPlayMsg{gen: gen, title: title, source: source, tracks: tracks, index: index, err: err}
	}
}

// openProviderSearchQuery opens the provider's own search already running
// query, for search's "Search Spotify for …" row.
func (m *Model) openProviderSearchQuery(prov playlist.Provider, query string) tea.Cmd {
	m.openProviderSearchWith(prov)
	s, ok := prov.(provider.Searcher)
	if !ok || !m.spotSearch.visible || query == "" {
		return nil
	}
	m.spotSearch.query = query
	m.spotSearch.loading = true
	return fetchSpotSearchCmd(m.newSpotRequestContext(30*time.Second), s, prov.Name(), query, nextRequest(&m.requests.spotSearch))
}

// The provider's own search says whose results it shows and for what, so it
// cannot be taken for the library search it was opened from: the same
// recording is often in both, and its rows carry no source.

// spotSearchProviderName is the searched provider's name, e.g. "Spotify".
func (m Model) spotSearchProviderName() string {
	if m.spotSearch.prov == nil {
		return ""
	}
	return m.spotSearch.prov.Name()
}

// spotSearchPromptLabel labels the query input: "Search Spotify".
func (m Model) spotSearchPromptLabel() string {
	return strings.TrimSpace("Search " + m.spotSearchProviderName())
}

// spotSearchLoadingLabel is shown while a search runs:
// "Searching Spotify for “daphnis”…".
func (m Model) spotSearchLoadingLabel() string {
	lead := strings.TrimSpace("Searching " + m.spotSearchProviderName())
	return m.spotSearchWithQuery(lead, ui.PanelWidth-lipgloss.Width("  …")) + "…"
}

// spotSearchResultsLabel heads the results, before their position:
// "Spotify results for “daphnis”".
func (m Model) spotSearchResultsLabel() string {
	lead := strings.TrimSpace(m.spotSearchProviderName() + " results")
	position := ""
	if n := len(m.spotSearch.results); n > 0 {
		position = fmt.Sprintf("  %d/%d", m.spotSearch.cursor+1, n) // as sepHeaderN adds it
	}
	return m.spotSearchWithQuery(lead, ui.PanelWidth-lipgloss.Width("── "+position+" "))
}

// spotSearchWithQuery is lead followed by the query in quotes, within
// width columns. The query is what gives way: it is cut short, and on a
// panel with no room for a few characters of it, left out, so the
// provider's name always shows.
func (m Model) spotSearchWithQuery(lead string, width int) string {
	const open, shut = " for “", "”"
	room := width - lipgloss.Width(lead+open+shut)
	if room < 4 {
		return lead
	}
	return lead + open + truncate(m.spotSearch.query, room) + shut
}

// spotSearchQueryEdited ends the search in flight when the query was edited
// (before is the query as it was): its reply is for another query and would
// be dropped, leaving the screen loading with Enter inert. The request is
// cancelled and retired, so the edited query can be searched at once.
func (m *Model) spotSearchQueryEdited(before string) {
	if m.spotSearch.query == before || !m.spotSearch.loading {
		return
	}
	m.cancelSpotRequest()
	nextRequest(&m.requests.spotSearch)
	m.spotSearch.loading = false
}

// libSearchPrompt is the search frame's input line: highlighted while it
// has focus, dim once the results do.
func (m *Model) libSearchPrompt(sl library.SearchLevel) string {
	if m.lib.searchInput {
		return m.promptHeader(librarySearchField, "Search", sl.Query())
	}
	return dimStyle.Render(truncate("  Search: "+sl.Query(), ui.PanelWidth))
}

// libSearchHint is shown under an empty query.
var libSearchHint = []string{
	"  Type to search Spotify, Local and your radio stations.",
	"  artist:  album:  title:  genre:  source:spotify|local|radio",
	"  type:artist|album|track|playlist|station   \"exact phrase\"",
}

// spotSearchPlay plays what Enter picked in a provider's own search
// ("Search Spotify for …"): it replaces the queue, as playing from the
// library does (owner, 2026-10-01). a and q still append and queue next.
func (m *Model) spotSearchPlay(tracks []playlist.Track) tea.Cmd {
	return m.libraryPlayTracks(tracks, 0, "")
}

// stopSpotSearchAlbum drops an album the search overlay is still opening,
// for Stop: an album to append would otherwise start the stopped player. The
// overlay's other requests (a text search, a playlist add) share its cancel
// slot, so it acts only while an album is loading.
func (m *Model) stopSpotSearchAlbum() {
	if m.spotSearch.albumLoading {
		m.invalidateSpotAlbumRequest()
	}
}
