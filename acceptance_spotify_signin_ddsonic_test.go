package main

// ddsonic: the acceptance case for Spotify's first sign-in, in-process.
//
// What is real: the catalog runtime, sync engine, SQLite catalog, the
// library's sources and root as main builds them (librarySources,
// library.Root, the synced menu), the sign-in wiring (librarySignIn), and the
// model, driven by keys and read from its screen. What is a fake: the
// account, and the sign-in itself, which here flips the account to signed in
// where the product opens a browser. The browser flow, the session and the
// stored credential are live acceptance (LIVE-SPOT-001).

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/catalogsync/spotifysrc"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// signInSpotify is the Spotify player of a home that may not be signed in:
// signing in makes the account readable, and is counted.
type signInSpotify struct {
	fakePlayer
	account *spotifyAccount
	signIns int
}

func (p *signInSpotify) Authenticate() error {
	p.signIns++
	p.account.edit(func() { p.account.unavailable = nil })
	return nil
}

// idleEngine is a player that plays nothing: the library is what is tested.
// The queries the model makes of an idle player answer; anything that would
// play is not called, and would fail on the nil Engine if it were.
type idleEngine struct{ player.Engine }

func (idleEngine) ClearPreload()                                       {}
func (idleEngine) Stop()                                               {}
func (idleEngine) Close()                                              {}
func (idleEngine) IsPlaying() bool                                     { return false }
func (idleEngine) IsPaused() bool                                      { return false }
func (idleEngine) Drained() bool                                       { return false }
func (idleEngine) HasPreload() bool                                    { return false }
func (idleEngine) Seekable() bool                                      { return false }
func (idleEngine) IsStreamSeek() bool                                  { return false }
func (idleEngine) IsYTDLSeek() bool                                    { return false }
func (idleEngine) GaplessAdvanced() bool                               { return false }
func (idleEngine) LastPlayedDuration() time.Duration                   { return 0 }
func (idleEngine) Position() time.Duration                             { return 0 }
func (idleEngine) Duration() time.Duration                             { return 0 }
func (idleEngine) PositionAndDuration() (time.Duration, time.Duration) { return 0, 0 }
func (idleEngine) VolumeMin() float64                                  { return -30 }
func (idleEngine) Volume() float64                                     { return 0 }
func (idleEngine) Speed() float64                                      { return 1 }
func (idleEngine) Mono() bool                                          { return false }
func (idleEngine) EQBands() [10]float64                                { return [10]float64{} }
func (idleEngine) StreamErr() error                                    { return nil }
func (idleEngine) StreamTitle() string                                 { return "" }
func (idleEngine) StreamBytes() (downloaded, total int64)              { return 0, 0 }
func (idleEngine) SamplesInto([]float64) int                           { return 0 }
func (idleEngine) WaveformSamplesInto([]float64) int                   { return 0 }
func (idleEngine) StereoSamplesInto([][2]float64) int                  { return 0 }
func (idleEngine) SampleRate() int                                     { return 44100 }

// signInHome is one start of ddsonic over a catalog file: the runtime, the
// model as main wires it, and the sync messages the runtime sends it.
type signInHome struct {
	t    *testing.T
	rt   *catalogRuntime
	m    model.Model
	msgs chan tea.Msg
}

func startSignInHome(t *testing.T, dbPath string, account *spotifyAccount, spotify *signInSpotify) *signInHome {
	t.Helper()
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	h := &signInHome{t: t, msgs: make(chan tea.Msg, 64)}
	rt := &catalogRuntime{store: store, providers: map[string]*providerSync{}, retryMin: time.Hour, retryMax: time.Hour}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	src := spotifysrc.New(account)
	rt.filler = catalogsync.NewFiller(store, src, catalogsync.DefaultPacing)
	rt.setSources(source{Source: src, refresh: 2 * time.Hour, worker: rt.filler})
	rt.send = func(msg tea.Msg) { h.msgs <- msg }
	h.rt = rt

	providers := []model.ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: spotify}}
	h.m = model.New(idleEngine{}, playlist.New(), providers, "", nil, nil, nil, config.SaveFunc{})
	h.m.SetLibrary(library.Root(librarySources(providers, "", rt)))
	rt.configure(&h.m)
	librarySignIn(&h.m, spotify)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 32})
	// What start does, without a program to send to: sync what is due.
	for provider, ps := range rt.providers {
		if ps.stale {
			rt.sync(provider)
		}
	}
	return h
}

