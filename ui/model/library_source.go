package model

// ddsonic: the settings pane's SRC row while the library is enabled. It names
// the source of the playing track instead of cliamp's provider pill, which
// the library replaced. The library records the source on each track it
// plays; a track queued by cliamp's own paths (a file or URL argument, the
// file browser, a provider's search) shows what its path tells, or nothing.

import (
	"maps"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/library"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
)

// librarySourceMeta is the ProviderMeta key holding a track's catalog
// provider, set when the library plays it.
const librarySourceMeta = appdir.Name + ".source"

// withLibrarySource returns tracks with source recorded on each, leaving the
// caller's tracks (and their shared meta maps) untouched.
func withLibrarySource(tracks []playlist.Track, source string) []playlist.Track {
	if source == "" {
		return tracks
	}
	out := make([]playlist.Track, len(tracks))
	for i, t := range tracks {
		meta := maps.Clone(t.ProviderMeta)
		if meta == nil {
			meta = make(map[string]string, 1)
		}
		meta[librarySourceMeta] = source
		t.ProviderMeta = meta
		out[i] = t
	}
	return out
}

// trackSource is the catalog provider a track comes from: as the library
// recorded it, else as its path shows, else "".
func trackSource(t playlist.Track) string {
	if s := t.Meta(librarySourceMeta); s != "" {
		return s
	}
	switch {
	case t.Path == "":
		return ""
	case strings.HasPrefix(t.Path, "spotify:"):
		return catalog.Spotify
	case playlist.IsYouTubeURL(t.Path) || playlist.IsYouTubeMusicURL(t.Path):
		return catalog.YouTube
	case t.Realtime:
		return catalog.Radio
	case !t.Stream && !t.Feed && !playlist.IsURL(t.Path):
		return catalog.Local
	}
	return ""
}

// libSourceRow is the SRC row for the playing track's source, fitted to w,
// or "" when the queue is empty or the source is unknown.
func (m Model) libSourceRow(w int) string {
	if m.playlist == nil {
		return ""
	}
	t, idx := m.playlist.Current()
	if idx < 0 {
		return ""
	}
	name := library.SourceLabel(trackSource(t))
	if name == "" {
		return ""
	}
	label := labelStyle.Render("SRC ")
	name = truncate(name, max(1, w-lipgloss.Width(label)-2))
	return label + dimStyle.Render("[") + trackStyle.Render(name) + dimStyle.Render("]")
}

// libSourcePill is libSourceRow across the frame, for the layouts without
// the settings pane.
func (m Model) libSourcePill() string { return m.libSourceRow(ui.PanelWidth) }
