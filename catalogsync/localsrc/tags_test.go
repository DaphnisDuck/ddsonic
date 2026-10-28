package localsrc

import (
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// A file whose tags crash the reader is indexed by its name; the others keep
// their tags and their order.
func TestReadTagsSurvivesAPanickingFile(t *testing.T) {
	orig := tagReader
	t.Cleanup(func() { tagReader = orig })
	tagReader = func(path string) playlist.Track {
		if path == "/m/bad.mp3" {
			panic("malformed tag")
		}
		return playlist.Track{Path: path, Title: "tagged " + path}
	}
	paths := []string{"/m/a.mp3", "/m/bad.mp3", "/m/b.mp3"}
	got := readTags(paths)
	if len(got) != 3 || got[0].Title != "tagged /m/a.mp3" || got[2].Title != "tagged /m/b.mp3" {
		t.Fatalf("tracks = %+v", got)
	}
	if got[1].Path != "/m/bad.mp3" || got[1].Title != "bad" {
		t.Errorf("bad file = %+v, want indexed by its name", got[1])
	}
}
