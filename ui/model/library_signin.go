package model

// ddsonic: signing in to a synced source (Spotify) from the library.
//
// The source's lists are read from the catalog, so nothing in them fails with
// "sign-in required": the sync does, in the background. The library keeps
// what the sync, playback and the source's own search report (libSync
// needsAuth) and offers the sign-in where the source is browsed: a row in its
// menu, and its empty lists, whatever the catalog holds. Signing in starts
// only from Enter there; when it succeeds the source syncs at once, and the
// lists reload when that sync ends, as after any sync.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/provider"
	"github.com/bjarneo/cliamp/ui"
)

const (
	libSignInPrompt = "Sign-in required. Press Enter to sign in."
	libSigningIn    = "Signing in — finish in your browser…"
	libSyncingText  = "Syncing your library…"
)

// SetLibrarySignIn says that provider's synced source signs in through auth
// when it needs to: its menu then offers the sign-in, and its empty lists
// say why they are empty.
func (m *Model) SetLibrarySignIn(provider string, auth playlist.Authenticator) {
	if auth == nil {
		return
	}
	if m.lib.auth == nil {
		m.lib.auth = map[string]playlist.Authenticator{}
	}
	m.lib.auth[provider] = auth
}

// libAuthProviderOf is the synced source that signs in through auth.
func (m *Model) libAuthProviderOf(auth any) (string, bool) {
	for provider, a := range m.lib.auth {
		if sameValue(a, auth) {
			return provider, true
		}
	}
	return "", false
}

