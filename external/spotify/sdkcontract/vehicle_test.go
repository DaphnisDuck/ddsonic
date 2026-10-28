//go:build sdkcontract

package sdkcontract

// ddsonic: the tracked local replacement of go-librespot (third_party/
// go-librespot) and its binding. The base is the upstream module, which step 4
// downloads and Go authenticates against the provenance's base hashes. Here the
// patches the provenance lists are applied to that base, in order, and the
// result must be the tracked tree byte for byte; both must hash to the
// provenance's active-h1, and each patch file to its recorded SHA-256. So the
// tree the contract tests run against is the tree the build uses, and it is
// the base plus exactly those patches.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

const provenancePath = "third_party/go-librespot.provenance"

type patchRecord struct {
	path   string // relative to the repository
	sha256 string // hex
}

type provenance struct {
	module, baseVersion string
	baseH1, baseGoModH1 string
	activeH1            string
	patches             []patchRecord
}

// readProvenance parses the provenance file: "key value" lines, and "#"
// comments. Every key is required once, except patch, which repeats in the
// order the patches apply.
func readProvenance(repo string) (*provenance, error) {
	data, err := os.ReadFile(filepath.Join(repo, provenancePath))
	if err != nil {
		return nil, err
	}
	p := &provenance{}
	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		key := fields[0]
		if key != "patch" && seen[key] {
			return nil, fmt.Errorf("%s: %s appears twice", provenancePath, key)
		}
		seen[key] = true
		switch {
		case key == "patch" && len(fields) == 3 && strings.HasPrefix(fields[2], "sha256:"):
			p.patches = append(p.patches, patchRecord{fields[1], strings.TrimPrefix(fields[2], "sha256:")})
		case len(fields) != 2:
			return nil, fmt.Errorf("%s: malformed line %q", provenancePath, line)
		case key == "module":
			p.module = fields[1]
		case key == "base-version":
			p.baseVersion = fields[1]
		case key == "base-h1":
			p.baseH1 = fields[1]
		case key == "base-gomod-h1":
			p.baseGoModH1 = fields[1]
		case key == "active-h1":
			p.activeH1 = fields[1]
		default:
			return nil, fmt.Errorf("%s: unknown key %q", provenancePath, key)
		}
	}
	switch {
	case p.module != sdkModule || p.baseVersion != sdkPin:
		return nil, fmt.Errorf("%s names %s@%s, the gate is for %s@%s", provenancePath, p.module, p.baseVersion, sdkModule, sdkPin)
	case p.baseH1 == "" || p.baseGoModH1 == "" || p.activeH1 == "":
		return nil, fmt.Errorf("%s lacks a base or active hash", provenancePath)
	}
	return p, sc.Err()
}

// hash1 is the module dirhash Go records in go.sum (golang.org/x/mod's
// dirhash.Hash1): the SHA-256 of the sorted lines "<sha256 of file>  <prefix>/<path>".
func hash1(root, prefix string) (string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			files = append(files, prefix+"/"+filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, name := range files {
		if strings.Contains(name, "\n") {
			return "", errors.New("a file name contains a newline")
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, prefix+"/"))))
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%x  %s\n", sum, name)
	}
	return "h1:" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// verifyVehicle rebuilds the active tree from the authenticated base and the
// recorded patches, and checks it against the tracked tree and the
// provenance. It returns the tracked tree's directory.
func (r *runner) verifyVehicle(p *provenance, base string) (string, error) {
	prefix := sdkModule + "@" + sdkPin
	active := filepath.Join(r.repo, filepath.FromSlash(replaceTarget))
	if r.activeOverride != "" {
		active = r.activeOverride
	}
	if got, err := hash1(base, prefix); err != nil || got != p.baseH1 {
		return "", &stepError{6, fmt.Sprintf("the extracted base hashes to %s, the provenance records %s (%v)", got, p.baseH1, err), ""}
	}
	rebuilt := filepath.Join(r.dir, "rebuilt")
	if err := copyTree(base, rebuilt); err != nil {
		return "", &stepError{6, "copy the base: " + err.Error(), ""}
	}
	for _, patch := range p.patches {
		file := filepath.Join(r.repo, filepath.FromSlash(patch.path))
		data, err := os.ReadFile(file)
		if err != nil {
			return "", &stepError{6, err.Error(), ""}
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != patch.sha256 {
			return "", &stepError{6, fmt.Sprintf("%s hashes to %x, the provenance records %s", patch.path, sum, patch.sha256), ""}
		}
		if err := r.gitApply(rebuilt, file, false); err != nil {
			return "", err
		}
	}
	if diff, err := compareTrees(active, rebuilt); err != nil || diff != "" {
		return "", &stepError{6, fmt.Sprintf("the base with the recorded patches is not the tracked tree: %s (%v)", diff, err), ""}
	}
	for what, dir := range map[string]string{"the tracked tree": active, "the rebuilt tree": rebuilt} {
		if got, err := hash1(dir, prefix); err != nil || got != p.activeH1 {
			return "", &stepError{6, fmt.Sprintf("%s hashes to %s, the provenance records active-h1 %s (%v)", what, got, p.activeH1, err), ""}
		}
	}
	// The archive alone suffices too: the tracked tree with the patches
	// reversed must hash to the upstream base-h1, with no module cache, so an
	// offline rebuild from the source archive can check its own SDK bytes.
	if err := r.reconstructBase(p, active, filepath.Join(r.dir, "reversed")); err != nil {
		return "", err
	}
	return active, nil
}

// reconstructBase copies the tracked tree to dir, reverses the recorded
// patches there in reverse order, and requires the result to hash to the
// provenance's base-h1: the upstream module, recovered from archived bytes.
func (r *runner) reconstructBase(p *provenance, active, dir string) error {
	if err := copyTree(active, dir); err != nil {
		return &stepError{6, "copy the tracked tree: " + err.Error(), ""}
	}
	for i := len(p.patches) - 1; i >= 0; i-- {
		if err := r.gitApply(dir, filepath.Join(r.repo, filepath.FromSlash(p.patches[i].path)), true); err != nil {
			return err
		}
	}
	got, err := hash1(dir, sdkModule+"@"+sdkPin)
	if err != nil || got != p.baseH1 {
		return &stepError{6, fmt.Sprintf("the tracked tree with its patches reversed hashes to %s, the upstream base-h1 is %s (%v)", got, p.baseH1, err), ""}
	}
	return nil
}

// gitApplyLimit bounds one patch application; a patch that has not applied
// by then is a setup failure of the run, never a result.
var gitApplyLimit = 2 * time.Minute

// gitApply applies (or, reverse, unapplies) one patch in dir, bounded like
// every tool the runner starts. A run that hits the deadline is reported as a
// setup failure of step 6, with what the tool wrote.
func (r *runner) gitApply(dir, patch string, reverse bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	what := "apply " + filepath.Base(patch) + " to the base"
	if reverse {
		args = append(args, "-R")
		what = "reverse " + filepath.Base(patch) + " on the tracked tree"
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitApplyLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(args, patch)...)
	cmd.Dir, cmd.Env = dir, r.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		return &stepError{6, "setup failure: " + what + ": killed at its deadline (" + gitApplyLimit.String() + ")", "stderr:\n" + stderr.String() + "\nstdout:\n" + stdout.String()}
	}
	return toolResult(6, what, ctx, err, stdout.Bytes(), stderr.Bytes())
}

