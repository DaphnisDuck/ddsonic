package main

// ddsonic: the catalog runtime. It opens the SQLite catalog, runs the
// background Spotify and YouTube Music syncs (with Spotify's album-track fill
// and YouTube's enrichment), the local folder index and the radio stations'
// sync, and reports them to the library UI. Kept out of main.go so upstream merges
// there stay conflict-free.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalog/sqlite"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/catalogsync/localsrc"
	"github.com/bjarneo/cliamp/catalogsync/radiosrc"
	"github.com/bjarneo/cliamp/catalogsync/spotifysrc"
	"github.com/bjarneo/cliamp/catalogsync/youtubesrc"
	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/external/local"
	"github.com/bjarneo/cliamp/external/radio"
	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/ui/model"
)

const (
	// catalogOpenTimeout bounds opening and migrating the catalog at startup.
	catalogOpenTimeout = 10 * time.Second
	// catalogStopTimeout bounds how long quitting waits for a sync to stop.
	catalogStopTimeout = 5 * time.Second
	// A failed sync retries after syncRetryMin, doubling up to syncRetryMax,
	// so an outage clears without a restart or r.
	syncRetryMin = time.Minute
	syncRetryMax = 30 * time.Minute
)

// catalogRuntime owns the catalog for one run: the Spotify sync and album
// fill, and the local folder index. Without a store, browsing stays live, as
// in v0.1.
type catalogRuntime struct {
	store  *sqlite.Store
	engine *catalogsync.Engine
	filler *catalogsync.Filler // nil without Spotify
	ctx    context.Context
	cancel context.CancelFunc

	// providers holds each synced provider's state; its keys are fixed by
	// openCatalog, and retry fields are guarded by mu.
	providers map[string]*providerSync

	mu       sync.Mutex
	wg       sync.WaitGroup
	closed   bool
	send     func(tea.Msg) // set by start, before any sync runs
	retryMin time.Duration
	retryMax time.Duration
}

// source is a sync source and how the runtime schedules it.
type source struct {
	catalogsync.Source
	// refresh is how old the last successful sync may get before startup
	// syncs again; 0 syncs at every startup.
	refresh time.Duration
	// worker runs in the background after each sync and is paused during
	// one: the Spotify album filler, the YouTube enricher.
	worker worker
	// lists are menu lists the source offers beyond its sync's collections,
	// such as albums and artists its worker derives.
	lists []string
	// quiet sources (radio: local files, instant) report no sync status
	// to the UI.
	quiet bool
	// held: the provider's quota was found exhausted (ddsonic), so nothing
	// syncs or works in the background until a sync the user starts
	// succeeds.
	held bool
}

// worker is a source's background job.
type worker interface {
	Hold() (release func())
	Run(ctx context.Context) error
}

// providerSync is one provider's sync state.
type providerSync struct {
	worker      worker              // from source.worker
	lists       []string            // from source.lists
	quiet       bool                // from source.quiet
	collections []string            // what a complete sync covers
	startup     model.CatalogStatus // stored status, read once at startup
	stale       bool                // startup should sync
	retry       *time.Timer         // pending retry of a failed sync
	retryDelay  time.Duration       // the last retry's delay; zero after a success
	// requests counts sync requests. A quiet source's running sync that
	// sees it grow runs once more, serving requests made while it ran.
	requests int
	// running is set from a request starting a sync until that sync
	// decides not to run again. A request made meanwhile only counts.
	running bool
	// owed: one more run is to follow the running one, for an explicit
	// refresh it would otherwise lose (request). One bit, so any number of
	// requests owe one run. Never set for a quiet source.
	owed bool
	// announced: the running sync's end has been reported, and it has not
	// given the provider up yet. Whoever saw that end and asks again is
	// asking for a new sync.
	announced bool
	// requiredSeq counts the syncs a sign-in required; serving is its value
	// when the running sync began, which the UI gets as the run's ticket.
	requiredSeq, serving uint64

	// held (ddsonic): the provider's quota is exhausted. No sync or worker
	// runs automatically (startup, retry, after a sync) until a sync
	// succeeds; one the user asks for still runs.
	held bool
}

