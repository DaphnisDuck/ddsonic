package resolve

import (
	"net/url"
	"testing"
)

func TestFilterRemoteEntries(t *testing.T) {
	base, err := url.Parse("https://cdn.example/lists/radio.m3u?k=1")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		base  *url.URL
		paths []string
		want  []string
	}{
		{
			name:  "http and https survive unchanged",
			base:  base,
			paths: []string{"http://a.com/1.mp3", "https://b.com/2.mp3"},
			want:  []string{"http://a.com/1.mp3", "https://b.com/2.mp3"},
		},
		{
			name:  "ssh entries are dropped",
			base:  base,
			paths: []string{"https://a.com/1.mp3", "ssh://evil/etc/passwd"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "file and data entries are dropped",
			base:  base,
			paths: []string{"file:///etc/passwd", "data:audio/mp3,AAAA", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "relative references resolve against the playlist URL",
			base:  base,
			paths: []string{"a.mp3", "sub/b.mp3", "../c.mp3", "/abs/d.mp3", "//other.example/e.mp3", "?q=2"},
			want: []string{
				"https://cdn.example/lists/a.mp3",
				"https://cdn.example/lists/sub/b.mp3",
				"https://cdn.example/c.mp3",
				"https://cdn.example/abs/d.mp3",
				"https://other.example/e.mp3",
				"https://cdn.example/lists/radio.m3u?q=2",
			},
		},
		{
			name:  "a local absolute path becomes a URL on the playlist host",
			base:  base,
			paths: []string{"/etc/passwd"},
			want:  []string{"https://cdn.example/etc/passwd"},
		},
		{
			name:  "windows drive paths are dropped",
			base:  base,
			paths: []string{`C:\music\c.mp3`, "c:/music/d.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "without a base, scheme-less entries are dropped",
			base:  nil,
			paths: []string{"a.mp3", "/abs/b.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "a non-http base cannot make a relative entry playable",
			base:  &url.URL{Scheme: "file", Path: "/srv/list.m3u"},
			paths: []string{"a.mp3"},
			want:  []string{},
		},
		{
			name:  "uppercase schemes are normalized to lowercase",
			base:  base,
			paths: []string{"HTTP://a.com/1.mp3", "HtTpS://b.com/2.mp3"},
			want:  []string{"http://a.com/1.mp3", "https://b.com/2.mp3"},
		},
		{
			name:  "kept entries are returned in normalized form",
			base:  base,
			paths: []string{"https://a.com/a b.mp3", "sub dir/c.mp3"},
			want:  []string{"https://a.com/a%20b.mp3", "https://cdn.example/lists/sub%20dir/c.mp3"},
		},
		{
			name:  "opaque http forms are dropped",
			base:  base,
			paths: []string{"http:relative.mp3", "HTTPS:a.mp3", "http:../x.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "hostless http forms are dropped",
			base:  base,
			paths: []string{"http:/etc/passwd", "http:///etc/passwd", "https://", "http://:80/x.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "malformed references are dropped",
			base:  base,
			paths: []string{"http://a.com/%zz.mp3", "http://[::1/x.mp3", "%zz.mp3", " http://a.com/x.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "a base without a host cannot resolve relative entries",
			base:  &url.URL{Scheme: "https", Opaque: "list.m3u"},
			paths: []string{"a.mp3", "/b.mp3", "https://a.com/1.mp3"},
			want:  []string{"https://a.com/1.mp3"},
		},
		{
			name:  "every entry dropped yields an empty list",
			base:  base,
			paths: []string{"ssh://a/b", "file:///c"},
			want:  []string{},
		},
		{
			name:  "empty input",
			base:  base,
			paths: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]m3uEntry, 0, len(tt.paths))
			for _, p := range tt.paths {
				entries = append(entries, m3uEntry{Path: p, Title: "t:" + p})
			}
			got := filterRemoteEntries(tt.base, entries, func(e *m3uEntry) *string { return &e.Path })
			if len(got) != len(tt.want) {
				t.Fatalf("filterRemoteEntries() returned %d entries, want %d: %+v", len(got), len(tt.want), got)
			}
			for i, w := range tt.want {
				if got[i].Path != w {
					t.Errorf("entry %d path = %q, want %q", i, got[i].Path, w)
				}
				if got[i].Title == "" {
					t.Errorf("entry %d lost its title", i)
				}
			}
		})
	}
}
