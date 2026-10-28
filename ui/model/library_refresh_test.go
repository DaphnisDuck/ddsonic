package model

// ddsonic: after a sync, an open album or playlist rereads the catalog alone,
// never repeating the live calls its first load may have made (review R9).

import (
	"context"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/library"
)

// fakeCachedLevel counts its live loads and its catalog rereads.
type fakeCachedLevel struct {
	live, cached []library.Entry
	ok           bool // the rows come from the catalog
	loads, reads int
}

func (l *fakeCachedLevel) Title() string { return "Album" }
func (l *fakeCachedLevel) Load(context.Context) ([]library.Entry, error) {
	l.loads++
	return l.live, nil
}
func (l *fakeCachedLevel) CachedProvider() string { return "spotify" }
func (l *fakeCachedLevel) LoadCached(context.Context) ([]library.Entry, bool, error) {
	l.reads++
	return l.cached, l.ok, nil
}

func rows(ids ...string) []library.Entry {
	out := make([]library.Entry, len(ids))
	for i, id := range ids {
		out[i] = library.Entry{ID: id, Title: id}
	}
	return out
}

func entryIDs(f *libFrame) []string {
	var out []string
	for _, e := range f.entries {
		out = append(out, e.ID)
	}
	return out
}

func synced(t *testing.T, m Model, provider string) Model {
	t.Helper()
	updated, cmd := m.Update(CatalogSyncMsg{Provider: provider, Phase: CatalogSyncCollectionDone})
	return libRun(t, updated.(Model), cmd)
}

func openCached(t *testing.T, l *fakeCachedLevel) Model {
	t.Helper()
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Album", Open: l}))
	return libPress(t, m, "enter")
}

func TestOpenCachedLevelRereadsAfterSync(t *testing.T) {
	l := &fakeCachedLevel{live: rows("a", "b", "c"), ok: true}
	m := openCached(t, l)
	m.libTop().cursor = 1 // on b
	l.cached = rows("x", "b")
	m = synced(t, m, "spotify")
	if got := entryIDs(m.libTop()); !slices.Equal(got, []string{"x", "b"}) || l.loads != 1 || l.reads != 1 {
		t.Errorf("after the sync: rows %q, %d loads, %d rereads; want the catalog's rows from one reread", got, l.loads, l.reads)
	}
	if m.libTop().cursor != 1 {
		t.Errorf("cursor = %d, want it still on b", m.libTop().cursor)
	}
	m = synced(t, m, "youtube") // another source: nothing to do
	if l.reads != 1 {
		t.Errorf("a YouTube sync reread a Spotify album")
	}
}

// Rows fetched live (an album not cached yet) stay when the reread finds the
// catalog does not have them.
func TestLiveRowsStayAfterSync(t *testing.T) {
	l := &fakeCachedLevel{live: rows("a", "b"), ok: false}
	m := openCached(t, l)
	m = synced(t, m, "spotify")
	if got := entryIDs(m.libTop()); !slices.Equal(got, []string{"a", "b"}) || l.loads != 1 {
		t.Errorf("rows %q after %d loads, want the live rows kept and no live load", got, l.loads)
	}
}

// A sync during the first load does not cancel it (it may be a live fetch);
// the reread follows once it lands.
func TestSyncDuringFirstLoadRereadsAfterIt(t *testing.T) {
	l := &fakeCachedLevel{live: rows("a"), cached: rows("a", "b"), ok: true}
	m := newLibraryModel(library.Menu("Music", library.Entry{Title: "Album", Open: l}))
	updated, load := m.Update(libKey("enter")) // the load, held
	m = updated.(Model)
	updated, cmd := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncCollectionDone})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("the sync started a reread while the first load ran")
	}
	m = libRun(t, m, load)
	if got := entryIDs(m.libTop()); !slices.Equal(got, []string{"a", "b"}) || l.loads != 1 || l.reads != 1 {
		t.Errorf("rows %q, %d loads, %d rereads; want the reread after the load", got, l.loads, l.reads)
	}
}

// A level below the top that a sync changed rereads when shown again.
func TestStaleCachedLevelRereadsOnReturn(t *testing.T) {
	l := &fakeCachedLevel{live: rows("a"), cached: rows("a", "b"), ok: true}
	child := library.Menu("Track")
	l.live[0].Open = child
	m := openCached(t, l)
	m = libPress(t, m, "enter") // into a's child
	m = synced(t, m, "spotify")
	if l.reads != 0 {
		t.Fatal("reread a level that is not shown")
	}
	m = libPress(t, m, "esc")
	if got := entryIDs(m.libTop()); !slices.Equal(got, []string{"a", "b"}) || l.loads != 1 || l.reads != 1 {
		t.Errorf("back on the album: rows %q, %d loads, %d rereads", got, l.loads, l.reads)
	}
}

var _ tea.Msg = CatalogSyncMsg{}

// A reread that leaves the rows (they were live) still honors a commit that
// landed while it ran.
func TestUnchangedRereadHonorsANewerCommit(t *testing.T) {
	l := &fakeCachedLevel{live: rows("live-a"), ok: false}
	m := openCached(t, l)
	updated, reread := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncCollectionDone})
	m = updated.(Model)
	unchanged := reread() // the album was not cached yet
	l.ok, l.cached = true, rows("a", "b")
	m = synced(t, m, "spotify") // cached meanwhile; the reread still runs
	updated, next := m.Update(unchanged)
	m = libRun(t, updated.(Model), next)
	if got := entryIDs(m.libTop()); !slices.Equal(got, []string{"a", "b"}) || l.reads != 2 {
		t.Errorf("rows %q after %d rereads; want the newer commit's rows", got, l.reads)
	}
}

// The sync's final sweep comes after the collections' rereads; the end of
// the sync rereads again.
func TestSyncEndRereads(t *testing.T) {
	l := &fakeCachedLevel{live: rows("a"), ok: true}
	m := openCached(t, l)
	l.cached = nil // swept
	updated, cmd := m.Update(CatalogSyncMsg{Provider: "spotify", Phase: CatalogSyncFinished})
	m = libRun(t, updated.(Model), cmd)
	if l.reads != 1 || len(m.libTop().entries) != 0 {
		t.Errorf("after the sync ended: %d rereads, rows %q", l.reads, entryIDs(m.libTop()))
	}
}
