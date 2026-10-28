package ipc

// ddsonic: the messages a user sees name ddsonic, not cliamp.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAlreadyRunningNamesDdsonic(t *testing.T) {
	dir := shortTempDir(t)
	sock := filepath.Join(dir, "ddsonic.sock")
	if err := os.WriteFile(sock+".pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewServer(sock)
	if err == nil || !strings.Contains(err.Error(), "ddsonic is already running") || strings.Contains(err.Error(), "cliamp") {
		t.Fatalf("NewServer() error = %v, want it to say ddsonic is already running", err)
	}
}
