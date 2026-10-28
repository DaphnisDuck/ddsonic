package main

// ddsonic: the messages a user sees name ddsonic, not cliamp.

import (
	"fmt"
	"testing"

	"github.com/bjarneo/cliamp/ipc"
)

func TestNotRunningNamesDdsonic(t *testing.T) {
	// The socket path comes from the environment and may contain any name;
	// only the player's name in the message is under test.
	got := userIPCError(ipc.ErrNotRunning).Error()
	want := fmt.Sprintf("ddsonic is not running (no socket at %s)", ipc.DefaultSocketPath())
	if got != want {
		t.Fatalf("userIPCError = %q, want %q", got, want)
	}
}
