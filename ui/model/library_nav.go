package model

// ddsonic: the library navigation stack (Music → source → concept → item).
// The library owns the main screen; the playback chrome around it is
// cliamp's, untouched. Upstream files reach this code only through small
// "// ddsonic:" hooks (handleKey, Update, activeScreen, activeOverlay, the
// layout, the key bar and the SRC row).

import (
	"cmp"
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
)

// libraryLoadTimeout bounds one level load. Spotify libraries page slowly
// until Milestone 2's catalog removes the wait.
const libraryLoadTimeout = 5 * time.Minute

type libraryState struct {
	// visible is false while Tab has handed the screen to the queue.
	visible bool
	stack   []libFrame
	gen     uint64 // request generation shared by loads, plays and sign-ins
	// fits caches the queue's and Up next's row columns (library_rows.go).
	fits *libFits
	// border draws the frame's border and rules (library_frame.go).
	border bool
	// art is the info view's artwork, nil when off (ddsonic: library_info.go).
	art *libArt

	// ctx is the library's lifetime: foreground work (loads, plays,
	// artwork) derives its timeouts from it, and quitting cancels it, so
	// nothing keeps fetching or writing once ddsonic is on its way out.
	ctx  context.Context
	stop context.CancelFunc

	playGen   uint64
	authGen   uint64
	signingIn bool
	authURL   string
	// signInProvider is the source signing in (library_signin.go).
	signInProvider string

	// Catalog sync status per provider, and the action behind "r".
	sync    map[string]*libSync
	refresh func(provider string)
	// refreshRequired is refresh for the sync a sign-in requires
	// (SetCatalogSyncRequired); nil without a catalog runtime.
	refreshRequired func(provider string) uint64
	// auth is who signs in for each synced source that can be signed in to
	// from the library (library_signin.go).
	auth map[string]playlist.Authenticator

	// Search: whether the search frame's input has focus (else its
	// results), the last query (kept when search reopens), and the
	// debounced query waiting to run.
	searchInput   bool
	lastQuery     string
	searchGen     uint64
	searchPending bool // a typed query is waiting for its debounce tick

	// barRows is how many rows the layout gave the key bar (0 before any).
	barRows int

	// favSaving holds the stream URLs of the stations whose f is still
	// being saved (library_favorite.go).
	favSaving map[string]bool
}

// libSync is one provider's catalog sync status as the library shows it.
type libSync struct {
	running     bool
	lastSuccess time.Time
	lastErr     string
	// needsAuth: the source asked for sign-in, in a sync, in playback or in
	// its own search (library_signin.go).
	needsAuth bool
	// authTicket is the ticket of the sync a sign-in required, until that
	// sync has ended: a sync that ends with an older ticket began before the
	// sign-in, and says nothing about it (librarySignedIn).
	authTicket uint64
}

// CatalogSyncPhase is the stage a CatalogSyncMsg reports.
type CatalogSyncPhase int

const (
	CatalogSyncStarted CatalogSyncPhase = iota
	CatalogSyncCollectionDone
	CatalogSyncFinished
)

// CatalogSyncMsg reports catalog sync progress to the library. main sends it
// from the sync engine's events.
type CatalogSyncMsg struct {
	Provider   string
	Phase      CatalogSyncPhase
	Collection string // CatalogSyncCollectionDone only
	Err        error
	// Ticket is the latest sync a sign-in required that this run was started
	// to serve (SetCatalogSyncRequired); zero when none.
	Ticket uint64
}

// CatalogChangedMsg reports that a sync changed provider's catalog rows, for
// a source that shows no sync status (radio, whose sync follows a station
// starred or unstarred). main sends it when such a sync finishes.
type CatalogChangedMsg struct {
	Provider string
}

// CatalogStatus is a provider's last sync outcome, read at startup.
type CatalogStatus struct {
	LastSuccess time.Time
	LastError   string
}

// SetCatalogSync gives the library each provider's stored sync status and
// the function "r" calls to sync now: with the provider of the catalog level
// being browsed, or "" for every provider. refresh must not block.
func (m *Model) SetCatalogSync(status map[string]CatalogStatus, refresh func(provider string)) {
	m.lib.sync = make(map[string]*libSync, len(status))
	for provider, st := range status {
		m.lib.sync[provider] = &libSync{lastSuccess: st.LastSuccess, lastErr: st.LastError}
	}
	m.lib.refresh = refresh
}

