//go:build !unix

package memdiag

import "io/fs"

// Without SIGUSR1 there is no trigger: only the exit snapshot is taken.
func notifySnapshots(*recorder) (stop func(), done chan struct{}) { return nil, nil }

func ownedByMe(fs.FileInfo, string) error { return nil }
