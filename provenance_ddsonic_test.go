package main

// ddsonic: a release build says which commit it was built from, and the
// release script puts the same commit into the binary and into the
// archive's manifest. Release acceptance (acceptance.VerifyRelease) requires
// the expected commit, the manifest's and the binary's to be one.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bjarneo/cliamp/acceptance"
	"github.com/bjarneo/cliamp/internal/appdir"
)

func TestVersionNamesTheSourceCommitOfAReleaseBuild(t *testing.T) {
	savedVersion, savedSHA := version, sourceSHA
	t.Cleanup(func() { version, sourceSHA = savedVersion, savedSHA })

	version, sourceSHA = "v9.9.9-rc.1", ""
	if got := ddsonicApp().Version; got != "v9.9.9-rc.1" {
		t.Errorf("without an embedded commit the version reads %q", got)
	}
	version, sourceSHA = "v9.9.9-rc.1", strings.Repeat("a", 40)
	if got, want := ddsonicApp().Version, "v9.9.9-rc.1 (commit "+strings.Repeat("a", 40)+")"; got != want {
		t.Errorf("the version reads %q, want %q", got, want)
	}
	version, sourceSHA = "", ""
	if got := ddsonicApp().Version; got != "dev" {
		t.Errorf("a plain build's version reads %q, want dev", got)
	}
}

// The executable, built the way the release script builds it, prints what
// release acceptance parses.
func TestReleaseBuildReportsVersionAndCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the executable")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	const commit = "0123456789abcdef0123456789abcdef01234567"
	bin := filepath.Join(t.TempDir(), appdir.Name)
	build := exec.Command("go", "build", "-ldflags", "-X main.version=v9.9.9-rc.1 -X main.sourceSHA="+commit, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(out)), appdir.Name+" version v9.9.9-rc.1 (commit "+commit+")"; got != want {
		t.Errorf("--version = %q, want %q", got, want)
	}
}

// packaging/release.sh resolves its source to a commit once, archives that
// commit, embeds it, checks what the binary prints, and writes the manifest
// release acceptance requires.
func TestReleaseScriptRecordsProvenance(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("packaging", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, want := range []string{
		`commit=$(git -c safe.directory=/src -C /src rev-parse --verify "${3:-HEAD}^{commit}")`,
		`archive --format=tar "$commit"`,
		"-X main.sourceSHA=${commit}",
		`want="ddsonic version ${version} (commit ${commit})"`,
		`"$work/$bin/` + acceptance.ManifestName + `"`,
		`"name": "ddsonic"`,
		`"version": "${version}"`,
		`"source_sha": "${commit}"`,
		`"goos": "$(go env GOOS)"`,
		`"goarch": "$(go env GOARCH)"`,
		`"go_version": "$(go env GOVERSION)"`,
		`"binary_sha256": "$(sha256sum "$work/ddsonic" | cut -d' ' -f1)"`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("packaging/release.sh lacks %q", want)
		}
	}
	// The source name is resolved exactly once, before anything is copied,
	// and never used again: the archive, the binary and the manifest all
	// take the resolved ID.
	if n := strings.Count(script, "rev-parse"); n != 1 {
		t.Errorf("packaging/release.sh resolves a revision %d times, want once", n)
	}
	if strings.Contains(script, "treeish") || strings.Count(script, "${3") != 1 {
		t.Error("packaging/release.sh still uses the source's name after resolving it")
	}
	resolve, archive := strings.Index(script, "rev-parse"), strings.Index(script, "archive --format=tar")
	if resolve < 0 || archive < 0 || resolve > archive {
		t.Error("the commit is not resolved before the source is archived")
	}
	// The manifest is written before the archive is packed, and names the
	// binary that is packed with it.
	if m, p := strings.Index(script, acceptance.ManifestName), strings.Index(script, `pack "$bin.tar.gz"`); m < 0 || p < 0 || m > p {
		t.Error("the manifest is not written before the binary archive is packed")
	}
	if out, err := exec.Command("sh", "-n", filepath.Join("packaging", "release.sh")).CombinedOutput(); err != nil {
		t.Errorf("sh -n packaging/release.sh: %v: %s", err, out)
	}
}

// The script's own first stage, run as written, with a git that answers the
// way a moving branch would: the name resolves to commit A when the script
// starts, and would resolve to B at any later time. Everything the stage
// does with the source must use A.
func TestReleaseScriptArchivesTheCommitItResolved(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("packaging", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	// From the start of the script through the line that archives the source.
	end := strings.Index(script, "archive --format=tar")
	if end < 0 {
		t.Fatal("no archive step in packaging/release.sh")
	}
	end += strings.Index(script[end:], "\n") + 1
	stage := script[:end]

	dir := t.TempDir()
	const a, b = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// A stand-in git: the first rev-parse answers A and every later one B;
	// archive records what it was asked for and emits an empty archive.
	git := `#!/bin/sh
log="` + dir + `/git.log"
echo "$*" >> "$log"
case "$*" in
*rev-parse*)
	if [ -e "` + dir + `/resolved" ]; then echo ` + b + `; else : > "` + dir + `/resolved"; echo ` + a + `; fi ;;
*archive*)
	head -c 10240 /dev/zero ;;
esac
`
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(git), 0o700); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(dir, "stage.sh")
	// After the stage, show what the script holds as the commit, and what
	// the name would resolve to now.
	tail := "\necho \"COMMIT=$commit\"\necho \"NOW=$(git -C /src rev-parse --verify 'main^{commit}')\"\n"
	if err := os.WriteFile(stagePath, []byte(stage+tail), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", stagePath, "v9.9.9-rc.1", "0", "main")
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TMPDIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the script's first stage: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "COMMIT="+a) || !strings.Contains(string(out), "NOW="+b) {
		t.Fatalf("the stage holds another commit than the one first resolved, or the stand-in did not move:\n%s", out)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "git.log"))
	var archived string
	for line := range strings.SplitSeq(string(log), "\n") {
		if strings.Contains(line, "archive --format=tar") {
			archived = line
		}
	}
	if !strings.HasSuffix(archived, " "+a) {
		t.Errorf("the source was archived as %q, want the resolved commit %s (git was asked:\n%s)", archived, a, log)
	}
	if strings.Contains(archived, "main") || strings.Contains(archived, b) {
		t.Errorf("the archive step used the moving name or the later commit: %q", archived)
	}
}