// SetCatalogSyncRequired gives the library the function a successful sign-in
// calls: it syncs provider even when an older sync still runs, after it, and
// returns the ticket that sync's messages will carry. It must not block.
func (m *Model) SetCatalogSyncRequired(required func(provider string) uint64) {
	m.lib.refreshRequired = required
}

// libCatalogProvider is the provider of the nearest catalog level on the
// stack, from the top down, or "" outside any (the Music root).
func (m *Model) libCatalogProvider() string {
	for i := len(m.lib.stack) - 1; i >= 0; i-- {
		if cl, ok := m.lib.stack[i].level.(library.CatalogLevel); ok {
			return cl.CatalogProvider()
		}
	}
	return ""
}

func (m *Model) libSyncState(provider string) *libSync {
	if m.lib.sync == nil {
		m.lib.sync = map[string]*libSync{}
	}
	st := m.lib.sync[provider]
	if st == nil {
		st = &libSync{}
		m.lib.sync[provider] = st
	}
	return st
}

// libFrame is one level on the navigation stack. Popping keeps the parent's
// cursor, so Back returns to the row that was opened.
type libFrame struct {
	level   library.Level
	entries []library.Entry
	cursor  int
	scroll  int
	err     error
	gen     uint64
	cancel  context.CancelFunc // non-nil while a load is in flight
	// keepID, set by a refresh, is the entry ID the cursor returns to once
	// the reloaded rows arrive.
	keepID string
	// stale marks a catalog level below the top whose provider synced since
	// it loaded; it reloads when it becomes the top again.
	stale bool
	// source is the catalog provider of the level's content, from the row
	// that opened it (or its frame); "" where sources mix.
	source string
	// needs is each row's width, and column the widest (setEntries,
	// library_rows.go).
	needs  []int
	column int
	// rows are the entries as rendered lines (with section headings),
	// numbers each track's number, and rowOf each entry's line: set with
	// the entries, so a render costs the lines in view, not the list.
	rows    []libRow
	numbers []int
	rowOf   []int
	cols    libCols // the fields of its browsable rows
	// trackCols are the fields of its track rows.
	trackCols libCols
}

func (f *libFrame) loading() bool { return f.cancel != nil }

type libraryLoadedMsg struct {
	gen     uint64
	entries []library.Entry
	err     error
	// unchanged: a catalog reread found the rows came from the provider,
	// so they stay (library.CachedLevel).
	unchanged bool
}

type libraryPlayMsg struct {
	gen    uint64
	title  string
	source string
	tracks []playlist.Track
	index  int // the track to start at
	err    error
}

type libraryAuthDoneMsg struct {
	gen uint64
	err error
	// provider is the source that signed in (library_signin.go).
	provider string
}

// SetLibrary makes the library the main screen, starting at root. root must
// load without I/O (a static menu), because Init cannot issue its load.
func (m *Model) SetLibrary(root library.Level) {
	entries, err := root.Load(context.Background())
	m.lib = libraryState{visible: true, stack: []libFrame{{level: root, err: err}}, fits: &libFits{}}
	m.lib.ctx, m.lib.stop = context.WithCancel(context.Background())
	m.lib.stack[0].setEntries(entries)
	m.focus = focusPlaylist
}

func (m Model) libraryEnabled() bool { return len(m.lib.stack) > 0 }

// libContext is the parent of foreground library work: the library's
// lifetime, or Background for a model without a library (tests).
func (m Model) libContext() context.Context {
	if m.lib.ctx != nil {
		return m.lib.ctx
	}
	return context.Background()
}

// libStop cancels foreground library work as ddsonic quits.
func (m *Model) libStop() {
	if m.lib.stop != nil {
		m.lib.stop()
	}
}

// StopLibrary cancels foreground library work once the program has ended.
// main calls it on every way out: a signal ends the program without the
// model's own quit.
func (m Model) StopLibrary() { m.libStop() }

