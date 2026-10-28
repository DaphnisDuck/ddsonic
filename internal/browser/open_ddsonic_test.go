package browser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ddsonic: under go test Open starts nothing, whatever is on PATH.
func TestOpenStartsNothingUnderTest(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "opened")
	for _, name := range []string{"xdg-open", "open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := Open("https://example.invalid/authorize"); !errors.Is(err, ErrUnderTest) {
		t.Errorf("Open() = %v under go test, want ErrUnderTest", err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("Open started a browser under go test")
	}
}
