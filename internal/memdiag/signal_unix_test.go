//go:build unix

package memdiag

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// SIGUSR1 takes a snapshot while diagnostics are on.
func TestSignalTakesASnapshot(t *testing.T) {
	_, stop := start(t)
	run := Dir()
	if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		snaps := readLines(t, filepath.Join(run, "snapshots.jsonl"))
		if len(snaps) == 2 && snaps[1]["trigger"] == "signal" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no signal snapshot: %v", snaps)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
}
