package main

import "testing"

// Artwork loads only when it is on and the terminal draws images.
func TestArtworkLoaderGate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		env     map[string]string
		enabled bool
		want    bool
	}{
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, true, true},
		{"kitty, turned off", map[string]string{"TERM": "xterm-kitty"}, false, false},
		{"alacritty", map[string]string{"TERM": "alacritty"}, true, false},
		{"kitty in tmux", map[string]string{"TERM": "xterm-kitty", "TMUX": "x"}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"TERM", "TMUX", "KITTY_WINDOW_ID", "TERM_PROGRAM"} {
				t.Setenv(k, tt.env[k])
			}
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			if got := artworkLoader(tt.enabled) != nil; got != tt.want {
				t.Errorf("loader = %v, want %v", got, tt.want)
			}
		})
	}
}