// syncKind is who asks for a sync.
type syncKind int

const (
	// syncBackground is a retry timer, the startup sync, a quiet source's
	// own change: while a sync runs it is that sync.
	syncBackground syncKind = iota
	// syncExplicit is "r": while a sync runs it is that sync, unless the
	// sync's end has been reported already; then it is owed its own.
	syncExplicit
	// syncRequired is the sync a successful sign-in needs: always owed.
	syncRequired
)

// openCatalog opens the catalog with a sync source for Spotify and YouTube
// Music, when they are configured, for the music folder, when there is one,
// and for your radio stations. A catalog that cannot open is logged and left out.
func openCatalog(sp *spotify.SpotifyProvider, rp *radio.Provider, cfg config.Config) *catalogRuntime {
	rt := &catalogRuntime{providers: map[string]*providerSync{}, retryMin: syncRetryMin, retryMax: syncRetryMax}
	rt.ctx, rt.cancel = context.WithCancel(context.Background())
	path, err := appdir.LibraryDBPath()
	if err == nil {
		ctx, cancel := context.WithTimeout(rt.ctx, catalogOpenTimeout)
		rt.store, err = sqlite.Open(ctx, path)
		cancel()
	}
	if err != nil {
		applog.Warn("catalog unavailable, browsing live: %v", err)
		return rt
	}
	var sources []source
	if sp != nil {
		held := rt.keepRateLimit(sp)
		src := spotifysrc.New(sp)
		rt.filler = catalogsync.NewFiller(rt.store, src, catalogsync.DefaultPacing)
		sources = append(sources, source{Source: src, refresh: cfg.Ddsonic.SpotifyRefresh, worker: rt.filler, held: held})
	}
	if client := youtubeClient(cfg.YouTubeMusic); client != nil {
		// Tracks are enriched with their artist, album and year through
		// yt-dlp: with the browser's cookies when set, else (OAuth-only
		// sign-in) anonymously.
		meta := ytmusic.NewCookieCatalog(strings.TrimSpace(cfg.YouTubeMusic.CookiesFrom))
		enricher := catalogsync.NewEnricher(rt.store, meta, enrichPacing, func() {
			rt.notify(catalogsync.Event{Kind: catalogsync.CollectionDone, Provider: catalog.YouTube, Collection: catalog.CollectionDerived})
		})
		sources = append(sources, source{Source: youtubesrc.New(client, cfg.Ddsonic.YouTubePlaylists...),
			refresh: cfg.Ddsonic.YouTubeRefresh, worker: enricher,
			lists: []string{catalog.CollectionAlbums, catalog.CollectionArtists}})
	}
	if dir, implicit := local.ResolveMusicDir(cfg.InitialDirectory); dir != "" {
		// Indexed at every startup: it rereads only changed files and
		// writes nothing when none changed.
		src := localsrc.New(dir, rt.store)
		if implicit {
			src = localsrc.NewDefault(dir, rt.store) // ddsonic: an absent ~/Music is empty (#82)
		}
		sources = append(sources, source{Source: src})
	}
	if rp != nil {
		// Favorites and radios.toml, read from local files at every startup.
		sources = append(sources, source{Source: radiosrc.New(rp), quiet: true})
	}
	rt.setSources(sources...)
	if rp != nil && rt.engine != nil {
		rp.OnFavoritesToggled(func() { rt.sync(catalog.Radio) })
	}
	return rt
}

// ytdlpAvailable reports whether yt-dlp is installed; replaced in tests.
var ytdlpAvailable = player.YTDLPAvailable

// youtubeClient reads the YouTube Music account the way it is signed in:
// cookies, an own OAuth client, or both (Liked Music through OAuth,
// playlists through cookies). It returns nil when YouTube is disabled or
// not signed in, or yt-dlp, which plays its tracks, is missing.
func youtubeClient(yt config.YouTubeMusicConfig) youtubesrc.Client {
	if yt.Disabled || !ytdlpAvailable() {
		return nil
	}
	cookies := strings.TrimSpace(yt.CookiesFrom)
	oauth := strings.TrimSpace(yt.ClientID) != "" && strings.TrimSpace(yt.ClientSecret) != ""
	switch {
	case oauth && cookies != "":
		return youtubesrc.Mixed{OAuth: ytmusic.NewOAuthCatalog(yt.ClientID, yt.ClientSecret), Cookies: ytmusic.NewCookieCatalog(cookies)}
	case oauth:
		return ytmusic.NewOAuthCatalog(yt.ClientID, yt.ClientSecret)
	case cookies != "":
		return ytmusic.NewCookieCatalog(cookies)
	}
	return nil
}

