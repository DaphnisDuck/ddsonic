package spotify

// ddsonic: a passing failure neither advises `spotify reset` (it deletes the
// stored sign-in) nor reads like an account problem (#61).

import (
	"errors"
	"strings"
	"testing"
)

func TestStoredTokenMessage(t *testing.T) {
	passing := storedTokenMessage("a-refresh-token")
	for _, bad := range []string{"reset", "no longer valid", "sign in again", "sign in to Spotify again"} {
		if strings.Contains(passing, bad) {
			t.Errorf("after a failed refresh the message %q says %q", passing, bad)
		}
	}
	for _, want := range []string{"kept", "try again"} {
		if !strings.Contains(passing, want) {
			t.Errorf("after a failed refresh the message %q lacks %q", passing, want)
		}
	}
	lost := storedTokenMessage("")
	if !strings.Contains(lost, "sign in to Spotify again") || strings.Contains(lost, "kept") {
		t.Errorf("with no refresh token the message %q should ask to sign in again", lost)
	}
}

func TestSilentSessionErrorWording(t *testing.T) {
	msg := silentSessionError(errors.New("dial tcp: i/o timeout")).Error()
	if strings.Contains(msg, "sign-in unavailable") || strings.Contains(msg, "reset") {
		t.Errorf("an outage reads like an account problem: %q", msg)
	}
	for _, want := range []string{"unavailable for now", "sign-in is kept", "try again later", "i/o timeout"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
}
