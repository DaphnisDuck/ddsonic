package main

// ddsonic: an explicit refresh is never lost behind an older sync run (R8-3).
// The engine announces a sync's end before the runtime gives the provider up,
// so a request made by whoever saw that end used to be counted and dropped.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/catalogsync/spotifysrc"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/model"
)

// holdAfterFirstFinish makes rt deliver its first Finished message and then
// hold the syncing goroutine there, before it gives the provider up, until
// release is closed. entered is closed once it is held.
func holdAfterFirstFinish(rt *catalogRuntime) (entered, release chan struct{}) {
	entered, release = make(chan struct{}), make(chan struct{})
	inner, first := rt.send, true
	rt.send = func(msg tea.Msg) {
		inner(msg)
		if m, ok := msg.(model.CatalogSyncMsg); ok && m.Phase == model.CatalogSyncFinished && first {
			first = false
			close(entered)
			<-release
		}
	}
	return entered, release
}

// A refresh asked for after a sync was seen to finish, but before that run
// has given the provider up, runs. This is the window that made
// TestAcceptanceSpotifyFailedSyncKeepsCacheAndRecovers time out now and then.
func TestRefreshAfterAnnouncedFinishIsNotDropped(t *testing.T) {
	account := &spotifyAccount{albums: []catalog.AlbumRecord{savedAlbum("al-a", "Alpha Album", 1)}}
	run := startSpotify(t, filepath.Join(t.TempDir(), "library.db"), account)
	t.Cleanup(run.rt.close)
	entered, release := holdAfterFirstFinish(run.rt)

	run.rt.refresh(catalog.Spotify)
	<-entered
	<-run.finished                  // the first sync's end has been seen
	run.rt.refresh(catalog.Spotify) // what "r" does next
	close(release)
	select {
	case <-run.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh made after the sync was seen to finish never ran")
	}
}

// The sync a successful sign-in requires runs once the older sync has given
// the provider up, with no further key and no retry timer: the older run's
// completion had been delivered, and it failed for want of the sign-in.
func TestSignInSyncIsNotLostBehindOlderSyncCompletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	account := &spotifyAccount{
		albums:      []catalog.AlbumRecord{savedAlbum("album", "Alpha Album", 1)},
		unavailable: fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth),
	}
	sp := &signInSpotify{fakePlayer: fakePlayer{"Spotify"}, account: account}
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
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
	entered, release := holdAfterFirstFinish(rt)

	providers := []model.ProviderEntry{{Key: "spotify", Name: "Spotify", Provider: sp}}
	h.m = model.New(idleEngine{}, playlist.New(), providers, "", nil, nil, nil, config.SaveFunc{})
	h.m.SetLibrary(library.Root(librarySources(providers, "", rt)))
	rt.configure(&h.m)
	librarySignIn(&h.m, sp)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 32})

	rt.sync(catalog.Spotify)
	<-entered
	if err := h.syncEnds(); err == nil {
		t.Fatal("the sync before the sign-in did not fail")
	}
	reads := account.callCount()
	h.open("Spotify")
	h.open("Sign in to Spotify")
	if sp.signIns != 1 {
		t.Fatalf("the sign-in ran %d times, want once", sp.signIns)
	}
	close(release)

	finished := false
	wait := time.NewTimer(5 * time.Second)
	defer wait.Stop()
waitLoop:
	for {
		select {
		case msg := <-h.msgs:
			if m, ok := msg.(model.CatalogSyncMsg); ok && m.Phase == model.CatalogSyncFinished {
				finished = true
				break waitLoop
			}
		case <-wait.C:
			break waitLoop
		}
	}
	got, err := store.Albums(context.Background(), catalog.Spotify, catalog.ByTitle)
	if err != nil {
		t.Fatal(err)
	}
	rt.close()
	if !finished || account.callCount() == reads || len(got) == 0 {
		t.Fatalf("the sign-in was followed by no sync: finished %t, reads before %d and after %d, albums %d",
			finished, reads, account.callCount(), len(got))
	}
}

// heldSource is a sync source whose every run waits to be let through.
type heldSource struct {
	namedSource
	entered chan int   // a run's number, once its read has begun
	proceed chan error // what to let the waiting run end with
	runs    int        // read by the test only between runs
}

func (s *heldSource) Fetch(ctx context.Context, _ string, _ catalogsync.Known) (catalog.Snapshot, error) {
	s.runs++
	s.entered <- s.runs
	select {
	case err := <-s.proceed:
		if err != nil {
			return catalog.Snapshot{}, err
		}
		return catalog.Snapshot{}, catalogsync.ErrUnchanged
	case <-ctx.Done():
		return catalog.Snapshot{}, ctx.Err()
	}
}

// owedRuntime is a runtime over one gated Spotify source; ended receives each
// run's last message.
func owedRuntime(t *testing.T) (rt *catalogRuntime, src *heldSource, ended chan model.CatalogSyncMsg) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	src = &heldSource{namedSource: namedSource{catalog.Spotify}, entered: make(chan int, 8), proceed: make(chan error)}
	rt = &catalogRuntime{store: store, providers: map[string]*providerSync{}, retryMin: time.Hour, retryMax: time.Hour}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	rt.setSources(source{Source: src})
	ended = make(chan model.CatalogSyncMsg, 8)
	rt.send = func(msg tea.Msg) {
		if m, ok := msg.(model.CatalogSyncMsg); ok && m.Phase == model.CatalogSyncFinished {
			ended <- m
		}
	}
	t.Cleanup(rt.close)
	return rt, src, ended
}