// setSources creates the engine for sources and reads their stored status.
// A source syncs at startup when incomplete or older than its refresh.
func (rt *catalogRuntime) setSources(sources ...source) {
	if len(sources) == 0 || rt.store == nil {
		return
	}
	engineSources := make([]catalogsync.Source, len(sources))
	for i, src := range sources {
		engineSources[i] = src.Source
	}
	rt.engine = catalogsync.New(rt.store, rt.notify, engineSources...)
	for _, src := range sources {
		ps := &providerSync{worker: src.worker, lists: src.lists, quiet: src.quiet, collections: src.Collections(), held: src.held}
		var complete bool
		ps.startup, complete = rt.status(src.Provider(), len(ps.collections))
		// A sync that failed last time runs again, whatever its age: its
		// retry timer did not outlive the process.
		ps.stale = !complete || ps.startup.LastError != "" || time.Since(ps.startup.LastSuccess) > src.refresh
		rt.providers[src.Provider()] = ps
	}
}

// collections returns what provider's sync covers, or nil when it is not
// synced, so its library menu offers matching lists.
func (rt *catalogRuntime) collections(provider string) []string {
	if ps := rt.providers[provider]; ps != nil && rt.catalog() != nil {
		return append(slices.Clone(ps.collections), ps.lists...)
	}
	return nil
}

// catalog returns the catalog for the library, or nil to browse live.
func (rt *catalogRuntime) catalog() catalog.Catalog {
	if rt.store == nil || rt.engine == nil {
		return nil
	}
	if rt.filler == nil {
		return rt.store
	}
	return fillingCatalog{rt.store, rt.filler}
}

var _ catalogsync.AlbumStore = (*sqlite.Store)(nil)

// fillingCatalog is the store plus the filler's on-demand album fetch, so
// opening an uncached album caches it.
type fillingCatalog struct {
	catalog.Catalog
	filler *catalogsync.Filler
}

func (c fillingCatalog) FetchAlbumTracks(ctx context.Context, album catalog.Album) ([]catalog.Track, error) {
	if album.Ref.Provider != catalog.Spotify {
		return nil, fmt.Errorf("fetch album %q: only Spotify albums can be fetched: %w", album.Title, catalog.ErrNotFound)
	}
	return c.filler.FetchAlbumTracks(ctx, album)
}

// configure gives the UI the stored sync status and the refresh action.
func (rt *catalogRuntime) configure(m *model.Model) {
	if rt.engine == nil {
		return
	}
	status := make(map[string]model.CatalogStatus, len(rt.providers))
	for provider, ps := range rt.providers {
		if !ps.quiet {
			status[provider] = ps.startup
		}
	}
	m.SetCatalogSync(status, rt.refresh)
	m.SetCatalogSyncRequired(rt.refreshRequired)
}

// status sums up a provider's collections: the oldest success (zero unless
// every collection has synced) and any stored error.
func (rt *catalogRuntime) status(provider string, collections int) (st model.CatalogStatus, complete bool) {
	stored, err := rt.store.SyncStatus(rt.ctx, provider)
	if err != nil {
		return st, false
	}
	complete = len(stored) >= collections
	for i, c := range stored {
		if c.LastSuccess.IsZero() {
			complete = false
		}
		if i == 0 || c.LastSuccess.Before(st.LastSuccess) {
			st.LastSuccess = c.LastSuccess
		}
		if c.LastError != "" {
			st.LastError = c.LastError
		}
	}
	if !complete {
		st.LastSuccess = time.Time{}
	}
	return st, complete
}

