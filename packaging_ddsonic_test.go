package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/internal/appmeta"
)

// TestDesktopInstall runs packaging/desktop.sh as the release build, the AUR
// recipe and `make install` do, and checks what a launcher then reads.
func TestDesktopInstall(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the launcher entry is installed on Linux only")
	}

	tests := []struct {
		name string
		exe  string // second argument; "" leaves it out
		want string
	}{
		// The package's binary is in /usr/bin, on every session's PATH.
		{"on PATH", "", `Exec=ddsonic`},
		// A desktop session's PATH often lacks ~/.local/bin, and a launcher
		// drops an entry whose program it can't find.
		{"absolute", "/home/duck/.local/bin/ddsonic", `Exec="/home/duck/.local/bin/ddsonic"`},
		// Two layers (packaging/desktop.sh): the quoted argument's own
		// backslashes, each doubled as the file's string values want.
		{"needs quoting", `/home/a b/$x/50%/ddsonic`, `Exec="/home/a b/\\$x/50%%/ddsonic"`},
		{"quotes and backslashes", "/home/q\"uo`te\\back/it's/ddsonic", "Exec=\"/home/q\\\\\"uo\\\\`te\\\\\\\\back/it's/ddsonic\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dest := t.TempDir()
			args := []string{"packaging/desktop.sh", dest}
			if tt.exe != "" {
				args = append(args, tt.exe)
			}
			if out, err := exec.Command("sh", args...).CombinedOutput(); err != nil {
				t.Fatalf("desktop.sh: %v\n%s", err, out)
			}

			entry, err := os.ReadFile(filepath.Join(dest, "applications", "ddsonic.desktop"))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(string(entry), "\n")
			if tt.exe != "" {
				// Read back as a launcher reads it, the line names the program.
				if got := desktopExecProgram(t, lines); got != tt.exe {
					t.Errorf("a launcher would run %q, want %q", got, tt.exe)
				}
			}
			if validate, err := exec.LookPath("desktop-file-validate"); err == nil {
				path := filepath.Join(dest, "applications", "ddsonic.desktop")
				if out, err := exec.Command(validate, path).CombinedOutput(); err != nil {
					t.Errorf("desktop-file-validate: %v\n%s", err, out)
				}
			}
			for _, want := range []string{tt.want, "Icon=ddsonic", "Terminal=true"} {
				found := false
				for _, line := range lines {
					found = found || line == want
				}
				if !found {
					t.Errorf("the entry has no line %q:\n%s", want, entry)
				}
			}

			for _, size := range []string{"16", "24", "32", "48", "64", "128", "256", "512"} {
				want, err := os.ReadFile(filepath.Join("assets", "branding", "icons", "ddsonic-"+size+".png"))
				if err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(filepath.Join(dest, "icons", "hicolor", size+"x"+size, "apps", "ddsonic.png"))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("the %s px icon differs from the supplied one", size)
				}
			}
		})
	}
}

// TestMakeNamesTheBinary asks make what `make install` would run. Go names a
// plain build after the module path, cliamp's, so the Makefile has to name
// the binary itself.
func TestMakeNamesTheBinary(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}

	cmd := exec.Command("make", "-n", "install")
	// Under `make test`, the outer make's variables and flags would leak in.
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "BINARY", "HOME", "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "MAKEOVERRIDES":
		default:
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "HOME=/home/duck")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("make -n install: %v\n%s", err, out)
	}

	var built, installed bool
	for _, line := range strings.Split(string(out), "\n") {
		built = built || strings.HasPrefix(line, "go build ") && strings.HasSuffix(line, " -o ddsonic .")
		installed = installed || line == "install -m 755 ddsonic /home/duck/.local/bin/ddsonic"
	}
	if !built {
		t.Errorf("make does not build ./ddsonic:\n%s", out)
	}
	if !installed {
		t.Errorf("make does not install ~/.local/bin/ddsonic:\n%s", out)
	}
}

