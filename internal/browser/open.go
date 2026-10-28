package browser

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
)

// ErrUnderTest is what Open returns under go test.
var ErrUnderTest = errors.New("browser: not opened under go test") // ddsonic

// Open tries to open a URL in the user's default browser.
func Open(u string) error {
	// ddsonic: no test opens a real browser, whichever package it is in and
	// whatever stand-in it forgot.
	if testing.Testing() {
		return ErrUnderTest
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "linux":
		return exec.Command("xdg-open", u).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
	default:
		return fmt.Errorf("unsupported platform")
	}
}
