package main

// ddsonic: when yt-dlp is missing, ddsonic says how to install it and starts
// without YouTube (#82). It never installs it: it runs no package manager,
// asks for no password and reads nothing from the terminal for it, whatever
// enabled YouTube (the built-in credentials, a client ID, cookies, or the
// default provider).

import (
	"fmt"
	"io"

	"github.com/bjarneo/cliamp/player"
)

// ytdlpReady reports whether yt-dlp is on PATH. When it is not, it writes to
// w how to install it.
func ytdlpReady(w io.Writer) bool {
	if player.YTDLPAvailable() {
		return true
	}
	fmt.Fprintf(w, "\nYouTube needs yt-dlp, which is not installed, so it is off for now.\n")
	fmt.Fprintf(w, "To use YouTube, install yt-dlp (for example: %s;\n", player.YtdlpInstallHint())
	fmt.Fprintf(w, "other ways: https://github.com/yt-dlp/yt-dlp#installation), then restart.\n\n")
	return false
}