func (m Model) libraryVisible() bool { return m.libraryEnabled() && m.lib.visible }

func (m *Model) libTop() *libFrame { return &m.lib.stack[len(m.lib.stack)-1] }

// libFrameByGen is the frame whose load has generation gen, or nil once
// that frame was popped or reloaded since.
func (m *Model) libFrameByGen(gen uint64) *libFrame {
	for i := range m.lib.stack {
		if m.lib.stack[i].gen == gen {
			return &m.lib.stack[i]
		}
	}
	return nil
}

func (m *Model) libraryPush(level library.Level, source string) tea.Cmd {
	m.lib.stack = append(m.lib.stack, libFrame{level: level, source: source})
	return m.libraryLoad()
}

// libraryLoad (re)loads the top level, superseding any load in flight.
func (m *Model) libraryLoad() tea.Cmd {
	f := m.libTop()
	if f.cancel != nil {
		f.cancel()
	}
	ctx, cancel := context.WithTimeout(m.libContext(), libraryLoadTimeout)
	f.gen = nextRequest(&m.lib.gen)
	f.cancel = cancel
	f.err = nil
	level, gen := f.level, f.gen
	return func() tea.Msg {
		defer cancel()
		entries, err := level.Load(ctx)
		return libraryLoadedMsg{gen: gen, entries: entries, err: err}
	}
}

func (m *Model) libraryPop() tea.Cmd {
	if len(m.lib.stack) <= 1 {
		return nil
	}
	if f := m.libTop(); f.cancel != nil {
		f.cancel()
	}
	m.lib.stack = m.lib.stack[:len(m.lib.stack)-1]
	if f := m.libTop(); f.stale {
		return m.libraryRefreshTop()
	}
	return nil
}

// libraryRefreshCached rereads the top level from the catalog alone
// (library.CachedLevel), keeping the cursor on its row, so a sync never sets
// off the live calls its Load may make.
func (m *Model) libraryRefreshCached(cl library.CachedLevel) tea.Cmd {
	f := m.libTop()
	keep := ""
	if f.cursor >= 0 && f.cursor < len(f.entries) {
		keep = f.entries[f.cursor].ID
	}
	if f.cancel != nil {
		f.cancel()
	}
	ctx, cancel := context.WithTimeout(m.libContext(), libraryLoadTimeout)
	f.gen = nextRequest(&m.lib.gen)
	f.cancel = cancel
	f.keepID, f.stale = keep, false
	gen := f.gen
	return func() tea.Msg {
		defer cancel()
		entries, ok, err := cl.LoadCached(ctx)
		return libraryLoadedMsg{gen: gen, entries: entries, err: err, unchanged: !ok}
	}
}

// libraryRereadIfStale rereads frame f, the top, when a sync changed its
// rows while its load ran.
func (m *Model) libraryRereadIfStale(f *libFrame) tea.Cmd {
	if _, ok := f.level.(library.CachedLevel); ok && f.stale && f == m.libTop() {
		return m.libraryRefreshTop()
	}
	return nil
}

// libraryRefreshTop rereads the top level after a sync changed its rows:
// a catalog level loads again; a cached level rereads the catalog, or, while
// its first load (perhaps live) is still running, once that load lands.
func (m *Model) libraryRefreshTop() tea.Cmd {
	f := m.libTop()
	if cl, ok := f.level.(library.CachedLevel); ok {
		if f.loading() {
			f.stale = true
			return nil
		}
		return m.libraryRefreshCached(cl)
	}
	return m.libraryRefresh()
}

// libraryRefresh reloads the top level in place: the rows stay on screen
// until the new ones arrive, and the cursor returns to the same item.
func (m *Model) libraryRefresh() tea.Cmd {
	f := m.libTop()
	keep := ""
	if f.cursor >= 0 && f.cursor < len(f.entries) {
		keep = f.entries[f.cursor].ID
	}
	cmd := m.libraryLoad()
	f.keepID, f.stale = keep, false
	return cmd
}

