//go:build !windows

package fileutil

import "os"

// syncDir fsyncs the directory so a preceding rename is durable across crashes.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
