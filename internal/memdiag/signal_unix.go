//go:build unix

package memdiag

import (
	"io/fs"
	"os"
	"os/signal"
	"syscall"
)

// notifySnapshots takes a snapshot on each SIGUSR1 until stop is called; done
// is closed when its goroutine has ended. One signal that arrives during a
// snapshot is kept; more are dropped.
func notifySnapshots(r *recorder) (stop func(), done chan struct{}) {
	sig := make(chan os.Signal, 1)
	quit := make(chan struct{})
	done = make(chan struct{})
	signal.Notify(sig, syscall.SIGUSR1)
	go func() {
		defer close(done)
		for {
			select {
			case <-sig:
				_ = r.snapshot("signal")
			case <-quit:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sig)
		close(quit)
	}, done
}

func ownedByMe(fi fs.FileInfo, dir string) error {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return &fs.PathError{Op: "memdiag", Path: dir, Err: fs.ErrPermission}
	}
	return nil
}