// start connects sync events to the program and syncs each provider that
// needs it. Uncached Spotify albums are filled after its sync, or at once
// when none is due.
func (rt *catalogRuntime) start(prog *tea.Program) {
	if rt.engine == nil {
		return
	}
	rt.send = prog.Send
	rt.startSyncs()
}

// startSyncs is start's work once events have somewhere to go.
func (rt *catalogRuntime) startSyncs() {
	for provider, ps := range rt.providers {
		rt.mu.Lock()
		held := ps.held
		rt.mu.Unlock()
		if held {
			applog.Info("catalog sync %s: quota exceeded earlier; not syncing automatically (press r to sync)", provider)
			continue
		}
		if ps.stale {
			rt.sync(provider)
		} else if ps.worker != nil {
			rt.runWorker(provider, ps.worker)
		}
	}
}

// refresh syncs provider now, or every provider when it is "". It is what
// "r" in the library calls.
func (rt *catalogRuntime) refresh(provider string) {
	if provider != "" {
		rt.request(provider, syncExplicit)
		return
	}
	for p := range rt.providers {
		rt.request(p, syncExplicit)
	}
}

// refreshRequired syncs provider because the user has just signed in to it:
// that sync runs even when an older one still owns the provider, after it.
// It returns the ticket the required run will carry (CatalogSyncMsg.Ticket),
// so the library can tell an older run's end from it; zero when no sync can
// run.
func (rt *catalogRuntime) refreshRequired(provider string) uint64 {
	return rt.request(provider, syncRequired)
}

// sync starts a background sync of provider (request).
func (rt *catalogRuntime) sync(provider string) { rt.request(provider, syncBackground) }

// request starts a sync of provider in the background. A sync of a provider
// with a background worker pauses it while it runs and runs it after, so
// it covers what the sync brought. A sync already running makes it a
// no-op, except for a quiet source: its sync is instant and follows a
// local change (a favorite toggled), so the running sync runs once more
// to include it. Requests made during one sync share that one more run.
//
// An explicit request is not lost behind a running sync either: the sync a
// sign-in requires, and an "r" pressed after the running sync's end was
// reported, are owed one run, which follows when the running sync gives the
// provider up. Owed is one bit: there is no queue, and a run cannot owe
// itself another (its own retry is a background request).
func (rt *catalogRuntime) request(provider string, kind syncKind) (ticket uint64) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ps := rt.providers[provider]
	if rt.engine == nil || rt.closed || ps == nil {
		return 0
	}
	ps.requests++
	if kind == syncRequired {
		ps.requiredSeq++
		ticket = ps.requiredSeq
	}
	if ps.running {
		// The running sync owns the provider. Starting another here would
		// race it: arriving after its Sync returned, both would run.
		if !ps.quiet && (kind == syncRequired || (kind == syncExplicit && ps.announced)) {
			ps.owed = true
		}
		return ticket
	}
	ps.running, ps.owed, ps.announced, ps.serving = true, false, false, ps.requiredSeq
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		release := func() {}
		if ps.worker != nil {
			release = ps.worker.Hold()
		}
		var err error
		var held, cleared bool
		for {
			rt.mu.Lock()
			seen := ps.requests
			rt.mu.Unlock()
			err = rt.engine.Sync(rt.ctx, provider)
			// Deciding to stop and giving the provider up are one step, so
			// a request is either seen here or starts its own sync.
			rt.mu.Lock()
			again := ps.quiet && err == nil && ps.requests != seen && !rt.closed
			// The running sync, or quitting, owns what happens next.
			owned := errors.Is(err, catalogsync.ErrRunning) || rt.ctx.Err() != nil
			// An owed run follows this one, in the step that takes the debt,
			// so one request owes one run. Its own result settles the retry;
			// this run's does not.
			if !again && ps.owed && !owned && !rt.closed {
				again = true
				ps.announced, ps.serving = false, ps.requiredSeq
			}
			ps.owed = false
			wasHeld := ps.held
			if !again {
				// The retry is settled before the provider is given up:
				// settled later, this sync's result could undo the retry
				// of a newer sync that failed meanwhile.
				if !owned {
					rt.scheduleRetryLocked(ps, provider, err)
				}
				ps.running = false
			}
			held, cleared = ps.held, wasHeld && !ps.held
			rt.mu.Unlock()
			if !again {
				break
			}
		}
		release()
		if cleared {
			rt.clearQuota(provider)
		}
		if errors.Is(err, catalogsync.ErrRunning) || rt.ctx.Err() != nil {
			return
		}
		if err != nil {
			applog.Info("catalog sync: %v", err)
		}
		if ps.worker != nil && !held {
			rt.runWorker(provider, ps.worker)
		}
	}()
	return ticket
}