// sameValue is a == b for values that may not be comparable.
func sameValue(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// libFrameProvider is the synced source f belongs to, or "".
func libFrameProvider(f *libFrame) string {
	switch l := f.level.(type) {
	case library.SourceMenu:
		return l.SourceProvider()
	case library.CachedLevel:
		return l.CachedProvider()
	case library.CatalogLevel:
		return l.CatalogProvider()
	}
	return ""
}

// libSigningInHere reports whether f shows the sign-in in flight: the levels
// of the source that is signing in.
func (m *Model) libSigningInHere(f *libFrame) bool {
	if !m.lib.signingIn {
		return false
	}
	if provider, ok := m.libPending(f); ok {
		return provider == m.lib.signInProvider
	}
	// A level of the source that asked for the sign-in itself.
	if provider, ok := m.libFrameSignIn(f); ok {
		return provider == m.lib.signInProvider
	}
	return libFrameProvider(f) == m.lib.signInProvider
}

// libFrameSignIn is the source that signs in for f's level: one whose
// provider is registered to sign in from the library (SetLibrarySignIn). A
// level of any other provider has none, whatever that provider can do by
// itself: YouTube Music's own sign-in is never started from here.
func (m *Model) libFrameSignIn(f *libFrame) (provider string, ok bool) {
	al, isAuth := f.level.(library.AuthLevel)
	if !isAuth || al.Authenticator() == nil {
		return "", false
	}
	return m.libAuthProviderOf(al.Authenticator())
}

// libCanSignIn reports whether Enter on f signs in.
func (m *Model) libCanSignIn(f *libFrame) bool {
	_, ok := m.libFrameSignIn(f)
	return ok
}

// libSigningInLines is the sign-in in flight as the screen shows it, in at
// most budget rows, with the address to open by hand once the browser flow
// has one. It is the same wherever the sign-in was started: the library, the
// provider's own search, or its playlist picker.
//
// The address is the part that cannot be done without, so it is offered in a
// form that needs no repair. Its rows are a terminal hyperlink (OSC 8) whose
// target is the whole address: opening or copying the link gives the address
// intact, however many rows it takes. The rows hold nothing but the address,
// with no indentation, and come first in the claim on space: the two lines
// above them give way before any row of the address does.
func (m Model) libSigningInLines(budget int) []string {
	head := []string{loadingLine(libSigningIn)}
	if m.lib.authURL == "" {
		return head
	}
	head = append(head, dimStyle.Render("  If no browser opened, open this address (the rows below are one link):"))
	var address []string
	width := max(ui.PanelWidth, 20)
	for rest := []rune(m.lib.authURL); len(rest) > 0; {
		n := min(width, len(rest))
		address = append(address, ansi.SetHyperlink(m.lib.authURL)+dimStyle.Render(string(rest[:n]))+ansi.ResetHyperlink())
		rest = rest[n:]
	}
	for len(head) > 0 && len(head)+len(address) > budget {
		head = head[1:]
	}
	return append(head, address...)
}

// libPending reports whether f is a list with nothing cached of a source
// that signs in from the library, and names the source. Such a list says
// why it is empty (libPendingText) in place of its placeholder row.
func (m *Model) libPending(f *libFrame) (provider string, ok bool) {
	if len(f.entries) != 1 || f.entries[0].Intent != library.IntentPending {
		return "", false
	}
	provider = f.entries[0].Source
	return provider, m.lib.auth[provider] != nil
}

// libPendingText is what an empty list of provider says: it needs sign-in,
// its sync failed, or its first sync is on its way.
func (m *Model) libPendingText(provider string) string {
	st := m.libSyncState(provider)
	switch {
	case st.needsAuth:
		return libSignInPrompt
	case st.running || st.lastErr == "":
		return libSyncingText
	}
	return "Couldn't sync " + library.SourceLabel(provider) + ". Press r to retry."
}

// libCachedSuffix is " · cached" when provider's library has synced before,
// so there is something to browse while it cannot sync. A source that does
// not sign in from the library always says it, as it always has.
func (m *Model) libCachedSuffix(provider string) string {
	if m.lib.auth[provider] != nil && m.libSyncState(provider).lastSuccess.IsZero() {
		return ""
	}
	return " · cached"
}

// libSetNeedsAuth records whether provider needs sign-in and shows or hides
// its menu's sign-in row. A source that does not sign in from the library
// is left alone.
func (m *Model) libSetNeedsAuth(provider string, needs bool) {
	if m.lib.auth[provider] == nil {
		return
	}
	m.libSyncState(provider).needsAuth = needs
	for i := range m.lib.stack {
		m.libApplySignInRow(&m.lib.stack[i])
	}
	if m.libraryEnabled() {
		m.libAdjustScroll()
	}
}

// libSyncEndedAuth takes a finished sync's verdict on the sign-in: asked for,
// or working. Any other failure says nothing about it.
func (m *Model) libSyncEndedAuth(provider string, err error) {
	switch {
	case err == nil:
		m.libSetNeedsAuth(provider, false)
	case errors.Is(err, playlist.ErrNeedsAuth):
		m.libSetNeedsAuth(provider, true)
	}
}

// libApplySignInRow gives a source's menu its sign-in row while the source
// needs sign-in, first, and takes it away otherwise. The cursor stays on
// the row it was on, except that a menu just opened starts on the sign-in.
func (m *Model) libApplySignInRow(f *libFrame) {
	sm, ok := f.level.(library.SourceMenu)
	if !ok {
		return
	}
	provider := sm.SourceProvider()
	want := m.lib.auth[provider] != nil && m.libSyncState(provider).needsAuth
	has := len(f.entries) > 0 && f.entries[0].Intent == library.IntentSignIn
	switch {
	case want && !has:
		row := library.Entry{Title: "Sign in to " + library.SourceLabel(provider), Intent: library.IntentSignIn, Source: provider}
		f.setEntries(append([]library.Entry{row}, f.entries...))
		if f.cursor > 0 {
			f.cursor++
		}
	case !want && has:
		f.setEntries(f.entries[1:])
		f.cursor = max(f.cursor-1, 0)
	}
}

// librarySignInProvider starts provider's sign-in, unless one is running.
func (m *Model) librarySignInProvider(provider string) tea.Cmd {
	auth := m.lib.auth[provider]
	if auth == nil || m.lib.signingIn {
		return nil
	}
	gen := nextRequest(&m.lib.gen)
	m.lib.authGen = gen
	m.lib.signingIn = true
	m.lib.signInProvider = provider
	m.lib.authURL = ""
	return func() tea.Msg {
		return libraryAuthDoneMsg{gen: gen, provider: provider, err: auth.Authenticate()}
	}
}

// librarySignedIn ends provider's sign-in. A failure keeps the prompt and
// says why. A success syncs the source at once; its lists reload when the
// sync ends. Nothing the sign-in interrupted is done again, except a search.
func (m *Model) librarySignedIn(msg libraryAuthDoneMsg) tea.Cmd {
	provider := msg.provider
	m.libEndSignIn()
	if m.lib.ctx != nil && m.lib.ctx.Err() != nil {
		return nil // quitting
	}
	if msg.err != nil {
		m.status.Errorf(statusTTLLong, "%s sign-in failed: %s", library.SourceLabel(provider), msg.err)
		m.spotSearchSignInEnded(provider, false)
		return nil
	}
	m.libSyncState(provider).lastErr = ""
	m.libSetNeedsAuth(provider, false)
	// The sync is required, not merely asked for: it runs after any sync
	// still running, whose end would otherwise be taken for this one's.
	switch {
	case m.lib.refreshRequired != nil:
		m.libSyncState(provider).authTicket = m.lib.refreshRequired(provider)
	case m.lib.refresh != nil:
		m.lib.refresh(provider)
	}
	var cmds []tea.Cmd
	// A level that asked for the sign-in itself (an album read live) loads now.
	if f := m.libTop(); errors.Is(f.err, playlist.ErrNeedsAuth) {
		cmds = append(cmds, m.libraryLoad())
	}
	cmds = append(cmds, m.spotSearchSignInEnded(provider, true))
	return tea.Batch(cmds...)
}

// libPlaybackNeedsAuth marks the source that plays track as needing sign-in,
// after its stream was refused for the sign-in, and says where to sign in.
func (m *Model) libPlaybackNeedsAuth(track playlist.Track) {
	for provider, auth := range m.lib.auth {
		streamer, ok := auth.(interface{ URISchemes() []string })
		if !ok {
			continue
		}
		for _, scheme := range streamer.URISchemes() {
			if strings.HasPrefix(track.Path, scheme) {
				m.libSetNeedsAuth(provider, true)
				label := library.SourceLabel(provider)
				m.status.Warningf(statusTTLLong, "%s sign-in required. Open %s in the library to sign in.", label, label)
				return
			}
		}
	}
}

// spotSearchFailed shows err in the provider's own search, as the sign-in
// prompt where that is what it asks for.
func (m *Model) spotSearchFailed(err error) {
	if !m.spotSearchNeedsSignIn(err) {
		m.setSpotSearchError(err.Error())
	}
}

// spotSearchNeedsSignIn reports whether err asks for the sign-in of a source
// that signs in from the library. It then shows the prompt in the provider's
// search in place of the error, and Enter signs in (spotSearchSignInKey).
func (m *Model) spotSearchNeedsSignIn(err error) bool {
	provider, ok := m.libAuthProviderOf(m.spotSearch.prov)
	if !ok || !errors.Is(err, playlist.ErrNeedsAuth) {
		return false
	}
	m.libSetNeedsAuth(provider, true)
	m.setSpotSearchError(libSignInPrompt)
	return true
}

// spotAddOutcome says what is known of an add that returned an error: that
// the track was not added, when the request was never sent or was refused;
// otherwise that it may have been, since a request that timed out or lost its
// connection may have been carried out all the same.
func spotAddOutcome(err error) string {
	if provider.NotApplied(err) {
		return "The track was not added"
	}
	return "The track may or may not have been added (check the playlist)"
}

// spotSearchCreateFailed reports a create-and-add that did not complete.
// When the playlist was created, that is said first and stays on screen: it
// exists, whatever became of the track, and a sign-in that is needed is
// offered beside it. Nothing is undone, and nothing is tried again.
func (m *Model) spotSearchCreateFailed(msg spotCreatedMsg) {
	if !msg.created {
		if !m.spotSearchNeedsSignIn(msg.err) {
			m.setSpotSearchError("Create failed: " + msg.err.Error())
		}
		return
	}
	created := fmt.Sprintf("Created %q. ", msg.name)
	if m.spotSearchNeedsSignIn(msg.err) {
		m.spotSearch.notice = created + spotAddOutcome(msg.err) + "; add it once you have signed in."
		return
	}
	m.setSpotSearchError(created + spotAddOutcome(msg.err) + ": " + msg.err.Error())
}

// spotSearchAsksSignIn reports whether the provider's search shows the
// sign-in prompt, or the sign-in running.
func (m Model) spotSearchAsksSignIn() bool {
	return m.spotSearch.err == libSignInPrompt || m.spotSearch.err == libSigningIn
}

// spotSearchSignInKey handles Enter while the provider's search asks for
// sign-in or is signing in. ok reports whether the key was consumed.
func (m *Model) spotSearchSignInKey(msg tea.KeyPressMsg) (cmd tea.Cmd, ok bool) {
	if msg.Code != tea.KeyEnter {
		return nil, false
	}
	switch m.spotSearch.err {
	case libSigningIn:
		return nil, true
	case libSignInPrompt:
		provider, found := m.libAuthProviderOf(m.spotSearch.prov)
		if !found {
			return nil, false
		}
		cmd = m.librarySignInProvider(provider)
		if cmd != nil || m.lib.signInProvider == provider {
			m.spotSearch.err = libSigningIn
		}
		return cmd, true
	}
	return nil, false
}

// spotSearchSignInEnded returns the provider's search to use after a sign-in
// started from it. A search that was refused runs again: it only reads. An
// album or playlist action is not repeated; the user asks again.
func (m *Model) spotSearchSignInEnded(provider string, signedIn bool) tea.Cmd {
	if !m.spotSearch.visible || m.spotSearch.err != libSigningIn {
		return nil
	}
	if p, _ := m.libAuthProviderOf(m.spotSearch.prov); p != provider {
		return nil
	}
	if !signedIn {
		m.spotSearch.err = libSignInPrompt
		return nil
	}
	// What was said beside the prompt is what remains to be said.
	m.spotSearch.err, m.spotSearch.notice = m.spotSearch.notice, ""
	s, ok := m.spotSearch.prov.(searcher)
	if !ok || m.spotSearch.screen != spotSearchInput || m.spotSearch.query == "" || m.spotSearch.loading {
		return nil
	}
	m.spotSearch.loading = true
	return fetchSpotSearchCmd(m.newSpotRequestContext(30*time.Second), s, m.spotSearch.prov.Name(), m.spotSearch.query, nextRequest(&m.requests.spotSearch))
}

// searcher is provider.Searcher, named apart from the provider parameter above.
type searcher = provider.Searcher
