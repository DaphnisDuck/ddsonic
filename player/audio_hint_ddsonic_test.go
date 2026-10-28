//go:build linux

package player

// ddsonic: the messages a user sees name ddsonic, not cliamp.

import (
	"strings"
	"testing"
)

func TestAudioOutputHintNamesDdsonic(t *testing.T) {
	hint := audioOutputHint()
	if !strings.Contains(hint, "ddsonic outputs through ALSA") || strings.Contains(hint, "cliamp") {
		t.Fatalf("audioOutputHint() = %q, want it to name ddsonic", hint)
	}
}
