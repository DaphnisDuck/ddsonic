package acceptance

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// Run modes. Only a release run is evidence for a release.
const (
	ModeRelease     = "release"
	ModeDevelopment = "development"
)

// Identity says exactly what a run tested and with what.
type Identity struct {
	Mode string `json:"mode"`
	// ExpectedVersion is the version the candidate must report; empty in a
	// development run.
	ExpectedVersion string `json:"expected_version,omitempty"`
	ReportedVersion string `json:"reported_version"`
	// SourceSHA is the commit the candidate was released from, as the run
	// was told. In a release run the archive's manifest and the binary
	// itself each name a commit too (ManifestSourceSHA, BinarySourceSHA),
	// and the run starts only when all three are the same. SourceCheck says
	// what was compared.
	SourceSHA         string `json:"source_sha"`
	ManifestSourceSHA string `json:"manifest_source_sha,omitempty"`
	BinarySourceSHA   string `json:"binary_source_sha,omitempty"`
	SourceCheck       string `json:"source_check"`
	// ReleaseGoVersion is the Go the candidate was built with, as its
	// manifest and the binary's own build information both say. It need not
	// be the Go that runs the harness.
	ReleaseGoVersion string `json:"release_go_version,omitempty"`
	ArchiveName      string `json:"archive_name,omitempty"`
	ArchiveSHA256    string `json:"archive_sha256,omitempty"`
	BinarySHA256     string `json:"binary_sha256"`
	// BinaryPath is where the binary under test ran from. Not serialized:
	// it is a runtime path, of no use to a release record.
	BinaryPath string `json:"-"`
	// HarnessRevision is the commit of the checkout that ran the cases, with
	// "+dirty" when it had uncommitted changes. CatalogSHA256 is the case
	// catalog's checksum.
	HarnessRevision string            `json:"harness_revision"`
	CatalogSHA256   string            `json:"catalog_sha256"`
	Environment     map[string]string `json:"environment"`
	Tools           map[string]string `json:"tools"`
}

// Target is the candidate the gate judges inapplicability against.
func (id Identity) Target() Target {
	version := id.ExpectedVersion
	if version == "" {
		version = id.ReportedVersion
	}
	return Target{Version: version, Platform: runtime.GOOS + "/" + runtime.GOARCH}
}

var (
	sha40     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	goVersion = regexp.MustCompile(`^go[0-9]+\.[0-9]+`)
)

// ManifestName is the release manifest inside the binary archive, beside
// the binary. packaging/release.sh writes it.
const ManifestName = "RELEASE-MANIFEST.json"

// Manifest is the release manifest: what the archive says it is. Every field
// here is required and checked; the file may hold more.
type Manifest struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	SourceSHA    string `json:"source_sha"`
	GOOS         string `json:"goos"`
	GOARCH       string `json:"goarch"`
	GoVersion    string `json:"go_version"`
	BinarySHA256 string `json:"binary_sha256"`
}

// ReleaseInput is what a release run is given. All of it comes from the
// person running it; the harness downloads nothing and changes no release.
type ReleaseInput struct {
	Archive   string // the draft release's binary archive, already downloaded
	Sums      string // the release's SHA256SUMS file
	Version   string // the version the candidate must report, as tagged
	SourceSHA string // the commit the candidate is released from
}

