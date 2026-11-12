package main

// ddsonic: the opt-in memory diagnostics of a non-release investigation
// (#168, internal/memdiag). Unset, DDSONIC_MEMDIAG changes nothing.

import (
	"fmt"
	"os"

	"github.com/bjarneo/cliamp/external/spotify"
	"github.com/bjarneo/cliamp/internal/memdiag"
)

// startMemDiag turns diagnostics on when DDSONIC_MEMDIAG names a directory,
// and returns what turns them off at exit. A directory that cannot be used
// stops the program: a diagnostic run without its output is worse than none.
func startMemDiag(getenv func(string) string) (stop func(), err error) {
	dir := getenv(memdiag.Env)
	if dir == "" {
		return func() {}, nil
	}
	stopRec, err := memdiag.Start(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", memdiag.Env, err)
	}
	spotify.EnableMemDiag()
	fmt.Fprintf(os.Stderr, "memory diagnostics on: %s (SIGUSR1 takes a snapshot)\n", memdiag.Dir())
	return func() {
		spotify.DisableMemDiag()
		stopRec()
	}, nil
}