// idle waits until Spotify's sync has given the provider up, and reports how
// many runs there have been. No run is waiting at that point.
func idle(t *testing.T, rt *catalogRuntime, src *heldSource) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rt.mu.Lock()
		running := rt.providers[catalog.Spotify].running
		rt.mu.Unlock()
		if !running {
			select {
			case n := <-src.entered:
				t.Fatalf("run %d began after the provider was given up", n)
			default:
			}
			return src.runs
		}
		if time.Now().After(deadline) {
			t.Fatal("the sync never gave the provider up")
		}
		time.Sleep(time.Millisecond)
	}
}

func await[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		panic("unreachable")
	}
}

// What a request made while a sync runs is owed, by who makes it and when:
// before the run's end has been reported it is that run, except for the sync
// a sign-in requires; after, an explicit request is owed its own run; a
// background one never is. Any number of requests owe one run, and that run
// owes none itself.
func TestRequestsBehindARunningSync(t *testing.T) {
	tests := []struct {
		name      string
		kind      syncKind
		announced bool // the requests are made after the run's end was reported
		requests  int
		wantRuns  int
	}{
		{"r while the sync runs is that sync", syncExplicit, false, 3, 1},
		{"r after the sync was seen to end is owed a sync", syncExplicit, true, 1, 2},
		{"many r after the end owe one sync", syncExplicit, true, 5, 2},
		{"the sync a sign-in requires is owed while the old one runs", syncRequired, false, 1, 2},
		{"many required syncs while one runs owe one", syncRequired, false, 4, 2},
		{"the sync a sign-in requires is owed after the end too", syncRequired, true, 2, 2},
		{"a background request while the sync runs is that sync", syncBackground, false, 3, 1},
		{"a background request after the end is not owed", syncBackground, true, 3, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, src, ended := owedRuntime(t)
			var entered, release chan struct{}
			if tt.announced {
				entered, release = holdAfterFirstFinish(rt)
			}
			rt.refresh(catalog.Spotify)
			await(t, "the first run", src.entered)
			ask := func() uint64 {
				var ticket uint64
				for range tt.requests {
					ticket = rt.request(catalog.Spotify, tt.kind)
				}
				return ticket
			}
			var ticket uint64
			if tt.announced {
				src.proceed <- nil
				<-entered // the end is reported; the provider is not given up
				ticket = ask()
				close(release)
			} else {
				ticket = ask()
				src.proceed <- nil
			}
			if first := await(t, "the first run's end", ended); first.Ticket != 0 {
				t.Errorf("the run that began before any sign-in carries ticket %d", first.Ticket)
			}
			if tt.wantRuns == 2 {
				await(t, "the owed run", src.entered)
				src.proceed <- nil
				second := await(t, "the owed run's end", ended)
				if tt.kind == syncRequired && (ticket == 0 || second.Ticket != ticket) {
					t.Errorf("the required run carries ticket %d, want the last one handed out, %d", second.Ticket, ticket)
				}
			}
			if runs := idle(t, rt, src); runs != tt.wantRuns {
				t.Errorf("%d syncs ran, want %d", runs, tt.wantRuns)
			}
		})
	}
}

// A request made while the owed run itself runs owes exactly one more.
func TestRequestDuringOwedRunOwesOneMore(t *testing.T) {
	rt, src, ended := owedRuntime(t)
	rt.refresh(catalog.Spotify)
	await(t, "the first run", src.entered)
	first := rt.refreshRequired(catalog.Spotify)
	src.proceed <- nil
	await(t, "the first run's end", ended)
	await(t, "the owed run", src.entered)
	second := rt.refreshRequired(catalog.Spotify)
	src.proceed <- nil
	if m := await(t, "the owed run's end", ended); m.Ticket != first {
		t.Errorf("the owed run carries ticket %d, want %d", m.Ticket, first)
	}
	await(t, "the third run", src.entered)
	src.proceed <- nil
	if m := await(t, "the third run's end", ended); m.Ticket != second {
		t.Errorf("the third run carries ticket %d, want %d", m.Ticket, second)
	}
	if runs := idle(t, rt, src); runs != 3 {
		t.Errorf("%d syncs ran, want 3", runs)
	}
}

// The owed run follows a run that failed, at once: the failed run's retry
// delay is not waited for, and the owed run's own result settles the retry.
func TestOwedRunFollowsAFailedRun(t *testing.T) {
	rt, src, ended := owedRuntime(t)
	rt.refresh(catalog.Spotify)
	await(t, "the first run", src.entered)
	rt.refreshRequired(catalog.Spotify)
	src.proceed <- fmt.Errorf("spotify: %w", playlist.ErrNeedsAuth)
	if m := await(t, "the first run's end", ended); m.Err == nil {
		t.Fatal("the first run did not fail")
	}
	await(t, "the owed run", src.entered) // retryMin is an hour: no timer brought it
	src.proceed <- nil
	if m := await(t, "the owed run's end", ended); m.Err != nil {
		t.Fatalf("the owed run failed: %v", m.Err)
	}
	idle(t, rt, src)
	rt.mu.Lock()
	armed := rt.providers[catalog.Spotify].retry != nil
	rt.mu.Unlock()
	if armed {
		t.Error("a retry is armed after the owed run succeeded")
	}
}

// Quitting ends what was owed with everything else.
func TestNoOwedRunAfterQuit(t *testing.T) {
	rt, src, _ := owedRuntime(t)
	rt.refresh(catalog.Spotify)
	await(t, "the first run", src.entered)
	rt.refreshRequired(catalog.Spotify)
	rt.close() // cancels the run and waits for it
	if src.runs != 1 {
		t.Errorf("%d syncs ran, want the one that was cancelled", src.runs)
	}
	if ticket := rt.refreshRequired(catalog.Spotify); ticket != 0 {
		t.Errorf("a closed runtime handed out ticket %d", ticket)
	}
}
