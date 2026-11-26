package luaplugin

// ddsonic: the exec allowlist as config.Load reads it from a file (#75).

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/config"
)

// TestAllowedBinariesFromConfigFile: the allowlist is exactly the defaults
// plus the names configured in the top-level [plugins] table's
// allowed_binaries string. A comment, another table, or a commented-out
// line adds nothing.
func TestAllowedBinariesFromConfigFile(t *testing.T) {
	defaults := slices.Clone(defaultAllowedBinaries)
	with := func(names ...string) []string { return append(slices.Clone(defaults), names...) }
	for _, tt := range []struct {
		name string
		toml string
		want []string
	}{
		{"no [plugins]", "volume = 0\n", defaults},
		{"one name", "[plugins]\nallowed_binaries = \"ffprobe\"\n", with("ffprobe")},
		{"names and a comment", "[plugins]\nallowed_binaries = \"ffprobe, curl\"   # merged with defaults\n", with("ffprobe", "curl")},
		{"a name in the comment", "[plugins]\nallowed_binaries = \"ffprobe\" # , sh\n", with("ffprobe")},
		{"a comment after the header", "[plugins] # c\nallowed_binaries = \"curl\"\n", with("curl")},
		{"commented out", "[plugins]\n# allowed_binaries = \"sh\"\n", defaults},
		{"under a plugin's table", "[plugins.x]\nallowed_binaries = \"sh\"\n", defaults},
		{"an empty plugin name", "[plugins.\"\"]\nallowed_binaries = \"sh\"\n", defaults},
		{"a quoted name with a dot", "[\"plugins.\"]\nallowed_binaries = \"sh\"\n", defaults},
		{"a quoted plugin name with a dot", "[plugins.\"x.y\"]\nallowed_binaries = \"sh\"\n", defaults},
		{"a quoted [plugins]", "[\"plugins\"]\nallowed_binaries = \"curl\"\n", with("curl")},
		{"a default again", "[plugins]\nallowed_binaries = \"ffmpeg, ffmpeg\"\n", defaults},
		{"empty", "[plugins]\nallowed_binaries = \"\"\n", defaults},
		{"dotted key: the same table", "plugins.allowed_binaries = \"curl\"\n", with("curl")},
		{"a string over several lines", "[plugins]\nallowed_binaries = \"\"\"ffprobe,\ncurl\"\"\"\n", with("ffprobe", "curl")},
		{"an escaped comma separates", "[plugins]\nallowed_binaries = \"a\\u002Cb\"\n", with("a", "b")},
		// An array is not the documented form; its text is split on commas
		// as a one-line array's always was. A name is allowed only where the
		// user wrote it between commas: "y, sh, z" allows sh.
		{"an array", "[plugins]\nallowed_binaries = [\"sh\",\n \"curl\"]\n", with(`["sh"`, `"curl"]`)},
		{"an array element with commas", "[plugins]\nallowed_binaries = [\"x\",\n \"y, sh, z\"]\n", with(`["x"`, `"y`, "sh", `z"]`)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("CLIAMP_CONFIG_DIR", dir)
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tt.toml), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if got := resolveAllowedBinaries(cfg.Plugins); !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
