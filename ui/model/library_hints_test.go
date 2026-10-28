package model

import (
	"strings"
	"testing"
)

// The hints for the providers ddsonic offers name ddsonic's folder and
// command, never cliamp's.
func TestProviderHintsNameDdsonic(t *testing.T) {
	for _, key := range []string{"local playlists", "local", "spotify", "youtube music", "ytmusic"} {
		hint, ok := providerEmptyStateHint[key]
		if !ok {
			t.Errorf("no hint for %q", key)
			continue
		}
		if strings.Contains(hint, "cliamp") {
			t.Errorf("the %q hint names cliamp: %s", key, hint)
		}
	}
	if got := providerEmptyStateHint["local"]; !strings.Contains(got, "~/.config/ddsonic/playlists/") {
		t.Errorf("the local hint does not name ddsonic's playlists folder: %s", got)
	}
	if got := providerEmptyStateHint["ytmusic"]; !strings.Contains(got, "`ddsonic setup`") {
		t.Errorf("the ytmusic hint does not name ddsonic setup: %s", got)
	}
}
