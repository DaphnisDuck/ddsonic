// Package ytdlp holds what every yt-dlp process ddsonic starts has in common.
package ytdlp

// Args returns args for a yt-dlp run that does what ddsonic asked and nothing
// else:
//
//   - --ignore-config: yt-dlp otherwise reads options from a yt-dlp.conf
//     beside its binary and in the directory ddsonic was started from, and
//     from the user's and the system's configuration. Those are written for
//     the user's own downloads; here they could change the format, the output
//     or the account a read uses, or run a command.
//   - --no-mark-watched: ddsonic reads YouTube and never writes to the
//     account's watch history. This is yt-dlp's default once no configuration
//     is read; it is stated so that it does not rest on that.
//
// Cookies, when ddsonic uses them, are passed by the caller
// (--cookies-from-browser, from cookies_from in config.toml). Proxy
// environment variables still apply: they are not configuration files.
func Args(args ...string) []string {
	return append([]string{"--ignore-config", "--no-mark-watched"}, args...)
}
