// Package localsrc is the catalog sync source for the local music folder.
// It walks the folder, rereads tags only for files whose size or modification
// time changed, and groups every file into albums (by album tag within an
// album folder, disc folders merged) and artists (a multi-artist album is
// credited to Various Artists and to each of its artists).
package localsrc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bjarneo/cliamp/catalog"
	"github.com/bjarneo/cliamp/catalogsync"
	"github.com/bjarneo/cliamp/player"
	"github.com/bjarneo/cliamp/playlist"
)

// Files is the source's one collection: every audio file under the folder.
const Files = "files"

// tagBatch is how many changed files are read between cancellation checks.
const tagBatch = 256

// Index is the part of the catalog the source reads to skip unchanged files.
type Index interface {
	IndexedFiles(ctx context.Context) ([]catalog.IndexedFile, error)
}

// Source indexes one music folder.
type Source struct {
	dir      string
	index    Index
	readTags func(paths []string) []playlist.Track // replaced in tests
}

var _ catalogsync.Source = (*Source)(nil)

// indexerVersion is the version of the grouping rules in index.go. Bump it
// when they change: every file is then regrouped once, from its stored
// tags. A rule that needs a tag the catalog does not store must also make
// the files read again.
const indexerVersion = 1

// New returns a Source for dir, comparing against index.
func New(dir string, index Index) *Source {
	return &Source{dir: dir, index: index, readTags: readTags}
}

// Provider implements catalogsync.Source.
func (*Source) Provider() string { return catalog.Local }

// Collections implements catalogsync.Source.
func (*Source) Collections() []string { return []string{Files} }

// Fetch implements catalogsync.Source. It fails, leaving the catalog as it
// is, when the folder is missing, or is empty while the catalog holds files
// (an unmounted drive looks like that). Files under a subfolder it cannot
// read are kept as last indexed rather than dropped.
func (s *Source) Fetch(ctx context.Context, collection string, known catalogsync.Known) (catalog.Snapshot, error) {
	if collection != Files {
		return catalog.Snapshot{}, fmt.Errorf("unknown collection %q", collection)
	}
	indexed, err := s.index.IndexedFiles(ctx)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	found, unreadable, err := walk(ctx, s.dir)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	if len(found) == 0 && len(indexed) > 0 {
		return catalog.Snapshot{}, fmt.Errorf("no music found in %s; keeping the %d indexed files", s.dir, len(indexed))
	}

	stored := make(map[string]catalog.IndexedFile, len(indexed))
	for _, f := range indexed {
		stored[f.Path] = f
	}
	// Unchanged files keep their stored track, converted only once the
	// index is known to need a rewrite: nothing changing is the common case.
	files := make([]file, 0, len(found))
	var changed []int // indexes into files
	for _, st := range found {
		if prev, ok := stored[st.Path]; ok && prev.Size == st.Size && prev.MTimeNS == st.MTimeNS {
			files = append(files, file{stat: st, stored: &prev.Track})
		} else {
			changed = append(changed, len(files))
			files = append(files, file{stat: st})
		}
		delete(stored, st.Path)
	}
	// What is left was not found: deleted, unless it sits in a folder the
	// walk could not read.
	removed := 0
	for _, prev := range stored {
		if under(prev.Path, unreadable) {
			files = append(files, file{stat: prev.FileStat, stored: &prev.Track})
		} else {
			removed++
		}
	}
	if len(changed) == 0 && removed == 0 && known.Collections[Files].Version == indexerVersion {
		return catalog.Snapshot{}, catalogsync.ErrUnchanged
	}
	for i := range files {
		if f := &files[i]; f.stored != nil {
			f.track = storedTrack(*f.stored, f.stat.Path)
		}
	}

	for start := 0; start < len(changed); start += tagBatch {
		if err := ctx.Err(); err != nil {
			return catalog.Snapshot{}, err
		}
		batch := changed[start:min(start+tagBatch, len(changed))]
		paths := make([]string, len(batch))
		for i, fi := range batch {
			paths[i] = files[fi].stat.Path
		}
		for i, t := range s.readTags(paths) {
			t.Path = paths[i]
			files[batch[i]].track = t
		}
	}
	snap := snapshot(files)
	snap.Version = indexerVersion
	return snap, nil
}

// file is one indexed audio file and its track.
type file struct {
	stat   catalog.FileStat
	track  playlist.Track
	stored *catalog.Track // an unchanged file's catalog row, until converted to track
}

// walk lists the audio files under dir with their size and modification
// time. It fails if dir itself cannot be read; unreadable subfolders are
// skipped and returned.
func walk(ctx context.Context, dir string) ([]catalog.FileStat, []string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("music folder: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("music folder %s is not a directory", dir)
	}
	// WalkDir does not descend into a symlinked root (a ~/Music linked to
	// another disk); a trailing separator makes it. Paths keep the link.
	root := dir
	if li, err := os.Lstat(dir); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		root = dir + string(filepath.Separator)
	}
	var files []catalog.FileStat
	var unreadable []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			if path == root {
				return fmt.Errorf("music folder: %w", err)
			}
			if errors.Is(err, fs.ErrNotExist) {
				return nil // deleted mid-walk: gone
			}
			unreadable = append(unreadable, path) // keeps its stored files
			if d == nil || d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !player.SupportedExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		fi, err := os.Stat(path) // follows symlinks, as playback does
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil // a dangling symlink, or deleted mid-walk: gone
		case err != nil:
			unreadable = append(unreadable, path)
			return nil
		case !fi.Mode().IsRegular():
			return nil
		}
		files = append(files, catalog.FileStat{Path: path, Size: fi.Size(), MTimeNS: fi.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, unreadable, nil
}

// under reports whether path is one of roots or inside one.
func under(path string, roots []string) bool {
	for _, r := range roots {
		if path == r || strings.HasPrefix(path, r+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// storedTrack rebuilds the tags of an unchanged file from its catalog row.
func storedTrack(t catalog.Track, path string) playlist.Track {
	return playlist.Track{
		Path:         path,
		Title:        t.Title,
		Artist:       t.Artist,
		Album:        t.AlbumTitle,
		Genre:        t.Genre,
		Year:         t.Year,
		TrackNumber:  t.TrackNo,
		DurationSecs: int(t.Duration.Seconds()),
	}
}
