package appmeta

import (
	"testing"

	"github.com/bjarneo/cliamp/internal/appdir"
)

func TestDefaults(t *testing.T) {
	if got := ClientName(); got != "ddsonic" {
		t.Fatalf("ClientName() = %q, want %q", got, "ddsonic")
	}
	if got := DeviceName(); got != "ddsonic" {
		t.Fatalf("DeviceName() = %q, want %q", got, "ddsonic")
	}
}

func TestSetVersion(t *testing.T) {
	original := Version()
	defer SetVersion(original)

	SetVersion("1.2.3")
	if got := Version(); got != "1.2.3" {
		t.Fatalf("Version() = %q, want %q", got, "1.2.3")
	}
}

func TestSetVersionEmpty(t *testing.T) {
	original := Version()
	defer SetVersion(original)

	SetVersion("test")
	SetVersion("") // should be a no-op
	if got := Version(); got != "test" {
		t.Fatalf("empty SetVersion should be no-op, got %q", got)
	}
}

// ddsonic: the one User-Agent every request uses names ddsonic, its version
// without the tag's "v", and the project, never cliamp.
func TestUserAgent(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	for _, tt := range []struct{ version, want string }{
		{"dev", "ddsonic/dev (https://github.com/DaphnisDuck/ddsonic)"},
		{"v1.0.0", "ddsonic/1.0.0 (https://github.com/DaphnisDuck/ddsonic)"},
		{"v1.0.0-rc.1", "ddsonic/1.0.0-rc.1 (https://github.com/DaphnisDuck/ddsonic)"},
	} {
		version = tt.version
		if got := UserAgent(); got != tt.want {
			t.Errorf("UserAgent() with version %q = %q, want %q", tt.version, got, tt.want)
		}
	}
}

// ddsonic: the name desktop media integrations show (MPRIS Identity) is the
// product's name, the same one the bus name is built from.
func TestDisplayNameIsTheProductName(t *testing.T) {
	if got := DisplayName(); got != appdir.Name || got != ClientName() {
		t.Errorf("DisplayName() = %q, want the product name %q", got, appdir.Name)
	}
}

func TestDesktopEntry(t *testing.T) {
	if got := DesktopEntry(); got != "ddsonic" {
		t.Fatalf("DesktopEntry() = %q, want %q", got, "ddsonic")
	}
}
