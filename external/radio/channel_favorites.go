// ddsonic: favorite cliamp radio channels (issue #135). A separate store and
// file from the station favorites (favorites.go), so radio_favorites.toml,
// its parser and its locks stay upstream's.

package radio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/fileutil"
	"github.com/bjarneo/cliamp/internal/tomlutil"
)

const channelFavoritesFile = "radio_channel_favorites.toml"

// ChannelFavorite is a favorite cliamp radio channel. ID is its identity;
// Name is the name it had when it was starred, kept for display only.
type ChannelFavorite struct {
	ID   string
	Name string
}

// ChannelFavorites manages the persistent set of favorite channels. Its locks
// follow the station favorites' order (library_favorites.go): mutate, then
// the file's lock, and mu only to read the intent and publish the result.
type ChannelFavorites struct {
	mutate   sync.Mutex
	mu       sync.RWMutex
	revision uint64
	channels []ChannelFavorite
	byID     map[string]struct{}
	path     string
}

// LoadChannelFavorites reads radio_channel_favorites.toml from the config
// directory. A missing or unreadable file means no channel favorites.
func LoadChannelFavorites() *ChannelFavorites {
	f := &ChannelFavorites{byID: map[string]struct{}{}}
	dir, err := appdir.Dir()
	if err != nil {
		return f
	}
	f.path = filepath.Join(dir, channelFavoritesFile)
	channels, err := loadChannelFavorites(f.path)
	if err != nil {
		return f
	}
	f.channels, f.byID = channels, channelIDs(channels)
	return f
}

// Channels returns the favorite channels in saved order.
func (f *ChannelFavorites) Channels() []ChannelFavorite {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return slices.Clone(f.channels)
}

// Contains reports whether the channel ID is a favorite.
func (f *ChannelFavorites) Contains(id string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	_, ok := f.byID[id]
	return ok
}

// Revision changes when a successful toggle publishes a new local snapshot.
func (f *ChannelFavorites) Revision() uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.revision
}

// Toggle applies the opposite of this instance's displayed membership of
// c.ID to the latest file contents, and returns the new favorite state. An
// intent another instance already applied is a no-op on disk: a channel is
// never saved twice, and an existing entry keeps its saved name. It may wait
// for another ddsonic's save and for the disk: call it off the UI goroutine.
// A failed save leaves the local snapshot unchanged.
func (f *ChannelFavorites) Toggle(c ChannelFavorite) (bool, error) {
	if c.ID == "" {
		return false, errors.New("channel favorite: empty channel ID")
	}
	if c.Name == "" {
		c.Name = c.ID
	}
	f.mutate.Lock()
	defer f.mutate.Unlock()
	favorite := !f.Contains(c.ID)
	if f.path == "" { // written under f.mutate alone, as it is read
		dir, err := appdir.Dir()
		if err != nil {
			return false, fmt.Errorf("resolve channel favorites directory: %w", err)
		}
		f.path = filepath.Join(dir, channelFavoritesFile)
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return false, fmt.Errorf("create channel favorites directory: %w", err)
	}
	unlock, err := fileutil.LockFile(f.path + ".lock")
	if err != nil {
		return false, fmt.Errorf("lock channel favorites: %w", err)
	}
	defer func() { _ = unlock() }()

	channels, err := loadChannelFavorites(f.path)
	if err != nil {
		return false, fmt.Errorf("load channel favorites: %w", err)
	}
	byID := channelIDs(channels)
	if _, exists := byID[c.ID]; exists != favorite {
		if favorite {
			channels = append(channels, c)
			byID[c.ID] = struct{}{}
		} else {
			channels = slices.DeleteFunc(channels, func(x ChannelFavorite) bool { return x.ID == c.ID })
			delete(byID, c.ID)
		}
		if err := saveChannelFavorites(f.path, channels); err != nil {
			return false, fmt.Errorf("save channel favorites: %w", err)
		}
	}
	f.mu.Lock()
	f.channels, f.byID = channels, byID
	f.revision++
	f.mu.Unlock()
	return favorite, nil
}

func channelIDs(channels []ChannelFavorite) map[string]struct{} {
	ids := make(map[string]struct{}, len(channels))
	for _, c := range channels {
		ids[c.ID] = struct{}{}
	}
	return ids
}

// saveChannelFavorites writes the whole file atomically, so a failed write
// never truncates the existing favorites.
func saveChannelFavorites(path string, channels []ChannelFavorite) error {
	var b strings.Builder
	for i, c := range channels {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[[channel]]\nid = %q\nname = %q\n", c.ID, c.Name)
	}
	return fileutil.WriteFileAtomic(path, []byte(b.String()), 0o644)
}

// loadChannelFavorites parses the file. An entry without an ID is skipped, a
// repeated ID keeps its first entry, and a missing name falls back to the ID.
func loadChannelFavorites(path string) ([]ChannelFavorite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var channels []ChannelFavorite
	seen := map[string]struct{}{}
	tomlutil.ParseSections(data, "channel", func(kv map[string]string) {
		c := ChannelFavorite{ID: kv["id"], Name: kv["name"]}
		if c.ID == "" {
			return
		}
		if _, dup := seen[c.ID]; dup {
			return
		}
		seen[c.ID] = struct{}{}
		if c.Name == "" {
			c.Name = c.ID
		}
		channels = append(channels, c)
	})
	return channels, nil
}