// libraryCatalogChanged refreshes the visible level if a sync of provider
// changed it, and marks the catalog levels beneath it stale. The starred
// stations change with radio's (library_favorite.go).
func (m *Model) libraryCatalogChanged(provider string) tea.Cmd {
	if !m.libraryEnabled() {
		return nil
	}
	// A level of every source (CatalogProvider "") changes with each.
	isCatalog := func(f *libFrame) bool {
		if cl, ok := f.level.(library.CachedLevel); ok {
			return cl.CachedProvider() == provider
		}
		if _, ok := f.level.(library.StarredStationsLevel); ok {
			return provider == catalog.Radio
		}
		cl, ok := f.level.(library.CatalogLevel)
		return ok && (cl.CatalogProvider() == provider || cl.CatalogProvider() == "")
	}
	for i := range m.lib.stack[:len(m.lib.stack)-1] {
		if isCatalog(&m.lib.stack[i]) {
			m.lib.stack[i].stale = true
		}
	}
	// A load in flight may have read the rows before this sync wrote them;
	// refreshing supersedes it.
	if top := m.libTop(); isCatalog(top) {
		return m.libraryRefreshTop()
	}
	return nil
}

// handleLibraryMsg handles the library's own messages. ok reports whether msg
// was consumed; the auth URL is also left for cliamp's handler.
func (m *Model) handleLibraryMsg(msg tea.Msg) (tea.Cmd, bool) {
	if m.handleArtworkMsg(msg) { // the info view's artwork (library_info.go)
		return nil, true
	}
	switch msg := msg.(type) {
	case libraryLoadedMsg:
		// The load goes to the frame that asked for it, which may no longer
		// be the top: a refresh can land after its level opened a child.
		f := m.libFrameByGen(msg.gen)
		if f == nil {
			return nil, true
		}
		f.cancel = nil
		if msg.unchanged {
			f.keepID = ""
			return m.libraryRereadIfStale(f), true
		}
		f.err = msg.err
		if msg.err == nil {
			f.setEntries(msg.entries)
			f.cursor = min(f.cursor, max(len(f.entries)-1, 0))
			if f.keepID != "" {
				for i, e := range f.entries {
					if e.ID == f.keepID {
						f.cursor = i
						break
					}
				}
			}
			m.libApplySignInRow(f)
			if f == m.libTop() {
				m.libAdjustScroll()
			}
		}
		f.keepID = ""
		return m.libraryRereadIfStale(f), true

	case CatalogSyncMsg:
		st := m.libSyncState(msg.Provider)
		switch msg.Phase {
		case CatalogSyncStarted:
			st.running = true
		case CatalogSyncCollectionDone:
			if msg.Err == nil {
				return m.libraryCatalogChanged(msg.Provider), true
			}
		case CatalogSyncFinished:
			if st.authTicket != 0 && msg.Ticket < st.authTicket {
				// A sync that began before the sign-in: the one the sign-in
				// required follows it. Its rows are reread; its verdict on
				// the sign-in, and its error, are about the old sign-in.
				return m.libraryCatalogChanged(msg.Provider), true
			}
			st.authTicket = 0
			st.running = false
			switch {
			case msg.Err == nil:
				st.lastSuccess, st.lastErr = time.Now(), ""
			case !errors.Is(msg.Err, context.Canceled):
				st.lastErr = msg.Err.Error()
			}
			m.libSyncEndedAuth(msg.Provider, msg.Err)
			// The sync's final sweep may have deleted rows the collections'
			// rereads still found; reread after it (failed collections
			// included: the sweep runs after those too).
			return m.libraryCatalogChanged(msg.Provider), true
		}
		return nil, true

	case CatalogChangedMsg:
		return m.libraryCatalogChanged(msg.Provider), true

	case libFavoriteSavedMsg:
		return m.libFavoriteSaved(msg), true

	case libraryPlayMsg:
		if msg.gen != m.lib.playGen {
			return nil, true
		}
		if msg.err != nil {
			m.status.Errorf(statusTTLDefault, "%s: %s", msg.title, msg.err)
			return nil, true
		}
		if len(msg.tracks) == 0 {
			m.status.Warningf(statusTTLDefault, "%s: nothing to play", msg.title)
			return nil, true
		}
		return m.libraryPlayTracks(msg.tracks, min(max(msg.index, 0), len(msg.tracks)-1), msg.source), true

	case librarySearchTickMsg:
		return m.handleLibrarySearchTick(msg), true

	case libraryAuthDoneMsg:
		if msg.gen != m.lib.authGen || !m.libraryEnabled() {
			return nil, true
		}
		return m.librarySignedIn(msg), true

	case ProvAuthURLMsg:
		if m.lib.signingIn {
			m.lib.authURL = msg.URL
		}
	}
	return nil, false
}