// send delivers msg to the model, and then what its commands produce: loads
// and the sign-in. Timers and everything else are left alone.
func (h *signInHome) send(msg tea.Msg) {
	h.t.Helper()
	updated, cmd := h.m.Update(msg)
	h.m = updated.(model.Model)
	h.run(cmd)
}

func (h *signInHome) run(cmd tea.Cmd) {
	h.t.Helper()
	if cmd == nil {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(200 * time.Millisecond):
		return // a timer
	}
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		if name := fmt.Sprintf("%T", msg); name == "model.libraryLoadedMsg" || name == "model.libraryAuthDoneMsg" {
			h.send(msg)
		}
	}
}

// syncEnds delivers the runtime's messages until Spotify's sync finishes,
// and returns how it ended.
func (h *signInHome) syncEnds() error {
	h.t.Helper()
	for {
		select {
		case msg := <-h.msgs:
			h.send(msg)
			if m, ok := msg.(model.CatalogSyncMsg); ok && m.Provider == catalog.Spotify && m.Phase == model.CatalogSyncFinished {
				return m.Err
			}
		case <-time.After(10 * time.Second):
			h.t.Fatal("Spotify's sync did not finish")
			return nil
		}
	}
}

func (h *signInHome) screen() string { return ansi.Strip(h.m.View().Content) }

func (h *signInHome) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		switch k {
		case "enter":
			h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
		default:
			h.send(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
		}
	}
}

// open moves the cursor to the row that reads title and opens it.
func (h *signInHome) open(title string) {
	h.t.Helper()
	for range 12 {
		if strings.Contains(h.screen(), "> "+title) {
			h.press("enter")
			return
		}
		h.press("j")
	}
	h.t.Fatalf("no row %q to open:\n%s", title, h.screen())
}

func (h *signInHome) want(when string, want []string, not ...string) {
	h.t.Helper()
	screen := h.screen()
	for _, w := range want {
		if !strings.Contains(screen, w) {
			h.t.Fatalf("%s: the screen lacks %q:\n%s", when, w, screen)
		}
	}
	for _, n := range not {
		if strings.Contains(screen, n) {
			h.t.Fatalf("%s: the screen shows %q:\n%s", when, n, screen)
		}
	}
}

// spotifySynced is how many of Spotify's collections have synced, and how
// many hold an error.
func (h *signInHome) spotifySynced() (synced, failed int) {
	h.t.Helper()
	status, err := h.rt.store.SyncStatus(context.Background(), catalog.Spotify)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, c := range status {
		if !c.LastSuccess.IsZero() {
			synced++
		}
		if c.LastError != "" {
			failed++
		}
	}
	return synced, failed
}

