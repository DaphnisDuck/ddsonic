// Package catalogsync keeps the catalog current from providers. A Source
// fetches whole collections; the Engine applies each as one catalog
// transaction or, when anything fails, records the failure and leaves the
// cached collection untouched. The engine holds no SQL: catalog.Writer owns
// transactions, reconciliation and cleanup.
package catalogsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bjarneo/cliamp/catalog"
)

// Source fetches one provider's collections.
type Source interface {
	// Provider is the catalog provider name, e.g. catalog.Spotify.
	Provider() string
	// Collections lists the collection names to sync, in order.
	Collections() []string
	// Fetch returns the complete collection. If any page fails it returns
	// an error and no snapshot: a partial snapshot would reconcile away
	// everything it missed. ErrUnchanged means the stored collection is
	// already current.
	Fetch(ctx context.Context, collection string, known Known) (catalog.Snapshot, error)
}

// Known is what the catalog already holds that lets a source skip work.
type Known struct {
	// PlaylistSnapshots maps playlist provider IDs to their stored change
	// markers; a playlist whose marker is unchanged needs no track fetch.
	PlaylistSnapshots map[string]string
	// Collections holds what is stored of each synced collection, so a
	// source can tell cheaply that one is current.
	Collections map[string]catalog.CollectionState
}

// EventKind distinguishes sync progress events.
type EventKind int

// Events of one provider sync: Started, a CollectionDone per collection (Err
// set if it failed), then Finished with the joined errors.
const (
	Started EventKind = iota
	CollectionDone
	Finished
)

// Event reports sync progress, e.g. so the UI can refresh or show status.
type Event struct {
	Kind       EventKind
	Provider   string
	Collection string // CollectionDone only
	Err        error
}

// ErrRunning means a sync of that provider is already in progress.
var ErrRunning = errors.New("sync already running")

// ErrUnchanged is returned by Source.Fetch when the collection has not
// changed since the catalog stored it. The sync succeeds without writing.
var ErrUnchanged = errors.New("collection unchanged")

// Engine runs syncs. It is safe for concurrent use; each provider syncs at
// most once at a time.
type Engine struct {
	store   catalog.Writer
	notify  func(Event)
	sources map[string]Source

	mu      sync.Mutex
	running map[string]bool
}

// New returns an Engine writing to store. notify, if non-nil, receives
// events on the syncing goroutine and must not block.
func New(store catalog.Writer, notify func(Event), sources ...Source) *Engine {
	e := &Engine{store: store, notify: notify, sources: map[string]Source{}, running: map[string]bool{}}
	for _, s := range sources {
		e.sources[s.Provider()] = s
	}
	return e
}

// Sync syncs every collection of provider in order and returns their joined
// errors. A collection's failure does not stop the others. It returns
// ErrRunning at once if provider is already syncing.
func (e *Engine) Sync(ctx context.Context, provider string) error {
	src, ok := e.sources[provider]
	if !ok {
		return fmt.Errorf("sync %s: no such source", provider)
	}
	if !e.begin(provider) {
		return fmt.Errorf("sync %s: %w", provider, ErrRunning)
	}
	defer e.end(provider)

	e.emit(Event{Kind: Started, Provider: provider})
	err := e.syncAll(ctx, src)
	e.emit(Event{Kind: Finished, Provider: provider, Err: err})
	return err
}

func (e *Engine) syncAll(ctx context.Context, src Source) error {
	provider := src.Provider()
	snapshots, err := e.store.PlaylistSnapshots(ctx, provider)
	if err != nil {
		return fmt.Errorf("sync %s: %w", provider, err)
	}
	states, err := e.store.CollectionStates(ctx, provider)
	if err != nil {
		return fmt.Errorf("sync %s: %w", provider, err)
	}
	known := Known{PlaylistSnapshots: snapshots, Collections: states}
	var errs []error
	for _, collection := range src.Collections() {
		if ctx.Err() != nil {
			// Report the collections left unsynced rather than succeeding.
			return errors.Join(append(errs, fmt.Errorf("sync %s: %w", provider, ctx.Err()))...)
		}
		err := e.syncCollection(ctx, src, collection, known)
		e.emit(Event{Kind: CollectionDone, Provider: provider, Collection: collection, Err: err})
		if err != nil {
			errs = append(errs, err)
		}
	}
	// Collect what the applied snapshots left unreferenced. Sweeping is safe
	// after a failed collection too: it only deletes what nothing references.
	if ctx.Err() == nil {
		if err := e.store.Sweep(ctx, provider); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// failureRecordTimeout bounds recording a failure after the sync context
// may already be ending.
const failureRecordTimeout = 5 * time.Second

func (e *Engine) syncCollection(ctx context.Context, src Source, collection string, known Known) error {
	provider := src.Provider()
	snap, err := src.Fetch(ctx, collection, known)
	if errors.Is(err, ErrUnchanged) {
		if err := e.store.RecordSyncSuccess(ctx, provider, collection); err != nil {
			return fmt.Errorf("sync %s/%s: %w", provider, collection, err)
		}
		return nil
	}
	if err == nil && (snap.Provider != provider || snap.Collection != collection) {
		err = fmt.Errorf("source returned %s/%s", snap.Provider, snap.Collection)
	}
	if err == nil {
		err = e.store.ApplySnapshot(ctx, snap)
	}
	if err == nil {
		return nil
	}
	err = fmt.Errorf("sync %s/%s: %w", provider, collection, err)
	// A cancelled sync (shutdown, superseded) is not a provider failure.
	if ctx.Err() != nil {
		return err
	}
	// Record even if ctx is cancelled after the check above: the failure
	// already happened and the status must show it.
	recCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureRecordTimeout)
	defer cancel()
	if recErr := e.store.RecordSyncFailure(recCtx, provider, collection, err); recErr != nil {
		return errors.Join(err, recErr)
	}
	return err
}

func (e *Engine) begin(provider string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running[provider] {
		return false
	}
	e.running[provider] = true
	return true
}

func (e *Engine) end(provider string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.running, provider)
}

func (e *Engine) emit(ev Event) {
	if e.notify != nil {
		e.notify(ev)
	}
}