// librarySkip plays the next track, or the previous one, as cliamp's < and >
// do.
func (m *Model) librarySkip(next bool) tea.Cmd {
	refresh := m.scrobbleCurrent()
	cmd := m.skipByUser(next)
	m.notifyPlayback()
	return tea.Batch(refresh, cmd)
}

// handleLibraryKey owns the main screen's keys when the library is enabled.
// handled=false passes the key on to cliamp's handlers.
func (m *Model) handleLibraryKey(msg tea.KeyPressMsg) (cmd tea.Cmd, handled bool) {
	if !m.libraryEnabled() {
		return nil, false
	}
	key := msg.String()

	if !m.lib.visible {
		switch key {
		case "tab", "esc", "b":
			m.lib.visible = true
			return nil, true
		case "p", "n":
			return m.librarySkip(key == "n"), true
		case "f":
			return m.libToggleFavorite(), true
		case "X":
			return m.libClearQueue(), true
		}
		return nil, !queuePassthroughKeys[key] && !libraryPassthroughKeys[key]
	}

	if sl, ok := m.libSearchLevel(); ok {
		if m.lib.searchInput {
			return m.handleLibrarySearchInput(msg, sl), true
		}
		switch key {
		case "/", "esc", "h", "left", "backspace":
			m.lib.searchInput = true // back to the query
			return nil, true
		case "r":
			return nil, true // sync belongs to the browse views, not results
		}
	}

	f := m.libTop()
	n := len(f.entries)
	page := max(m.libListBudget()-1, 1)
	switch key {
	case "up", "k":
		if n > 0 {
			f.cursor = (f.cursor - 1 + n) % n
		}
	case "down", "j":
		if n > 0 {
			f.cursor = (f.cursor + 1) % n
		}
	case "g", "home":
		f.cursor = 0
	case "G", "end":
		f.cursor = max(n-1, 0)
	case "pgup", "ctrl+u":
		f.cursor = max(f.cursor-page, 0)
	case "pgdown", "ctrl+d":
		f.cursor = max(min(f.cursor+page, n-1), 0)
	case "enter", "l", "right":
		return m.libraryActivate(), true
	case "p", "n":
		return m.librarySkip(key == "n"), true
	case "esc", "h", "left", "backspace":
		cmd := m.libraryPop()
		m.libAdjustScroll()
		return cmd, true
	case "r":
		if m.lib.refresh != nil {
			m.status.Showf(statusTTLDefault, "Syncing library…")
			m.lib.refresh(m.libCatalogProvider()) // starts the sync in the background
			return nil, true
		}
	case "o":
		// Reorder a list that has more than one order (Albums), keeping
		// the cursor on the same row.
		if ol, ok := f.level.(library.OrderedLevel); ok {
			m.status.Showf(statusTTLDefault, "%s %s", f.level.Title(), ol.NextOrder())
			return m.libraryRefresh(), true
		}
		return nil, true
	case "tab":
		m.lib.visible = false
		m.focus = focusPlaylist
		return nil, true
	case "/":
		return m.librarySearch(), true
	case "space":
		return m.togglePlayPause(), true
	case "f":
		return m.libToggleFavorite(), true
	default:
		return nil, !libraryPassthroughKeys[key]
	}
	m.libAdjustScroll()
	return nil, true
}

