//go:build windows

package fileutil

// syncDir is a no-op on Windows: directory handles cannot be fsynced there
// (Sync returns "Access is denied"), and NTFS does not require a directory
// sync for the preceding rename to be durable.
func syncDir(string) error { return nil }