// scheduleRetry arranges provider's next sync after one finished with err:
// after a failure, a retry on a doubling delay, or when the provider's rate
// limit lasts longer, at its end; after a success, none, and the delay
// resets. An exhausted quota holds the provider: no retry is armed, for that
// or any later failure, until a sync succeeds. It returns the retry's delay,
// zero when none is armed.
func (rt *catalogRuntime) scheduleRetry(provider string, err error) time.Duration {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	ps := rt.providers[provider]
	if ps == nil {
		return 0
	}
	return rt.scheduleRetryLocked(ps, provider, err)
}

// scheduleRetryLocked is scheduleRetry for a caller holding rt.mu.
func (rt *catalogRuntime) scheduleRetryLocked(ps *providerSync, provider string, err error) time.Duration {
	if ps.retry != nil {
		ps.retry.Stop()
		ps.retry = nil
	}
	if err == nil {
		ps.retryDelay = 0
		ps.held = false
		return 0
	}
	if rt.closed {
		return 0
	}
	if quotaExceeded(err) {
		ps.held = true
	}
	if ps.held {
		applog.Info("catalog sync %s: quota exceeded; not retrying automatically (press r to sync)", provider)
		return 0
	}
	ps.retryDelay = catalogsync.NextBackoff(ps.retryDelay, rt.retryMin, rt.retryMax)
	delay := ps.retryDelay
	if rl, ok := errors.AsType[*catalog.RateLimitError](err); ok {
		delay = max(delay, rl.RetryAfter)
	}
	applog.Info("catalog sync %s: retrying in %v", provider, delay.Round(time.Second))
	ps.retry = time.AfterFunc(delay, func() { rt.sync(provider) })
	return delay
}

// rateLimitKeeper is the part of the Spotify provider keepRateLimit uses.
type rateLimitKeeper interface {
	SetRateLimitedUntil(until time.Time, quota bool)
	OnRateLimited(fn func(until time.Time, quota bool))
}

// keepRateLimit restores Spotify's rate-limit block, and its exhausted-quota
// mark, from the catalog, and records each new one there, short blocks
// included, so a restart waits it out too. It reports whether the quota is
// held. A store that cannot be read is taken conservatively: the gate closes
// for spotify.MinCooldown, and the quota is held. A failed write is logged;
// the provider's own gate stays closed whatever the store does.
func (rt *catalogRuntime) keepRateLimit(sp rateLimitKeeper) (held bool) {
	now := time.Now()
	held, err := rt.store.QuotaExceeded(rt.ctx, catalog.Spotify)
	if err != nil {
		applog.Warn("catalog: %v; holding Spotify's automatic syncs", err)
		held = true
	}
	until, err := rt.store.RateLimitedUntil(rt.ctx, catalog.Spotify)
	if err != nil {
		applog.Warn("catalog: %v; pausing Spotify for %v", err, spotify.MinCooldown)
		until = now.Add(spotify.MinCooldown)
	}
	// A clock set wrong once must not block Spotify for good.
	if limit := now.Add(spotify.MaxRateLimit); until.After(limit) {
		until = limit
	}
	if until.After(now) {
		applog.Info("spotify: rate limited until %s", until.Format(time.DateTime))
		sp.SetRateLimitedUntil(until, held)
	}
	sp.OnRateLimited(func(until time.Time, quota bool) {
		applog.Warn("spotify: rate limited until %s (quota exceeded: %t)", until.Format(time.DateTime), quota)
		if err := rt.store.SetRateLimitedUntil(rt.ctx, catalog.Spotify, until); err != nil {
			applog.Warn("catalog: %v", err)
		}
		if quota {
			if err := rt.store.SetQuotaExceeded(rt.ctx, catalog.Spotify, time.Now()); err != nil {
				applog.Warn("catalog: %v", err)
			}
		}
	})
	return held
}

