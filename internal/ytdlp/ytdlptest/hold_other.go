//go:build !unix

package ytdlptest

import (
	"errors"
	"time"
)

// InstallCopier skips Windows before it calls own. Anywhere else without
// unix the fixture cannot own the processes of its shell-script fake, so
// own refuses rather than let a test run without that ownership.
func (c *Copier) own() error {
	return errors.New("ytdlptest: the fake yt-dlp's processes cannot be owned on this system")
}

func (c *Copier) unhold() error                   { return nil }
func (c *Copier) join(time.Time) error            { return nil }
func (c *Copier) awaitRegistered(time.Time) error { return nil }