// A home with Spotify set up and never signed in, and nothing cached: the
// library offers the sign-in in Spotify's own menu and in its empty lists,
// an explicit Enter signs in, Spotify syncs at once with no "r", every
// collection fills, and after a restart nothing asks for sign-in and nothing
// is read again.
func TestAcceptanceSpotifyFirstRunSignIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dbPath := filepath.Join(t.TempDir(), "library.db")
	account := &spotifyAccount{
		albums:      []catalog.AlbumRecord{savedAlbum("al-b", "Beta Album", 2), savedAlbum("al-a", "Alpha Album", 1)},
		artists:     []catalog.ArtistRecord{{Ref: spotifyRef("ar-1"), Name: "Followed Artist"}},
		liked:       []catalog.TrackRecord{likedTrack("tr-1", "Liked One")},
		unavailable: fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth), // not signed in
	}
	spotify := &signInSpotify{fakePlayer: fakePlayer{"Spotify"}, account: account}
	home := startSignInHome(t, dbPath, account, spotify)

	// Fresh, configured, not signed in, nothing cached.
	if err := home.syncEnds(); err == nil {
		t.Fatal("the first sync succeeded without a sign-in")
	}
	if synced, failed := home.spotifySynced(); synced != 0 || failed != 4 {
		t.Fatalf("before signing in: %d collections synced and %d failed, want 0 and 4", synced, failed)
	}
	if albums, err := home.rt.store.Albums(context.Background(), catalog.Spotify, catalog.ByTitle); err != nil || len(albums) != 0 {
		t.Fatalf("before signing in: the catalog holds %d Spotify albums (%v), want none", len(albums), err)
	}
	if spotify.signIns != 0 {
		t.Fatalf("signed in %d times before being asked", spotify.signIns)
	}

	// The sign-in is offered where Spotify is browsed, and in every list.
	home.open("Spotify")
	home.want("Spotify's menu", []string{"> Sign in to Spotify", "Spotify sign-in required", "Albums", "Artists", "Playlists", "Liked Songs"},
		"Syncing your library", "cached")
	for _, list := range []string{"Albums", "Artists", "Playlists", "Liked Songs"} {
		home.open(list)
		home.want(list+" before signing in", []string{"Sign-in required. Press Enter to sign in."}, "Syncing your library", "press r to retry")
		home.press("h")
	}

	// Explicit sign-in: Enter on the row. The sync that follows is asked for
	// by the sign-in, not by a key.
	readsBefore := account.callCount()
	home.open("Sign in to Spotify")
	if spotify.signIns != 1 {
		t.Fatalf("Enter on the sign-in row signed in %d times, want 1", spotify.signIns)
	}
	if err := home.syncEnds(); err != nil {
		t.Fatalf("the sync after signing in failed: %v", err)
	}
	if account.callCount() == readsBefore {
		t.Fatal("no sync read the account after the sign-in")
	}
	if synced, failed := home.spotifySynced(); synced != 4 || failed != 0 {
		t.Fatalf("after signing in: %d collections synced and %d failed, want 4 and 0", synced, failed)
	}
	home.want("Spotify's menu after the sync", []string{"> Albums", "✓ synced"}, "Sign in to Spotify", "sign-in required", "sync failed")
	home.open("Albums")
	home.want("Albums after the sync", []string{"Alpha Album", "Beta Album"}, "Sign-in required")
	home.press("h")
	home.open("Artists")
	home.want("Artists after the sync", []string{"Followed Artist"}, "Sign-in required")
	home.press("h")
	home.open("Liked Songs")
	home.want("Liked Songs after the sync", []string{"Liked One"}, "Sign-in required")
	home.press("h")
	home.open("Playlists")
	home.want("Playlists after the sync", []string{"Nothing here."}, "Sign-in required", "Syncing your library")

	// Restart: the same catalog, the stored sign-in still good.
	home.rt.close()
	readsBefore = account.callCount()
	restarted := &signInSpotify{fakePlayer: fakePlayer{"Spotify"}, account: account}
	again := startSignInHome(t, dbPath, account, restarted)
	t.Cleanup(again.rt.close)
	again.open("Spotify")
	again.want("Spotify's menu after a restart", []string{"> Albums", "✓ synced"}, "Sign in to Spotify", "sign-in required", "↻ syncing")
	again.open("Albums")
	again.want("Albums after a restart", []string{"Alpha Album", "Beta Album"}, "Sign-in required", "Syncing your library")
	if restarted.signIns != 0 || spotify.signIns != 1 {
		t.Errorf("sign-ins after the restart = %d (and %d before), want 0 and 1", restarted.signIns, spotify.signIns)
	}
	if got := account.callCount(); got != readsBefore {
		t.Errorf("the restart read the account %d times, want none: the lists come from the catalog", got-readsBefore)
	}
}

// The app's own wiring with the real Spotify provider and no stand-in for
// Spotify: the sign-in fails at once. No browser is started and no callback
// port is opened, in this package's tests as in every other's.
func TestSignInFailsClosedInTheApp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	account := &spotifyAccount{unavailable: fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)}
	stand := &signInSpotify{fakePlayer: fakePlayer{"Spotify"}, account: account}
	home := startSignInHome(t, filepath.Join(t.TempDir(), "library.db"), account, stand)
	t.Cleanup(home.rt.close)
	real := spotify.New(nil, "0123456789abcdef0123456789abcdef", 320)
	t.Cleanup(real.Close)
	librarySignIn(&home.m, real) // as main registers it
	if err := home.syncEnds(); err == nil {
		t.Fatal("the first sync succeeded without a sign-in")
	}

	home.open("Spotify")
	home.open("Sign in to Spotify")
	home.want("after the refused sign-in", []string{"Sign in to Spotify", "sign-in failed"}, "Signing in")
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:19872", 200*time.Millisecond); err == nil {
		conn.Close()
		t.Error("the sign-in callback port was opened")
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a browser was started")
	}
	if stand.signIns != 0 {
		t.Error("the stand-in was signed in to instead of the registered provider")
	}
}
