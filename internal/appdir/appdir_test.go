package appdir

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDir(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		want        func(tempDir string) string
		windowsOnly bool
	}{
		{
			name: "home config",
			env:  map[string]string{"CLIAMP_CONFIG_DIR": "", ConfigDirEnv: "", "XDG_CONFIG_HOME": "", "APPDATA": "", "HOME": "TEMPDIR"},
			want: func(tmp string) string { return filepath.Join(tmp, ".config", Name) },
		},
		{
			name: "xdg config",
			env:  map[string]string{"CLIAMP_CONFIG_DIR": "", ConfigDirEnv: "", "HOME": "", "APPDATA": "", "XDG_CONFIG_HOME": "TEMPDIR"},
			want: func(tmp string) string { return filepath.Join(tmp, Name) },
		},
		{
			name:        "appdata on windows when home missing",
			windowsOnly: true,
			env:         map[string]string{"CLIAMP_CONFIG_DIR": "", ConfigDirEnv: "", "XDG_CONFIG_HOME": "", "HOME": "", "APPDATA": "TEMPDIR"},
			want:        func(tmp string) string { return filepath.Join(tmp, Name) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.windowsOnly && runtime.GOOS != "windows" {
				t.Skip("Windows-specific fallback")
			}
			var tempDir string
			for k, v := range tt.env {
				if v == "TEMPDIR" {
					tempDir = t.TempDir()
					t.Setenv(k, tempDir)
				} else {
					t.Setenv(k, v)
				}
			}
			got, err := Dir()
			if err != nil {
				t.Fatalf("Dir() error: %v", err)
			}
			want := tt.want(tempDir)
			if got != want {
				t.Fatalf("Dir() = %q, want %q", got, want)
			}
		})
	}
}

func TestPluginDir(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv(ConfigDirEnv, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", t.TempDir())

	dir, err := PluginDir()
	if err != nil {
		t.Fatalf("PluginDir() error: %v", err)
	}

	if !strings.HasSuffix(dir, filepath.Join(Name, "plugins")) {
		t.Fatalf("PluginDir() = %q, expected to end with ddsonic/plugins", dir)
	}
}

func TestPluginDirIsSubdirOfDir(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv(ConfigDirEnv, "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	t.Setenv("HOME", t.TempDir())

	base, _ := Dir()
	plugin, _ := PluginDir()

	if !strings.HasPrefix(plugin, base) {
		t.Fatalf("PluginDir %q should be under Dir %q", plugin, base)
	}
}

// ddsonic: under go test DDSONIC_CONFIG_DIR is ignored, so upstream tests
// that isolate themselves with CLIAMP_CONFIG_DIR or a temporary HOME stay
// isolated even when a developer exports it.
func TestDdsonicOverrideIgnoredInTests(t *testing.T) {
	t.Setenv(ConfigDirEnv, "/ddsonic-cfg")
	t.Setenv("CLIAMP_CONFIG_DIR", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, _ := Dir(); got != filepath.Join(home, ".config", Name) {
		t.Fatalf("Dir() = %q, want the temporary HOME, not DDSONIC_CONFIG_DIR", got)
	}
	t.Setenv("CLIAMP_CONFIG_DIR", "/cliamp-cfg")
	if got, _ := Dir(); got != "/cliamp-cfg" {
		t.Fatalf("Dir() = %q, want CLIAMP_CONFIG_DIR", got)
	}
}

// ddsonic: the catalog lives in DataDir.
func TestLibraryDBPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, _ := LibraryDBPath(); got != filepath.Join(home, ".local", "share", Name, "library.db") {
		t.Errorf("LibraryDBPath() = %q, want it under DataDir", got)
	}
}