// VerifyRelease identifies a release candidate and extracts its binary into
// extractTo. It computes the archive's and the binary's checksums itself,
// and it fails, so that the run does not start, on anything missing,
// unreadable or not in exact agreement:
//
//   - the archive is the one SHA256SUMS lists, and the one named for this
//     version and platform;
//   - the archive holds a manifest that names this version, this source
//     commit and the checksum of the binary beside it;
//   - the binary itself reports this version and this source commit.
//
// So the expected commit, the manifest's and the binary's own are one and
// the same. A local tag of that version is one more witness: it must not
// contradict them, and its absence proves nothing either way. Nothing is
// ever built here: a release run tests the archive's binary or nothing.
func VerifyRelease(in ReleaseInput, repo, extractTo string) (Identity, error) {
	id := Identity{Mode: ModeRelease, ExpectedVersion: in.Version, SourceSHA: in.SourceSHA}
	if in.Archive == "" || in.Sums == "" || in.Version == "" || in.SourceSHA == "" {
		return id, errors.New("a release run needs the archive, its SHA256SUMS, the expected version and the source commit")
	}
	if in.Version == "dev" || !strings.HasPrefix(in.Version, "v") {
		return id, fmt.Errorf("expected version %q is not a release version (vX.Y.Z…)", in.Version)
	}
	if !sha40.MatchString(in.SourceSHA) {
		return id, fmt.Errorf("source commit %q is not a full 40-character SHA", in.SourceSHA)
	}

	stem := fmt.Sprintf("%s-%s-%s-%s", appdir.Name, strings.TrimPrefix(in.Version, "v"), runtime.GOOS, runtime.GOARCH)
	id.ArchiveName = filepath.Base(in.Archive)
	if id.ArchiveName != stem+".tar.gz" {
		return id, fmt.Errorf("archive is %s; the archive of %s for this platform is %s.tar.gz", id.ArchiveName, in.Version, stem)
	}
	sum, err := FileSHA256(in.Archive)
	if err != nil {
		return id, err
	}
	id.ArchiveSHA256 = sum
	listed, err := listedSum(in.Sums, id.ArchiveName)
	if err != nil {
		return id, err
	}
	if listed != sum {
		return id, fmt.Errorf("archive checksum %s does not match SHA256SUMS (%s): this is not the release's archive", sum, listed)
	}

	binaryMember, manifestMember := stem+"/"+appdir.Name, stem+"/"+ManifestName
	extracted, err := extractMembers(in.Archive, extractTo, binaryMember, manifestMember)
	if err != nil {
		return id, err
	}
	id.BinaryPath = extracted[binaryMember]
	if id.BinarySHA256, err = FileSHA256(id.BinaryPath); err != nil {
		return id, err
	}

	// What the archive says it is.
	manifest, err := readManifest(extracted[manifestMember])
	if err != nil {
		return id, err
	}
	id.ManifestSourceSHA = manifest.SourceSHA
	// The build's identity: the product, and the platform this archive is
	// named for, which is the one it is being run on.
	switch {
	case manifest.Name != appdir.Name:
		return id, fmt.Errorf("the archive's manifest is for %q, and this is the archive of %s", manifest.Name, appdir.Name)
	case manifest.GOOS != runtime.GOOS || manifest.GOARCH != runtime.GOARCH:
		return id, fmt.Errorf("the archive's manifest is for %s/%s, and the archive %s is for %s/%s", manifest.GOOS, manifest.GOARCH, id.ArchiveName, runtime.GOOS, runtime.GOARCH)
	}
	// And the executable's own account of how it was built must agree.
	built, err := buildinfo.ReadFile(id.BinaryPath)
	if err != nil {
		return id, fmt.Errorf("the archive's binary carries no readable build information: %w", err)
	}
	settings := map[string]string{}
	for _, setting := range built.Settings {
		settings[setting.Key] = setting.Value
	}
	switch {
	case built.GoVersion != manifest.GoVersion:
		return id, fmt.Errorf("the archive's manifest says the binary was built with %s, and the binary says %s", manifest.GoVersion, built.GoVersion)
	case settings["GOOS"] != manifest.GOOS || settings["GOARCH"] != manifest.GOARCH:
		return id, fmt.Errorf("the archive's manifest is for %s/%s, and the binary was built for %s/%s", manifest.GOOS, manifest.GOARCH, settings["GOOS"], settings["GOARCH"])
	}
	id.ReleaseGoVersion = manifest.GoVersion
	switch {
	case manifest.Version != in.Version:
		return id, fmt.Errorf("the archive's manifest is for version %q, want %q", manifest.Version, in.Version)
	case manifest.SourceSHA != in.SourceSHA:
		return id, fmt.Errorf("the archive's manifest names source commit %s, and the expected release commit is %s", manifest.SourceSHA, in.SourceSHA)
	case manifest.BinarySHA256 != id.BinarySHA256:
		return id, fmt.Errorf("the archive's manifest gives the binary's checksum as %s, and the binary in the archive has %s", manifest.BinarySHA256, id.BinarySHA256)
	}

	// What the binary says it is, independently of the manifest.
	if id.ReportedVersion, id.BinarySourceSHA, err = reportedIdentity(id.BinaryPath); err != nil {
		return id, err
	}
	switch {
	case id.ReportedVersion != in.Version:
		return id, fmt.Errorf("the archive's binary reports version %q, want %q", id.ReportedVersion, in.Version)
	case id.BinarySourceSHA == "":
		return id, errors.New("the archive's binary does not report the source commit it was built from")
	case id.BinarySourceSHA != in.SourceSHA:
		return id, fmt.Errorf("the archive's binary reports source commit %s, and the expected release commit is %s", id.BinarySourceSHA, in.SourceSHA)
	}

	tag, err := checkTag(repo, in.Version, in.SourceSHA)
	if err != nil {
		return id, err
	}
	id.SourceCheck = "the expected commit, the archive's manifest and the binary itself agree; " + tag
	return id, nil
}

// readManifest reads and checks the release manifest's required fields.
func readManifest(path string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("release manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("release manifest %s is unreadable: %w", ManifestName, err)
	}
	for _, f := range []struct{ name, value string }{
		{"name", m.Name}, {"version", m.Version}, {"source_sha", m.SourceSHA},
		{"goos", m.GOOS}, {"goarch", m.GOARCH}, {"go_version", m.GoVersion}, {"binary_sha256", m.BinarySHA256},
	} {
		if strings.TrimSpace(f.value) == "" {
			return m, fmt.Errorf("release manifest %s has no %s", ManifestName, f.name)
		}
	}
	if !sha40.MatchString(m.SourceSHA) {
		return m, fmt.Errorf("release manifest %s: source_sha %q is not a full 40-character SHA", ManifestName, m.SourceSHA)
	}
	if !goVersion.MatchString(m.GoVersion) {
		return m, fmt.Errorf("release manifest %s: go_version %q is not a Go version (such as go1.27.1)", ManifestName, m.GoVersion)
	}
	return m, nil
}

