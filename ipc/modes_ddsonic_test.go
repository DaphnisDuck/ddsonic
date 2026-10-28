package ipc

import (
	"path/filepath"
	"testing"
)

func TestValidModeName(t *testing.T) {
	tests := []struct {
		op, name string
		want     bool
	}{
		{"shuffle", "", true},
		{"shuffle", "off", true},
		{"shuffle", "all", false},
		{"repeat", "", true},
		{"repeat", "all", true},
		{"repeat", "toggle", false},
		{"mono", "toggle", true},
		{"mono", "bogus", false},
		{"volume", "anything", true},
	}
	for _, tt := range tests {
		if got := ValidModeName(tt.op, tt.name); got != tt.want {
			t.Errorf("ValidModeName(%q, %q) = %v, want %v", tt.op, tt.name, got, tt.want)
		}
	}
}

func TestDefaultSocketPathIsDdsonic(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", dir)
	if got := DefaultSocketPath(); got != filepath.Join(dir, "ddsonic.sock") {
		t.Errorf("DefaultSocketPath = %q", got)
	}
}