// TestBrandingMatchesItsManifest holds assets/branding to the package as it
// was delivered: every file is the one manifest.json lists, and nothing has
// been added or removed. The artwork is the owner's and is never edited here.
func TestBrandingMatchesItsManifest(t *testing.T) {
	dir := filepath.Join("assets", "branding")
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Files []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) == 0 {
		t.Fatal("the manifest lists no files")
	}

	listed := map[string]bool{"manifest.json": true}
	for _, f := range manifest.Files {
		listed[f.Path] = true
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Errorf("%s: %v", f.Path, err)
			continue
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s differs from the delivered file", f.Path)
		}
	}
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if !listed[filepath.ToSlash(rel)] {
			t.Errorf("%s is not in the manifest", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The MPRIS DesktopEntry names a launcher entry that is really installed.
func TestMPRISDesktopEntryIsTheInstalledOne(t *testing.T) {
	entry := filepath.Join("packaging", "linux", appmeta.DesktopEntry()+".desktop")
	if _, err := os.Stat(entry); err != nil {
		t.Errorf("appmeta.DesktopEntry() names no launcher entry: %v", err)
	}
}

// desktopExecProgram decodes the entry's Exec line as the Desktop Entry
// specification says a launcher does: first the escapes of every string value
// (\\ is a backslash; an unknown escape is an error), then the one quoted
// argument (a backslash before \, ", ` or $), then %% for a percent sign.
func desktopExecProgram(t *testing.T, lines []string) string {
	t.Helper()
	var value string
	for _, line := range lines {
		if v, ok := strings.CutPrefix(line, "Exec="); ok {
			value = v
		}
	}

	var str strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			str.WriteByte(value[i])
			continue
		}
		i++
		if i == len(value) || value[i] != '\\' {
			t.Fatalf("Exec has an escape the file format does not define: %s", value)
		}
		str.WriteByte('\\')
	}

	quoted, ok := strings.CutPrefix(str.String(), `"`)
	quoted, ok2 := strings.CutSuffix(quoted, `"`)
	if !ok || !ok2 {
		t.Fatalf("Exec is not one quoted argument: %s", value)
	}
	var arg strings.Builder
	for i := 0; i < len(quoted); i++ {
		switch c := quoted[i]; {
		case c == '\\':
			i++
			if i == len(quoted) || !strings.ContainsRune("\\\"`$", rune(quoted[i])) {
				t.Fatalf("Exec's argument has a stray backslash: %s", value)
			}
			arg.WriteByte(quoted[i])
		case strings.ContainsRune("\"`$", rune(c)):
			t.Fatalf("Exec's argument has an unescaped %c: %s", c, value)
		default:
			arg.WriteByte(c)
		}
	}
	program := arg.String()
	if strings.Contains(strings.ReplaceAll(program, "%%", ""), "%") {
		t.Fatalf("Exec has a field code or a lone %%: %s", value)
	}
	return strings.ReplaceAll(program, "%%", "%")
}

// TestReadmeImagesShipWithIt holds the README to pictures the release build
// can ship beside it: packaging/release.sh copies every <img src> the README
// names into the archive, and the AUR recipe installs them from there.
func TestReadmeImagesShipWithIt(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`!\[[^\]]*\]\(`).Match(readme) {
		t.Error("the README has a Markdown image; the release build copies <img src> ones only")
	}
	images := regexp.MustCompile(`<img src="([^"]*)"`).FindAllSubmatch(readme, -1)
	if len(images) == 0 {
		t.Fatal("the README shows no image")
	}
	for _, m := range images {
		src := string(m[1])
		if strings.Contains(src, ":") || filepath.IsAbs(src) || strings.Contains(src, "..") {
			t.Errorf("%s is not a path inside the repository", src)
			continue
		}
		if info, err := os.Stat(filepath.FromSlash(src)); err != nil || info.Size() == 0 {
			t.Errorf("the README shows %s, which is missing or empty", src)
		}
		// The recipe copies these two folders of the archive, whole.
		if top, _, _ := strings.Cut(src, "/"); top != "assets" && top != "docs" {
			t.Errorf("%s is outside assets/ and docs/, which packaging/aur/PKGBUILD installs", src)
		}
	}
}

// TestWorkflowScriptsParse reads every `run: |` script of the workflows with
// bash, as the runner does. A quote left open in one, by an apostrophe in a
// comment inside a quoted `sh -c` script, say, stops the job where it stands.
func TestWorkflowScriptsParse(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	files, err := filepath.Glob(".github/workflows/*.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflow found: %v", err)
	}
	runKey := regexp.MustCompile(`^\s*(?:- )?run: \|\s*$`)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		scripts := 0
		for i := 0; i < len(lines); i++ {
			if !runKey.MatchString(lines[i]) {
				continue
			}
			key := len(lines[i]) - len(strings.TrimLeft(lines[i], " -"))
			start := i + 1
			var script []string
			for i+1 < len(lines) {
				next := lines[i+1]
				if strings.TrimSpace(next) != "" && len(next)-len(strings.TrimLeft(next, " ")) <= key {
					break
				}
				script = append(script, next)
				i++
			}
			scripts++
			cmd := exec.Command(bash, "-n")
			cmd.Stdin = strings.NewReader(strings.Join(script, "\n"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s: the script at line %d does not parse: %v\n%s", file, start, err, out)
			}
		}
		if scripts == 0 {
			t.Errorf("%s: no run script found", file)
		}
	}
}