// DevelopmentIdentity identifies a locally built binary. Such a run exercises
// the harness and the cases; it is never release evidence, and the gate says so.
func DevelopmentIdentity(binary, repo string) (Identity, error) {
	id := Identity{Mode: ModeDevelopment, BinaryPath: binary, SourceCheck: "a local build of the checkout: not a release artifact"}
	var err error
	if id.BinarySHA256, err = FileSHA256(binary); err != nil {
		return id, err
	}
	if id.ReportedVersion, id.BinarySourceSHA, err = reportedIdentity(binary); err != nil {
		return id, err
	}
	id.SourceSHA, _ = git(repo, "rev-parse", "HEAD")
	return id, nil
}

// Describe fills in what ran the cases: the checkout, the catalog and the
// machine. Tools that are missing are recorded as such, not left out.
func (id *Identity) Describe(repo string) error {
	head, err := git(repo, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("harness revision: %w", err)
	}
	if status, _ := git(repo, "status", "--porcelain"); status != "" {
		head += "+dirty"
	}
	id.HarnessRevision = head
	if id.CatalogSHA256, err = FileSHA256(filepath.Join(repo, CatalogPath)); err != nil {
		return err
	}
	id.Environment = map[string]string{
		"os":       runtime.GOOS + "/" + runtime.GOARCH,
		"kernel":   toolLine("uname", "-sr"),
		"terminal": os.Getenv("TERM"),
	}
	id.Tools = map[string]string{
		"go":     toolLine("go", "version"),
		"tmux":   toolLine("tmux", "-V"),
		"ffmpeg": toolLine("ffmpeg", "-version"),
		// The host's yt-dlp, for the record: deterministic cases never run it.
		"yt-dlp (host)": toolLine("yt-dlp", "--version"),
	}
	return nil
}

// FileSHA256 returns a file's SHA-256 in hex.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("checksum %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// listedSum returns the checksum a sha256sum file lists for name.
func listedSum(sums, name string) (string, error) {
	f, err := os.Open(sums)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s does not list %s", sums, name)
}

// extractMembers extracts exactly the named regular files from a .tar.gz
// archive into dir and returns where each went. Every one of them must be
// there. Nothing else is extracted, and each file is created by its base
// name, so no member can write outside dir.
func extractMembers(archive, dir string, members ...string) (map[string]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", archive, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	extracted := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", archive, err)
		}
		if hdr.Typeflag != tar.TypeReg || !slices.Contains(members, hdr.Name) {
			continue
		}
		if _, twice := extracted[hdr.Name]; twice {
			return nil, fmt.Errorf("%s holds %s more than once", archive, hdr.Name)
		}
		out := filepath.Join(dir, filepath.Base(hdr.Name))
		w, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(w, tr); err != nil {
			w.Close()
			return nil, fmt.Errorf("extract %s: %w", hdr.Name, err)
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		extracted[hdr.Name] = out
	}
	for _, member := range members {
		if extracted[member] == "" {
			return nil, fmt.Errorf("%s holds no %s", archive, member)
		}
	}
	return extracted, nil
}

// versionLine is what "--version" prints: the name, "version", the version,
// and, in a release build, the source commit it was built from.
var versionLine = regexp.MustCompile(`^(\S+) version (\S+)(?: \(commit ([0-9a-f]{40})\))?$`)

// reportedIdentity runs "<binary> --version" and returns the version and
// the source commit the binary itself reports; the commit is "" when the
// build embedded none.
func reportedIdentity(binary string) (version, commit string, err error) {
	out, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return "", "", fmt.Errorf("%s --version: %w", binary, err)
	}
	m := versionLine.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil || m[1] != appdir.Name {
		return "", "", fmt.Errorf("%s --version printed %q", binary, strings.TrimSpace(string(out)))
	}
	return m[2], m[3], nil
}

// checkTag looks for a local tag of the version, as one more witness. The
// commit must exist in this checkout; a tag that names another commit is a
// contradiction and an error; no tag is no evidence either way.
func checkTag(repo, version, sha string) (string, error) {
	if _, err := git(repo, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return "", fmt.Errorf("source commit %s is not in this checkout", sha)
	}
	tagged, err := git(repo, "rev-parse", "--verify", "--quiet", "refs/tags/"+version+"^{commit}")
	if err != nil || tagged == "" {
		return "this checkout has no tag " + version + " to corroborate it", nil
	}
	if tagged != sha {
		return "", fmt.Errorf("the local tag %s is commit %s, not the expected release commit %s", version, tagged, sha)
	}
	return "the local tag " + version + " points to it too", nil
}

func git(repo string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// toolLine is the first line a tool prints, or "not found".
func toolLine(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "not found"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}
