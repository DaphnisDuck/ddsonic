package radio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newChannelFavoritesAt(t *testing.T) (*ChannelFavorites, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	return LoadChannelFavorites(), dir
}

func TestChannelFavoritesTogglePersistsByID(t *testing.T) {
	f, dir := newChannelFavoritesAt(t)
	added, err := f.Toggle(ChannelFavorite{ID: "lofi", Name: "Lofi"})
	if err != nil || !added {
		t.Fatalf("Toggle add = %v, %v; want true, nil", added, err)
	}
	if !f.Contains("lofi") {
		t.Fatal("after add: not Contains")
	}

	reloaded := LoadChannelFavorites()
	if got := reloaded.Channels(); len(got) != 1 || got[0] != (ChannelFavorite{ID: "lofi", Name: "Lofi"}) {
		t.Fatalf("reloaded = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, favoritesFile)); !os.IsNotExist(err) {
		t.Fatalf("station favorites file touched: %v", err)
	}

	removed, err := f.Toggle(ChannelFavorite{ID: "lofi", Name: "Lofi"})
	if err != nil || removed {
		t.Fatalf("Toggle remove = %v, %v; want false, nil", removed, err)
	}
	if got := LoadChannelFavorites().Channels(); len(got) != 0 {
		t.Fatalf("after remove = %+v", got)
	}
}

// A channel renamed on the server stays one favorite: the ID is the key and
// the saved name is kept (issue #135, owner clarification of 2026-11-25).
func TestChannelFavoritesRenameIsNotADuplicate(t *testing.T) {
	a, dir := newChannelFavoritesAt(t)
	b := LoadChannelFavorites() // another instance that has not seen the add
	if _, err := a.Toggle(ChannelFavorite{ID: "lofi", Name: "Lofi"}); err != nil {
		t.Fatal(err)
	}
	// b still shows lofi as not favorite, so its intent is "add": already
	// applied on disk, a no-op rather than a second entry.
	added, err := b.Toggle(ChannelFavorite{ID: "lofi", Name: "Lofi Beats"})
	if err != nil || !added {
		t.Fatalf("Toggle = %v, %v; want true, nil", added, err)
	}
	got := LoadChannelFavorites().Channels()
	if len(got) != 1 || got[0].Name != "Lofi" {
		t.Fatalf("channels = %+v; want one lofi, saved name kept", got)
	}
	// The loader drops a repeated ID, so check the file itself: one entry.
	data, err := os.ReadFile(filepath.Join(dir, channelFavoritesFile))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "[[channel]]"); n != 1 {
		t.Fatalf("file has %d [[channel]] entries, want 1:\n%s", n, data)
	}
}

func TestChannelFavoritesUnreadableFileMeansNone(t *testing.T) {
	_, dir := newChannelFavoritesAt(t)
	path := filepath.Join(dir, channelFavoritesFile)
	if err := os.WriteFile(path, []byte("[[channel]]\nname = \"no id\"\n\n[[channel]]\nid = \"edm\"\n\n[[channel]]\nid = \"edm\"\nname = \"EDM\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadChannelFavorites().Channels()
	if len(got) != 1 || got[0] != (ChannelFavorite{ID: "edm", Name: "edm"}) {
		t.Fatalf("channels = %+v; want one edm, named by its ID", got)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil { // unreadable as a file
		t.Fatal(err)
	}
	if got := LoadChannelFavorites().Channels(); len(got) != 0 {
		t.Fatalf("unreadable file: channels = %+v", got)
	}
}
