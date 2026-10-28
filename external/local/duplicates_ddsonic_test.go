package local

// ddsonic: upstream 5450f73c's tests, without the cases that need 306c8b93
// (an addition placed between two saved tracks; ddsonic appends it).

import (
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// TestRebuildDocDuplicatePaths verifies that rebuildDoc matches the caller's
// tracks onto the original slots by occurrence, so a path that the document
// lists more than once keeps every copy. Titles tell the copies apart.
func TestRebuildDocDuplicatePaths(t *testing.T) {
	a1 := playlist.Track{Path: "/x/a.mp3", Title: "a1"}
	a2 := playlist.Track{Path: "/x/a.mp3", Title: "a2"}
	b := playlist.Track{Path: "/x/b.mp3", Title: "b"}
	c := playlist.Track{Path: "/x/c.mp3", Title: "c"}
	music := playlist.DirSource{Path: "/music", Recursive: true}
	plain := &playlistDoc{tracks: []playlist.Track{a1, b, a2}, order: []uint8{itemTrack, itemTrack, itemTrack}}
	withDir := &playlistDoc{
		tracks: []playlist.Track{a1, b, a2},
		dirs:   []playlist.DirSource{music},
		order:  []uint8{itemTrack, itemDir, itemTrack, itemTrack},
	}
	tests := []struct {
		name     string
		doc      *playlistDoc
		explicit []playlist.Track
		want     []string // track titles, or "dir" for a [[dir]] section
	}{
		{name: "no-op", doc: plain, explicit: []playlist.Track{a1, b, a2}, want: []string{"a1", "b", "a2"}},
		{name: "add at the end", doc: plain, explicit: []playlist.Track{a1, b, a2, c}, want: []string{"a1", "b", "a2", "c"}},
		{name: "add a third copy", doc: plain, explicit: []playlist.Track{a1, b, a2, a1}, want: []string{"a1", "b", "a2", "a1"}},
		{name: "remove the other track", doc: plain, explicit: []playlist.Track{a1, a2}, want: []string{"a1", "a2"}},
		{name: "remove the first copy", doc: plain, explicit: []playlist.Track{b, a2}, want: []string{"b", "a2"}},
		{name: "remove the second copy", doc: plain, explicit: []playlist.Track{a1, b}, want: []string{"a1", "b"}},
		{name: "reorder", doc: plain, explicit: []playlist.Track{b, a1, a2}, want: []string{"b", "a1", "a2"}},
		{name: "dir stays anchored on a no-op", doc: withDir, explicit: []playlist.Track{a1, b, a2}, want: []string{"a1", "dir", "b", "a2"}},
		{name: "dir stays anchored on a removal", doc: withDir, explicit: []playlist.Track{a1, a2}, want: []string{"a1", "dir", "a2"}},
		{name: "dir stays anchored on an addition", doc: withDir, explicit: []playlist.Track{a1, b, a2, c}, want: []string{"a1", "dir", "b", "a2", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracks, dirs, order := rebuildDoc(tt.doc, tt.explicit)
			var got []string
			ti, di := 0, 0
			for _, kind := range order {
				if kind == itemDir {
					got = append(got, "dir")
					di++
					continue
				}
				got = append(got, tracks[ti].Title)
				ti++
			}
			if ti != len(tracks) || di != len(dirs) {
				t.Fatalf("order has %d tracks and %d dirs, want %d and %d", ti, di, len(tracks), len(dirs))
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("sections = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDuplicateTrackWrites verifies that every write to an existing playlist
// keeps a track path that the playlist lists more than once.
func TestDuplicateTrackWrites(t *testing.T) {
	a, b, c := "/a.mp3", "/b.mp3", "/c.mp3"
	tests := []struct {
		name      string
		write     func(p *Provider) error
		wantPaths []string
	}{
		{
			name: "AddTracks",
			write: func(p *Provider) error {
				_, _, err := p.AddTracks("Mix", []playlist.Track{{Path: c}})
				return err
			},
			wantPaths: []string{a, b, a, c},
		},
		{
			name:      "RemoveTrack of the other track",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 1) },
			wantPaths: []string{a, a},
		},
		{
			name:      "RemoveTrack of the first copy",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 0) },
			wantPaths: []string{b, a},
		},
		{
			name:      "RemoveTrack of the second copy",
			write:     func(p *Provider) error { return p.RemoveTrack("Mix", 2) },
			wantPaths: []string{a, b},
		},
		{
			name: "SavePlaylist with the same tracks",
			write: func(p *Provider) error {
				return p.SavePlaylist("Mix", []playlist.Track{{Path: a}, {Path: b}, {Path: a}})
			},
			wantPaths: []string{a, b, a},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTestProvider(t)
			if err := p.SavePlaylist("Mix", []playlist.Track{{Path: a}, {Path: b}, {Path: a}}); err != nil {
				t.Fatal(err)
			}
			if err := tt.write(p); err != nil {
				t.Fatal(err)
			}
			tracks, err := p.Tracks("Mix")
			if err != nil {
				t.Fatal(err)
			}
			if got := paths(tracks); !slices.Equal(got, tt.wantPaths) {
				t.Fatalf("tracks = %v, want %v", got, tt.wantPaths)
			}
		})
	}
}
