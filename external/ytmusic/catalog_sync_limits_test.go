package ytmusic

import (
	"strings"
	"testing"
)

func TestValidIDAndURLs(t *testing.T) {
	for id, want := range map[string]bool{
		"dQw4w9WgXcQ":                        true,
		"PLrAXtmErZgOeiKm4sgNOknGvNjby9efdf": true,
		"UC-9-kyTW8ZkZNDHQJ6FgpwQ":           true,
		"":                                   false,
		"abc&list=evil":                      false,
		"a b":                                false,
		strings.Repeat("a", 65):              false,
	} {
		if got := validID(id); got != want {
			t.Errorf("validID(%q) = %v, want %v", id, got, want)
		}
	}
	// Real IDs keep their stored addresses; anything else cannot add query
	// parameters.
	if got := watchURL("dQw4w9WgXcQ"); got != "https://music.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("watchURL = %q", got)
	}
	if got := playlistURL("x&index=9"); got != "https://www.youtube.com/playlist?list=x%26index%3D9" {
		t.Errorf("playlistURL = %q", got)
	}
	if _, ok := (ytdlpEntry{ID: "x&v=y", Title: "t"}).record(); ok {
		t.Error("an entry with a malformed ID became a track")
	}
}

func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{limit: 8}
	if n, err := b.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatalf("first write: %d, %v", n, err)
	}
	if _, err := b.Write([]byte("6789")); err == nil || !b.over {
		t.Errorf("write past the limit: err %v, over %v; want an error", err, b.over)
	}
	if got := b.buf.String(); got != "12345" {
		t.Errorf("kept %q, want only what fit", got)
	}
}