// libraryActivate acts on the highlighted row: open, play, or run its intent.
// On an error screen it signs in or retries instead.
func (m *Model) libraryActivate() tea.Cmd {
	f := m.libTop()
	if f.loading() || m.libSigningInHere(f) {
		return nil
	}
	if provider, ok := m.libPending(f); ok {
		if m.libSyncState(provider).needsAuth {
			return m.librarySignInProvider(provider)
		}
		return nil
	}
	if f.err != nil {
		// Only a source that signs in from the library does: any other
		// provider's "sign-in required" is retried, never signed in to.
		if provider, ok := m.libFrameSignIn(f); ok && errors.Is(f.err, playlist.ErrNeedsAuth) {
			return m.librarySignInProvider(provider)
		}
		return m.libraryLoad()
	}
	if f.cursor < 0 || f.cursor >= len(f.entries) {
		return nil
	}
	e := f.entries[f.cursor]
	source := cmp.Or(e.Source, f.source)
	m.libSleepCommand(e) // a row that plays is a transport command (sleep_ddsonic.go)
	switch {
	case e.Open != nil:
		if sl, ok := e.Open.(library.SearchLevel); ok {
			return m.libraryOpenSearch(sl)
		}
		return m.libraryPush(e.Open, source)
	case e.Track != nil && e.PlayFrom != nil:
		return m.libraryPlayFrom(e, source)
	case e.Track != nil:
		tracks, at := library.Tracks(f.entries, f.cursor)
		return m.libraryPlayTracks(tracks, at, source)
	case e.Play != nil:
		gen := nextRequest(&m.lib.gen)
		m.lib.playGen = gen
		m.status.Showf(statusTTLLong, "Loading %s…", e.Title)
		play, title, parent := e.Play, e.Title, m.libContext()
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(parent, libraryLoadTimeout)
			defer cancel()
			tracks, err := play(ctx)
			return libraryPlayMsg{gen: gen, title: title, source: source, tracks: tracks, err: err}
		}
	case e.Intent == library.IntentSignIn:
		return m.librarySignInProvider(e.Source)
	case e.Intent == library.IntentFolders:
		m.openFileBrowser()
	case e.Intent == library.IntentSearch:
		if e.Query != "" {
			return m.openProviderSearchQuery(e.Provider, e.Query)
		}
		m.openProviderSearchWith(e.Provider)
	}
	return nil
}

// retireLibraryPlay drops a library play still resolving (an album or a
// searched track's album loading): a newer intent replaced the queue,
// stopped playback, or picked a track in the queue, and the old result must
// not land on top of it. Request generations start at 1, so 0 matches none.
func (m *Model) retireLibraryPlay() { m.lib.playGen = 0 }

// skipByUser is an explicit next or previous (a key, IPC, MPRIS, a
// plugin). Unlike the automatic advance at a track's end, it is a newer
// intent, so it retires a pending library play.
func (m *Model) skipByUser(next bool) tea.Cmd {
	m.sleepCommand() // a transport command (sleep_ddsonic.go)
	m.retireLibraryPlay()
	if next {
		return m.nextTrack()
	}
	return m.prevTrack()
}

// libEndSignIn clears the sign-in screen.
func (m *Model) libEndSignIn() {
	m.lib.signingIn = false
	m.lib.authURL = ""
	m.lib.signInProvider = ""
}

// libraryPlayTracks replaces the queue with tracks and plays tracks[index],
// the way an album or playlist plays in a library player. The library stays
// on screen; Tab shows the queue. Each track records source for SRC.
func (m *Model) libraryPlayTracks(tracks []playlist.Track, index int, source string) tea.Cmd {
	if index < 0 || index >= len(tracks) {
		return nil
	}
	tracks = withLibrarySource(tracks, source)
	m.player.Stop()
	m.player.ClearPreload()
	m.preloading = false
	m.resetYTDLBatch()
	m.retireTracksPaging()
	m.replacePlaylist(tracks)
	m.loadedPlaylist = ""
	m.activeProviderPlaylistID = ""
	m.setHeaderStateFromTracks(tracks)
	m.playlist.SetIndex(index)
	m.plCursor = index
	m.plScroll = 0
	m.adjustScroll()
	if len(tracks) > 1 {
		m.status.Showf(statusTTLMedium, "Playing: %s (%d tracks)", tracks[index].DisplayName(), len(tracks))
	} else {
		m.status.Showf(statusTTLMedium, "Playing: %s", tracks[index].DisplayName())
	}
	cmd := m.playCurrentTrack()
	m.notifyPlayback()
	return cmd
}