// clearQuota removes provider's exhausted-quota mark after a sync
// succeeded. A failure leaves it, so the next run holds again: the safe side.
func (rt *catalogRuntime) clearQuota(provider string) {
	if err := rt.store.ClearQuotaExceeded(rt.ctx, provider); err != nil {
		applog.Warn("catalog: %v", err)
	}
}

// quotaExceeded reports whether err holds a *catalog.RateLimitError for an
// exhausted quota, anywhere among joined errors.
func quotaExceeded(err error) bool {
	switch e := err.(type) {
	case nil:
		return false
	case *catalog.RateLimitError:
		if e.Quota {
			return true
		}
	case interface{ Unwrap() []error }:
		return slices.ContainsFunc(e.Unwrap(), quotaExceeded)
	}
	return quotaExceeded(errors.Unwrap(err))
}

// runWorker runs a source's background worker. A run already going makes
// it a no-op; that run picks up new work itself.
func (rt *catalogRuntime) runWorker(provider string, w worker) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return
	}
	rt.wg.Add(1)
	go func() {
		defer rt.wg.Done()
		err := w.Run(rt.ctx)
		if err != nil && !errors.Is(err, catalogsync.ErrRunning) && !errors.Is(err, context.Canceled) {
			applog.Info("catalog %s background work: %v", provider, err)
		}
	}()
}

// enrichPacing paces YouTube enrichment: each read is a full yt-dlp run of
// about 4 s, and YouTube answers too many with a bot check, so a refusal
// waits long.
var enrichPacing = catalogsync.Pacing{Delay: time.Second, MinBackoff: time.Minute, MaxBackoff: 30 * time.Minute, MaxFailures: 3}

// notify forwards engine events, in order, from the syncing goroutine.
// start runs just before the program does, and prog.Send returns once the
// program has exited, so this cannot hang.
func (rt *catalogRuntime) notify(ev catalogsync.Event) {
	if rt.send == nil {
		return
	}
	if ps := rt.providers[ev.Provider]; ps != nil && ps.quiet {
		// No status, only that the sync, sweep included, changed the rows
		// (a station starred or unstarred), so search rereads them.
		if ev.Kind == catalogsync.Finished {
			rt.send(model.CatalogChangedMsg{Provider: ev.Provider})
		}
		return
	}
	var phase model.CatalogSyncPhase
	switch ev.Kind {
	case catalogsync.Started:
		phase = model.CatalogSyncStarted
	case catalogsync.CollectionDone:
		phase = model.CatalogSyncCollectionDone
	case catalogsync.Finished:
		phase = model.CatalogSyncFinished
	}
	// The end is marked as reported before it is sent, so whoever sees it
	// and asks again is owed a run (request). Not sent under the lock.
	var ticket uint64
	if ps := rt.providers[ev.Provider]; ps != nil {
		rt.mu.Lock()
		if ev.Kind == catalogsync.Finished {
			ps.announced = true
		}
		ticket = ps.serving
		rt.mu.Unlock()
	}
	rt.send(model.CatalogSyncMsg{Provider: ev.Provider, Phase: phase, Collection: ev.Collection, Err: ev.Err, Ticket: ticket})
}

// close stops any sync, waits briefly for it, and closes the catalog.
func (rt *catalogRuntime) close() {
	rt.mu.Lock()
	rt.closed = true
	for _, ps := range rt.providers {
		if ps.retry != nil {
			ps.retry.Stop()
		}
	}
	rt.mu.Unlock()
	rt.cancel()
	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(catalogStopTimeout):
		// Leave the store open: the sync still holds it. The process is
		// exiting, and SQLite's WAL keeps the file consistent regardless.
		applog.Warn("catalog sync did not stop within %v", catalogStopTimeout)
		return
	}
	if rt.store != nil {
		if err := rt.store.Close(); err != nil {
			applog.Warn("close catalog: %v", err)
		}
	}
}
