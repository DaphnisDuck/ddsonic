//go:build !unix

package ytdlptest

// alive is not asked where the fake does not run (InstallCopier skips).
func alive(int) bool { return false }