// downloadBase extracts the upstream module offline into the private module
// cache, through a scratch module whose go.sum holds only the provenance's
// base hashes, so that Go itself authenticates the archive. It returns the
// extracted directory. The runner's environment must be set up.
func (r *runner) downloadBase(prov *provenance) (string, error) {
	baseMod := filepath.Join(r.dir, "base")
	if err := os.MkdirAll(baseMod, 0o755); err != nil {
		return "", &stepError{4, err.Error(), ""}
	}
	if err := errors.Join(
		os.WriteFile(filepath.Join(baseMod, "go.mod"), []byte(fmt.Sprintf("module ddsonic.invalid/sdkbase\n\ngo 1.26\n\nrequire %s %s\n", sdkModule, sdkPin)), 0o644),
		os.WriteFile(filepath.Join(baseMod, "go.sum"), []byte(fmt.Sprintf("%s %s %s\n%s %s/go.mod %s\n", sdkModule, sdkPin, prov.baseH1, sdkModule, sdkPin, prov.baseGoModH1)), 0o644),
	); err != nil {
		return "", &stepError{4, err.Error(), ""}
	}
	out, err := r.tool(4, "go mod download", baseMod, 5*time.Minute, "mod", "download", "-json", sdkModule+"@"+sdkPin)
	if err != nil {
		return "", err
	}
	var downloaded moduleInfo
	if err := json.Unmarshal(out, &downloaded); err != nil || downloaded.Version != sdkPin || downloaded.Dir == "" {
		return "", &stepError{4, fmt.Sprintf("go mod download reported no directory for %s@%s (%v)", sdkModule, sdkPin, err), string(out)}
	}
	return downloaded.Dir, nil
}

// A tracked tree that is not the base with the recorded patches fails before
// anything is staged or compiled.
func TestSDKContractTrackedTreeMismatchFails(t *testing.T) {
	r := newRunner(t)
	tree := filepath.Join(t.TempDir(), "go-librespot")
	if err := copyTree(filepath.Join(r.repo, filepath.FromSlash(replaceTarget)), tree); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(tree, "player", "player.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n// changed\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r.activeOverride = tree
	wantStep(t, r, 6, "is not the tracked tree")
}

// hash1 reproduces the go.sum hash Go itself recorded for the base. The base
// is extracted here, privately and authenticated, from the same offline
// module source as the gate's: no extracted copy outside it is needed.
func TestHash1MatchesGoSum(t *testing.T) {
	r := newRunner(t)
	if err := r.environment(); err != nil {
		t.Fatal(err)
	}
	p, err := readProvenance(r.repo)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := r.downloadBase(p)
	if err != nil {
		t.Fatal(err)
	}
	if !within(filepath.Join(r.dir, "gomodcache"), dir) {
		t.Fatalf("the base was not extracted into this test's private cache: %s", dir)
	}
	if got, err := hash1(dir, sdkModule+"@"+sdkPin); err != nil || got != p.baseH1 {
		t.Fatalf("hash1 of the extracted base is %s (%v), the provenance's base-h1 is %s", got, err, p.baseH1)
	}
}

// A patch application that does not finish by its deadline is a setup failure
// of step 6, never a result: here the deadline has passed before it starts.
func TestGitApplyDeadlineIsSetupFailure(t *testing.T) {
	r := newRunner(t)
	if err := r.environment(); err != nil {
		t.Fatal(err)
	}
	saved := gitApplyLimit
	gitApplyLimit = time.Nanosecond
	defer func() { gitApplyLimit = saved }()
	err := r.gitApply(t.TempDir(), filepath.Join(r.repo, "patches", "go-librespot", "0001-player-guard-cdn-quarantine-map.patch"), false)
	var se *stepError
	if !errors.As(err, &se) || se.step != 6 || !strings.Contains(err.Error(), "setup failure") {
		t.Fatalf("a patch application past its deadline gave %v; want a step-6 setup failure", err)
	}
}
