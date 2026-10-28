//go:build sdkcontract

package sdkcontract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	sdkModule = "github.com/devgianlu/go-librespot"
	// sdkPin is the go-librespot version the contract tests were written
	// against. A bump changes it here, in the same change (see the upgrade
	// procedure in docs/ddsonic/upstream.md).
	sdkPin = "v0.9.1"
	// childMarker is set in the environment of every tool the runner starts:
	// a runner that finds it set is being run by itself, and refuses.
	childMarker = "DDSONIC_SDK_CONTRACT_CHILD"
	// replaceTarget is where go.mod replaces the SDK: the tracked local
	// replacement, bound by its provenance file (vehicle_test.go).
	replaceTarget = "./third_party/go-librespot"
	// stageEnv set to "base" stages the authenticated base instead of the
	// active tree. That run is diagnostic, for red evidence, and never a pass.
	stageEnv = "DDSONIC_SDKCONTRACT_STAGE"
)

// contractTests are the tests that must be shown to have run and passed, in
// the SDK packages they are compiled into.
var contractTests = []string{
	"TestDDSonicContractDoneClosesBeforeCloseReturns",
	"TestDDSonicContractCloseWaitsForReconnect",
	"TestDDSonicContractCloseIsOnce",
	"TestDDSonicContractIdentityGetterWaitsForReconnect",
	"TestDDSonicContractKeyRequestFollowsContextBeforeCommit",
	"TestDDSonicContractDoneReleasesKeyRequest",
	"TestDDSonicContractRequestAfterCloseFails",
	"TestDDSonicContractFirstChunkIgnoresSetupContext",
	"TestDDSonicContractClientContextEndsFirstChunk",
	"TestDDSonicContractMP3HandleSurvivesDropAndGC",
	"TestDDSonicContractMP3CloseReleasesHandle",
	"TestDDSonicContractFirstKeyOnOpenConnection",
	"TestDDSonicContractFirstKeyOnServicedConnection",
	"TestDDSonicContractUnservicedPeerCloseReplacesFirstKey",
	"TestDDSonicContractServicedPeerCloseFirstKeySucceeds",
	"TestDDSonicContractInFlightKeyAcrossReconnect",
	"TestDDSonicContractFailedReconnectClosesAccessPoint",
	"TestDDSonicContractCloseEndsTheKeyServiceAndLateAnswers",
	"TestDDSonicContractCDNConcurrentFailures",
	"TestDDSonicContractCDNConcurrentDeletesOnPopulatedMap",
	"TestDDSonicContractCDNLockNotHeldAcrossNetwork",
	// Patch 0002's access point: generations, publication and the
	// announcer slot (ap_keylink_contract_test.go.txt).
	"TestDDSonicContractGenerationZeroBeforeConnect",
	"TestDDSonicContractAttachAnnouncerDomain",
	"TestDDSonicContractInitialConnectPublishesWithoutAnnouncer",
	"TestDDSonicContractPublicationAnnouncesBeforeStore",
	"TestDDSonicContractFailedAuthenticationPublishesNothing",
	"TestDDSonicContractReconnectAnnouncesOnceAndSlotStays",
	// Patch 0002's key provider (audio_keylife_contract_test.go.txt).
	"TestDDSonicContractKeylinkOwnershipRule",
	"TestDDSonicContractOneOwnerPerAccessPoint",
	"TestDDSonicContractSecondProviderIsInert",
	"TestDDSonicContractProviderAndAccessPointCollectTogether",
	"TestDDSonicContractRegistrationWaitsForReconnect",
	"TestDDSonicContractEntryRefusalsLaunchNothing",
	"TestDDSonicContractAdmissionRefusalsAfterLaunch",
	"TestDDSonicContractReceiverLeavesAtStepZeroOnDone",
	"TestDDSonicContractReceiverLeavesUnderContinuousPackets",
	"TestDDSonicContractReceiverStepOneSeesClosedSubscription",
	"TestDDSonicContractReceiverStepThreeTerminalCloseTrials",
	"TestDDSonicContractReceiverBranchesDeterministic",
	"TestDDSonicContractReplyOutcomeStands",
	"TestDDSonicContractFenceFirstDiscardsHeldPacket",
	"TestDDSonicContractHeldReplyWinsOverExpiryAndReplacement",
	"TestDDSonicContractWaitingPacketTakenAtStepOne",
	"TestDDSonicContractWriterIsFIFOUnderInboundPackets",
	"TestDDSonicContractReplacementWhileWriterHeld",
	"TestDDSonicContractReplacementBeforeHeldPass",
	"TestDDSonicContractExpiryWithoutPublicationIsFinal",
	"TestDDSonicContractUnansweredRequestEndsAtWindow",
	"TestDDSonicContractExpiryDefersToAnnouncedPublication",
	"TestDDSonicContractExpiryBeforeAnnouncementWhenL7HoldsLock",
	"TestDDSonicContractPublisherWaitsForReceiverLock",
	"TestDDSonicContractDelayedStampAcrossPublication",
	"TestDDSonicContractCommittedBehindHeldReconnect",
	"TestDDSonicContractCancelledAroundStampIsNotResurrected",
	"TestDDSonicContractReceiverDoesNotLog",
	"TestDDSonicContractTwoRapidGenerations",
	"TestDDSonicContractAnnouncementDefersExpiry",
	"TestDDSonicContractDoneBetweenAnnouncementAndStore",
	"TestDDSonicContractWriterHeldBeforeSendLock",
	"TestDDSonicContractWriterHoldingSendLock",
	"TestDDSonicContractWriteErrorDisposition",
	"TestDDSonicContractLastSequenceIssuedOnce",
	"TestDDSonicContractFirstRegistrationHeldCallersCancel",
	"TestDDSonicContractQueuedRequestCancelledBeforeCommit",
	// The base-compatible mechanism probes (audio_baseprobe_contract_test.go.txt):
	// green here, and red against the base (baseProbes).
	"TestDDSonicContractProbeS2InFlightAcrossReconnect",
	"TestDDSonicContractProbeS5RefusedEntryWritesNothing",
	"TestDDSonicContractProbeS7CancelDuringHeldWrite",
	"TestDDSonicContractProbeS13FirstRegistrationHeld",
	// ddsonic: patch 0003's single-flight prefetch admission
	// (audio_prefetch_contract_test.go.txt; prefetch_mutants_test.go).
	"TestDDSonicContractPrefetchC1SingleFlight",
	"TestDDSonicContractPrefetchC2AtomicReservation",
	"TestDDSonicContractPrefetchC3CancelledStreamQuiesces",
	"TestDDSonicContractPrefetchC4SuccessPath",
	"TestDDSonicContractPrefetchC5DirectClose",
	"TestDDSonicContractPrefetchC6OverlapForeground",
	"TestDDSonicContractPrefetchC7FailureReadmission",
	"TestDDSonicContractPrefetchC8WaiterWakeup",
	"TestDDSonicContractPrefetchC9ErrorPropagation",
	"TestDDSonicContractPrefetchC10AccountingOrder",
	"TestDDSonicContractPrefetchC9BoundedJoinControl",
	"TestDDSonicContractPrefetchC9LateResultControl",
	"TestDDSonicContractPrefetchC9DeadlineBoundaryControl",
}

// baseProbes are the probes that must fail against the unpatched base, each
// on its marked behavioural assertion (design S18).
var baseProbes = map[string]string{
	"TestDDSonicContractProbeS2InFlightAcrossReconnect":   "S2-BASE-RED",
	"TestDDSonicContractProbeS5RefusedEntryWritesNothing": "S5-BASE-RED",
	"TestDDSonicContractProbeS7CancelDuringHeldWrite":     "S7-BASE-RED",
	"TestDDSonicContractProbeS13FirstRegistrationHeld":    "S13-BASE-RED",
}

// overlayFiles maps each contract file to where it is compiled in the SDK.
var overlayFiles = map[string]string{
	"ap_helper.go.txt":                 "ap/zz_ddsonic_contract_helper.go",
	"ap_fake_helper.go.txt":            "ap/zz_ddsonic_contract_fake.go",
	"ap_contract_test.go.txt":          "ap/zz_ddsonic_contract_test.go",
	"audio_contract_test.go.txt":       "audio/zz_ddsonic_contract_test.go",
	"aged_key_contract_test.go.txt":    "audio/zz_ddsonic_aged_contract_test.go",
	"mp3_contract_test.go.txt":         "mp3/zz_ddsonic_contract_test.go",
	"player_cdn_race_test.go.txt":      "player/zz_ddsonic_cdn_race_test.go",
	"player_cdn_lockprobe_test.go.txt": "player/zz_ddsonic_cdn_lockprobe_test.go",
	// ddsonic: patch 0002's contract files.
	"ap_keylink_helper.go.txt":           "ap/zz_ddsonic_keylink_helper.go",
	"ap_keylink_contract_test.go.txt":    "ap/zz_ddsonic_keylink_contract_test.go",
	"audio_keylife_contract_test.go.txt": "audio/zz_ddsonic_keylife_contract_test.go",
	// ddsonic: the base-compatible probes, and their per-stage adapter.
	"ap_probe_helper.go.txt":               "ap/zz_ddsonic_probe_helper.go",
	"audio_baseprobe_contract_test.go.txt": "audio/zz_ddsonic_baseprobe_contract_test.go",
	"audio_probe_adapter_base.go.txt":      "audio/zz_ddsonic_probe_adapter_test.go",
	"audio_probe_adapter_active.go.txt":    "audio/zz_ddsonic_probe_adapter_test.go",
	// ddsonic: patch 0003's contract (compiles against the base too, so the
	// authentic base reader can be its negative control M2).
	"audio_prefetch_contract_test.go.txt": "audio/zz_ddsonic_prefetch_contract_test.go",
}

// activeOnly are the contract files that name what the replacement's patches
// add, so they are compiled into the active tree only, never into the base.
var activeOnly = map[string]bool{
	"player_cdn_lockprobe_test.go.txt":   true,
	"ap_keylink_helper.go.txt":           true,
	"ap_keylink_contract_test.go.txt":    true,
	"audio_keylife_contract_test.go.txt": true,
	"aged_key_contract_test.go.txt":      true,
	"audio_probe_adapter_active.go.txt":  true,
}

// baseOnly are the contract files compiled into the base only: the base
// side of a per-stage adapter.
var baseOnly = map[string]bool{
	"audio_probe_adapter_base.go.txt": true,
}

// stepError says which step of the sequence failed, with the tool's output
// as it was.
type stepError struct {
	step   int
	what   string
	output string
}

func (e *stepError) Error() string {
	if e.output == "" {
		return fmt.Sprintf("sdk contract, step %d: %s", e.step, e.what)
	}
	return fmt.Sprintf("sdk contract, step %d: %s\n%s", e.step, e.what, e.output)
}

// runner is one execution of the sequence. Its fields beyond dir are the
// inputs a test may replace to show that a fault is caught.
type runner struct {
	dir string // a temporary directory of this run's own

	repo  string // the repository, read only
	proxy string // the offline module source: a module download directory
	goBin string // the Go executable
	// pinned is the toolchain version the repository pins (mise.toml).
	pinned string
	// corruptStaged changes a byte of the staged copy before it is compared;
	// extraGoMod is appended to the scratch go.mod after the one replace.
	corruptStaged bool
	extraGoMod    string
	// stage is "" for the active tree, or "base" (stageEnv).
	stage string
	// activeOverride, when set, stands for the tracked tree.
	activeOverride string

	env []string
	// notes are what the steps after the tests found, for the log.
	notes []string
}

func newRunner(t *testing.T) *runner {
	t.Helper()
	if os.Getenv(childMarker) != "" {
		t.Fatal("the contract runner is running inside its own child")
	}
	repo, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := pinnedGo(repo)
	if err != nil {
		t.Fatal(err)
	}
	return &runner{
		dir:    t.TempDir(),
		repo:   repo,
		proxy:  filepath.Join(realModCache(), "cache", "download"),
		goBin:  filepath.Join(runtime.GOROOT(), "bin", "go"),
		pinned: pinned,
		stage:  os.Getenv(stageEnv),
	}
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = parent
	}
}

// pinnedGo is the Go version the repository pins, as runtime.Version spells
// it.
func pinnedGo(repo string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repo, "mise.toml"))
	if err != nil {
		return "", err
	}
	m := regexp.MustCompile(`(?m)^go\s*=\s*"([^"]+)"`).FindSubmatch(data)
	if m == nil {
		return "", errors.New("mise.toml pins no Go version")
	}
	return "go" + string(m[1]), nil
}

// realModCache is the user's module cache. It is found without running a
// tool, and only its download directory is ever used, read only.
func realModCache() string {
	if dir := os.Getenv("GOMODCACHE"); dir != "" {
		return dir
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, _ := os.UserHomeDir()
		gopath = filepath.Join(home, "go")
	}
	return filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
}

// environment is what every tool the runner starts gets, and nothing else:
// private, empty caches and home; an offline module source; the local
// toolchain; cgo and the directories where the C compiler and pkg-config
// are. It is set before any tool is run.
func (r *runner) environment() error {
	for _, d := range []string{"home", "tmp", "gocache", "gopath", "gomodcache", "contract"} {
		if err := os.MkdirAll(filepath.Join(r.dir, d), 0o755); err != nil {
			return err
		}
	}
	path := "/usr/local/bin:/usr/bin:/bin"
	pkgConfig := ""
	if runtime.GOOS == "darwin" {
		// Homebrew's two prefixes, where pkg-config and the codec libraries
		// live there. Unverified until macOS acceptance.
		path = "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
		pkgConfig = "/opt/homebrew/lib/pkgconfig:/usr/local/lib/pkgconfig"
	}
	r.env = []string{
		"PATH=" + path,
		"HOME=" + filepath.Join(r.dir, "home"),
		"TMPDIR=" + filepath.Join(r.dir, "tmp"),
		"GOCACHE=" + filepath.Join(r.dir, "gocache"),
		"GOPATH=" + filepath.Join(r.dir, "gopath"),
		"GOMODCACHE=" + filepath.Join(r.dir, "gomodcache"),
		"GOENV=off",
		"GOFLAGS=-mod=readonly -modcacherw",
		"GOTOOLCHAIN=local",
		"GOSUMDB=off",
		"GOPROXY=file://" + filepath.ToSlash(r.proxy),
		"CGO_ENABLED=1",
		childMarker + "=1",
		"DDSONIC_CONTRACT_FIXTURES=" + filepath.Join(r.dir, "contract"),
	}
	if pkgConfig != "" {
		r.env = append(r.env, "PKG_CONFIG_PATH="+pkgConfig)
	}
	return nil
}

// tool runs the Go tool in cwd, bounded, and returns what it wrote. A
// non-zero exit is an error that carries both streams as they were: it is
// looked at before anything is parsed, because a failing tool may say why on
// either stream, and what it put on the other is not a result.
func (r *runner) tool(step int, what, cwd string, limit time.Duration, args ...string) ([]byte, error) {
	return r.toolEnv(step, what, cwd, limit, nil, args...)
}

// toolEnv is tool with extra environment entries.
func (r *runner) toolEnv(step int, what, cwd string, limit time.Duration, extra []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.goBin, args...)
	cmd.Dir, cmd.Env = cwd, append(slices.Clone(r.env), extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), toolResult(step, what, ctx, err, stdout.Bytes(), stderr.Bytes())
}

// toolResult turns a tool's exit into nil or a stepError with its output.
func toolResult(step int, what string, ctx context.Context, err error, stdout, stderr []byte) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		what += ": killed at its deadline"
	} else {
		what += ": " + err.Error()
	}
	return &stepError{step, what, "stderr:\n" + string(stderr) + "\nstdout:\n" + string(stdout)}
}

type moduleInfo struct {
	Path, Version, Dir string
	Main               bool
	Replace            *moduleInfo
}

// run executes the sequence and returns the tests that passed.
func (r *runner) run() ([]string, error) {
	// 0. The environment, then the toolchain: the executable that built this
	// test, which must be the version the repository pins.
	if err := r.environment(); err != nil {
		return nil, &stepError{0, "set up private directories: " + err.Error(), ""}
	}
	if _, err := os.Stat(r.goBin); err != nil {
		return nil, &stepError{0, "the Go executable is missing: " + err.Error(), ""}
	}
	out, err := r.tool(0, "go env GOVERSION", r.repo, time.Minute, "env", "GOVERSION")
	if err != nil {
		return nil, err
	}
	if got := strings.TrimSpace(string(out)); got != runtime.Version() || got != r.pinned {
		return nil, &stepError{0, fmt.Sprintf("toolchain mismatch: the executable is %s, this test was built with %s, the repository pins %s", got, runtime.Version(), r.pinned), ""}
	}

	// 2. The private caches are empty: nothing warm can hide a missing step.
	for _, d := range []string{"gocache", "gomodcache", "gopath"} {
		if entries, _ := os.ReadDir(filepath.Join(r.dir, d)); len(entries) != 0 {
			return nil, &stepError{2, d + " is not empty at the start", ""}
		}
	}

	// 3. The selected version, from metadata. No directory is asked of this
	// step: go list -m does not extract a module into a cold cache.
	out, err = r.tool(3, "go list -m", r.repo, 2*time.Minute, "list", "-m", "-json", sdkModule)
	if err != nil {
		return nil, err
	}
	var selected moduleInfo
	if err := json.Unmarshal(out, &selected); err != nil || selected.Version != sdkPin {
		return nil, &stepError{3, fmt.Sprintf("the repository selects %s %q, the contract tests are for %s (%v)", sdkModule, selected.Version, sdkPin, err), string(out)}
	}
	if selected.Replace == nil || selected.Replace.Path != replaceTarget {
		return nil, &stepError{3, fmt.Sprintf("the repository does not replace %s with %s", sdkModule, replaceTarget), string(out)}
	}
	prov, err := readProvenance(r.repo)
	if err != nil {
		return nil, &stepError{3, err.Error(), ""}
	}

	// 4. The base: the upstream module, extracted offline into the private
	// cache, authenticated by Go against the provenance's base hashes.
	baseDir, err := r.downloadBase(prov)
	if err != nil {
		return nil, err
	}

	// 5. The base directory is the extracted one, inside the private cache.
	modCache := filepath.Join(r.dir, "gomodcache")
	if info, err := os.Stat(baseDir); err != nil || !info.IsDir() || !within(modCache, baseDir) {
		return nil, &stepError{5, fmt.Sprintf("the module directory %q is not the extracted one inside the private module cache", baseDir), ""}
	}

	// 6. The vehicle: the base with the patches applied is the tracked active
	// tree, byte for byte, and both hash to the provenance's record. Then a
	// copy of the tree under test, outside the module cache: Go refuses an
	// overlay under GOMODCACHE, and that guard is not worked around.
	active, err := r.verifyVehicle(prov, baseDir)
	if err != nil {
		return nil, err
	}
	source := active
	if r.stage == "base" {
		source = baseDir
	}
	staged := filepath.Join(r.dir, "sdk")
	if err := copyTree(source, staged); err != nil {
		return nil, &stepError{6, "stage the SDK: " + err.Error(), ""}
	}
	if r.corruptStaged {
		f, err := os.OpenFile(filepath.Join(staged, "ap", "ap.go"), os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString("\n")
			f.Close()
		}
		if err != nil {
			return nil, &stepError{6, "corrupt the staged copy: " + err.Error(), ""}
		}
	}
	if within(modCache, staged) {
		return nil, &stepError{6, "the staged copy is inside the module cache", ""}
	}
	if diff, err := compareTrees(source, staged); err != nil || diff != "" {
		return nil, &stepError{6, fmt.Sprintf("the staged copy differs from the tree under test: %s (%v)", diff, err), ""}
	}

	// 7. A scratch module with the repository's graph and one replacement.
	scratch := filepath.Join(r.dir, "mod")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, &stepError{7, err.Error(), ""}
	}
	goMod, err := os.ReadFile(filepath.Join(r.repo, "go.mod"))
	if err != nil {
		return nil, &stepError{7, err.Error(), ""}
	}
	goSum, err := os.ReadFile(filepath.Join(r.repo, "go.sum"))
	if err != nil {
		return nil, &stepError{7, err.Error(), ""}
	}
	// The repository's own replacement is swapped for the staged copy.
	repoReplace := fmt.Sprintf("replace %s %s => %s\n", sdkModule, sdkPin, replaceTarget)
	if bytes.Count(goMod, []byte(repoReplace)) != 1 {
		return nil, &stepError{7, "go.mod does not hold the one expected replace line: " + strings.TrimSpace(repoReplace), ""}
	}
	goMod = bytes.Replace(goMod, []byte(repoReplace), nil, 1)
	goMod = append(goMod, []byte(fmt.Sprintf("\nreplace %s %s => %s\n%s", sdkModule, sdkPin, staged, r.extraGoMod))...)
	if err := errors.Join(os.WriteFile(filepath.Join(scratch, "go.mod"), goMod, 0o644), os.WriteFile(filepath.Join(scratch, "go.sum"), goSum, 0o644)); err != nil {
		return nil, &stepError{7, err.Error(), ""}
	}
	repoGraph, err := r.graph(r.repo)
	if err != nil {
		return nil, err
	}
	scratchGraph, err := r.graph(scratch)
	if err != nil {
		return nil, err
	}
	if diff := compareGraphs(repoGraph, scratchGraph, staged); diff != "" {
		return nil, &stepError{7, "the scratch module's graph differs from the repository's: " + diff, ""}
	}
	sumUnchanged := func(step int) error {
		got, err := os.ReadFile(filepath.Join(scratch, "go.sum"))
		if err != nil || !bytes.Equal(got, goSum) {
			return &stepError{step, fmt.Sprintf("the scratch go.sum is no longer the repository's (%v)", err), ""}
		}
		return nil
	}
	if err := sumUnchanged(7); err != nil {
		return nil, err
	}

	// 8. The overlay, onto the staged copy, and the tests.
	replace := map[string]string{}
	testdata := filepath.Join(r.repo, "external", "spotify", "sdkcontract", "testdata")
	entries, err := os.ReadDir(testdata)
	if err != nil {
		return nil, &stepError{8, err.Error(), ""}
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(testdata, e.Name()))
		if err != nil {
			return nil, &stepError{8, err.Error(), ""}
		}
		local := filepath.Join(r.dir, "contract", e.Name())
		if err := os.WriteFile(local, data, 0o644); err != nil {
			return nil, &stepError{8, err.Error(), ""}
		}
		if target, ok := overlayFiles[e.Name()]; ok && !(r.stage == "base" && activeOnly[e.Name()]) && !(r.stage != "base" && baseOnly[e.Name()]) {
			replace[filepath.Join(staged, filepath.FromSlash(target))] = local
		}
	}
	want := len(overlayFiles) - len(baseOnly)
	if r.stage == "base" {
		want = len(overlayFiles) - len(activeOnly)
	}
	if len(replace) != want {
		return nil, &stepError{8, fmt.Sprintf("%d of the %d contract files were found", len(replace), want), ""}
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": replace})
	overlayPath := filepath.Join(r.dir, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
		return nil, &stepError{8, err.Error(), ""}
	}
	out, err = r.tool(8, "go test", scratch, 15*time.Minute, "test", "-overlay="+overlayPath, "-race", "-count=1", "-json",
		"-run", "^TestDDSonicContract", sdkModule+"/ap", sdkModule+"/audio", sdkModule+"/mp3", sdkModule+"/player")
	if r.stage == "base" {
		// Diagnostic only: what failed against the unpatched base, and
		// whether each base probe failed on its marked assertion.
		passed, bad := testOutcomes(out)
		return passed, &stepError{10, "base stage (diagnostic, never a pass); failed: " + strings.Join(bad, ", ") + "; " + baseProbeFindings(out, bad), string(out)}
	}
	if err != nil {
		return nil, err
	}
	if err := sumUnchanged(8); err != nil {
		return nil, err
	}
	if diff, err := compareTrees(source, staged); err != nil || diff != "" {
		return nil, &stepError{8, fmt.Sprintf("the staged copy changed during the run: %s (%v)", diff, err), ""}
	}

	// 10. Every expected test is shown to have run and passed. Subtests are
	// part of the test they are in.
	all, bad := testOutcomes(out)
	if len(bad) != 0 {
		return all, &stepError{10, "contract tests did not pass: " + strings.Join(bad, ", "), string(out)}
	}
	passed := slices.DeleteFunc(slices.Clone(all), func(name string) bool { return strings.Contains(name, "/") })
	for _, want := range contractTests {
		if !slices.Contains(passed, want) {
			return passed, &stepError{10, want + " was not shown to pass", string(out)}
		}
	}
	if len(passed) != len(contractTests) {
		return passed, &stepError{10, fmt.Sprintf("%d tests passed, %d are expected: %q", len(passed), len(contractTests), passed), ""}
	}

	// 11. The symbols gate, from its own module.
	if err := r.symbolsGate(scratch, staged, baseDir, overlayPath); err != nil {
		return passed, err
	}

	// 12. The behavioural mutations: each must turn its test red.
	if err := r.mutations(scratch, staged, replace); err != nil {
		return passed, err
	}
	// ddsonic: patch 0003's negative controls M1-M3 (prefetch_mutants_test.go).
	if err := r.prefetchMutations(scratch, staged, baseDir, replace); err != nil {
		return passed, err
	}
	if err := sumUnchanged(12); err != nil {
		return passed, err
	}
	if diff, err := compareTrees(source, staged); err != nil || diff != "" {
		return passed, &stepError{12, fmt.Sprintf("the staged copy changed during the run: %s (%v)", diff, err), ""}
	}

	// 13-14. ddsonic: the bypass binding and the composed stage
	// (composed_test.go).
	if err := r.composed(scratch, staged); err != nil {
		return passed, err
	}
	if err := sumUnchanged(14); err != nil {
		return passed, err
	}
	if diff, err := compareTrees(source, staged); err != nil || diff != "" {
		return passed, &stepError{14, fmt.Sprintf("the staged copy changed during the run: %s (%v)", diff, err), ""}
	}

	// 15. ddsonic: the stock stage (stock_test.go; plan v9 §4, §9): rc.4's
	// authenticated archive staged over the authenticated base, then its
	// probes, once, on that stage.
	notes, err := r.stockStage(baseDir, prov)
	r.notes = append(r.notes, notes...)
	if err != nil {
		return passed, err
	}
	notes, err = r.stockProbes(prov, true)
	r.notes = append(r.notes, notes...)
	if err != nil {
		return passed, err
	}
	return passed, nil
}

// baseProbeFindings says, for each base probe, whether it failed on its
// marked assertion, and whether any package failed to build.
func baseProbeFindings(out []byte, bad []string) string {
	text := string(out)
	var f []string
	names := slices.Sorted(maps.Keys(baseProbes))
	for _, name := range names {
		marker := baseProbes[name]
		if slices.Contains(bad, name+" (fail)") && strings.Contains(text, marker) {
			f = append(f, "base probe "+name+": red with "+marker)
		} else {
			f = append(f, "base probe "+name+": NOT shown red")
		}
	}
	if strings.Contains(text, "[build failed]") || strings.Contains(text, "[setup failed]") {
		f = append(f, "a package failed to build")
	} else {
		f = append(f, "no build failure")
	}
	return strings.Join(f, "; ")
}

func (r *runner) graph(dir string) (map[string]moduleInfo, error) {
	out, err := r.tool(7, "go list -m all in "+dir, dir, 5*time.Minute, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	graph := map[string]moduleInfo{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var m moduleInfo
		if err := dec.Decode(&m); err == io.EOF {
			break
		} else if err != nil {
			return nil, &stepError{7, "go list -m all: " + err.Error(), string(out)}
		}
		graph[m.Path] = m
	}
	return graph, nil
}

// compareGraphs reports how the scratch module's selected versions differ
// from the repository's. The SDK's replacement is the one difference allowed.
func compareGraphs(repo, scratch map[string]moduleInfo, staged string) string {
	var diffs []string
	for path, want := range repo {
		got, ok := scratch[path]
		switch {
		case want.Main:
		case !ok:
			diffs = append(diffs, path+" is missing")
		case got.Version != want.Version:
			diffs = append(diffs, fmt.Sprintf("%s is %s, not %s", path, got.Version, want.Version))
		case (got.Replace != nil) != (want.Replace != nil) && path != sdkModule:
			diffs = append(diffs, path+" is replaced")
		}
	}
	for path, got := range scratch {
		if _, ok := repo[path]; !ok && !got.Main {
			diffs = append(diffs, path+" is added")
		}
	}
	// The SDK's replacement is the one difference, and its targets are named:
	// the repository's is the tracked tree, the scratch module's the staged
	// copy, so a replacement elsewhere cannot pass for either.
	if sdk := repo[sdkModule]; sdk.Replace == nil || sdk.Replace.Path != replaceTarget {
		diffs = append(diffs, sdkModule+" is not replaced by "+replaceTarget+" in the repository")
	}
	if sdk := scratch[sdkModule]; sdk.Replace == nil || sdk.Replace.Dir != staged {
		diffs = append(diffs, sdkModule+" is not replaced by the staged copy")
	}
	slices.Sort(diffs)
	return strings.Join(diffs, "; ")
}

// testOutcomes reads go test -json: the tests that passed, and anything that
// failed or was skipped.
func testOutcomes(out []byte) (passed, bad []string) {
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var ev struct{ Action, Package, Test string }
		if err := dec.Decode(&ev); err != nil {
			break
		}
		switch {
		case ev.Test != "" && ev.Action == "pass":
			passed = append(passed, ev.Test)
		case ev.Test != "" && (ev.Action == "fail" || ev.Action == "skip"):
			bad = append(bad, ev.Test+" ("+ev.Action+")")
		case ev.Test == "" && ev.Action == "fail":
			bad = append(bad, ev.Package+" (fail)")
		}
	}
	slices.Sort(passed)
	return passed, bad
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// compareTrees reports the first difference between two trees: the same
// relative paths, and every file byte for byte. Modes are not compared.
func compareTrees(a, b string) (string, error) {
	list := func(root string) (map[string]bool, error) {
		files := map[string]bool{}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				rel, _ := filepath.Rel(root, path)
				files[rel] = true
			}
			return nil
		})
		return files, err
	}
	fa, err := list(a)
	if err != nil {
		return "", err
	}
	fb, err := list(b)
	if err != nil {
		return "", err
	}
	for rel := range fb {
		if !fa[rel] {
			return rel + " is only in the copy", nil
		}
	}
	for rel := range fa {
		if !fb[rel] {
			return rel + " is missing from the copy", nil
		}
		da, err := os.ReadFile(filepath.Join(a, rel))
		if err != nil {
			return "", err
		}
		db, err := os.ReadFile(filepath.Join(b, rel))
		if err != nil {
			return "", err
		}
		if !bytes.Equal(da, db) {
			return rel + " differs", nil
		}
	}
	return "", nil
}

// TestSDKContract is the gate: the whole sequence, from empty private caches
// and an offline, verified module source, to the eighty-one contract tests shown
// to have run and passed under the race detector against a staged copy of
// the pinned SDK, the symbols gate, and the behavioural mutations shown red.
func TestSDKContract(t *testing.T) {
	r := newRunner(t)
	start := time.Now()
	passed, err := r.run()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d contract tests passed against %s@%s with %s in %v:\n  %s\n%s",
		len(passed), sdkModule, sdkPin, runtime.Version(), time.Since(start).Round(time.Millisecond), strings.Join(passed, "\n  "), strings.Join(r.notes, "\n"))
}

// wantStep runs r and requires it to fail at step, having run no contract
// test.
func wantStep(t *testing.T, r *runner, step int, contains string) {
	t.Helper()
	passed, err := r.run()
	var se *stepError
	if !errors.As(err, &se) || se.step != step || len(passed) != 0 {
		t.Fatalf("the runner ended with %d tests passed and %v; want it to fail at step %d with none run", len(passed), err, step)
	}
	if contains != "" && !strings.Contains(err.Error(), contains) {
		t.Errorf("the failure does not say %q:\n%v", contains, err)
	}
	if _, statErr := os.Stat(filepath.Join(r.dir, "overlay.json")); statErr == nil && step < 8 {
		t.Error("an overlay was built although the sequence failed before it")
	}
}

// A toolchain that is not the pinned one fails, at the start.
func TestSDKContractWrongToolchainFails(t *testing.T) {
	r := newRunner(t)
	r.pinned = "go9.99.9"
	wantStep(t, r, 0, "toolchain mismatch")
}

// offlineSource is a module download directory like the real one, as
// symbolic links, except for the SDK's own files, which change copies.
func offlineSource(t *testing.T, change func(versions string)) string {
	t.Helper()
	real := filepath.Join(realModCache(), "cache", "download")
	dir := filepath.Join(t.TempDir(), "download")
	sdkVersions := filepath.Join(real, filepath.FromSlash(sdkModule), "@v")
	err := filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(real, path)
		target := filepath.Join(dir, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case filepath.Dir(path) == sdkVersions:
			if strings.HasSuffix(path, ".lock") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, 0o644)
		}
		return os.Symlink(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	change(filepath.Join(dir, filepath.FromSlash(sdkModule), "@v"))
	return dir
}

// A source without the pinned module's archive fails at the extraction.
func TestSDKContractMissingArchiveFails(t *testing.T) {
	r := newRunner(t)
	r.proxy = offlineSource(t, func(versions string) {
		if err := os.Remove(filepath.Join(versions, sdkPin+".zip")); err != nil {
			t.Fatal(err)
		}
	})
	wantStep(t, r, 4, sdkPin+".zip")
}

// A well-formed archive whose bytes are not the ones go.sum records fails at
// the extraction, as a checksum mismatch.
func TestSDKContractChecksumMismatchFails(t *testing.T) {
	r := newRunner(t)
	r.proxy = offlineSource(t, func(versions string) {
		path := filepath.Join(versions, sdkPin+".zip")
		in, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		out := zip.NewWriter(&buf)
		changed := false
		for _, f := range in.File {
			if strings.HasSuffix(f.Name, "/") {
				continue // a module archive holds files only
			}
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if !changed && strings.HasSuffix(f.Name, "/ap/ap.go") {
				data, changed = append(data, []byte("\n// changed\n")...), true
			}
			w, err := out.Create(f.Name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		in.Close()
		if err := out.Close(); err != nil || !changed {
			t.Fatalf("could not rebuild the archive: %v, changed %t", err, changed)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	wantStep(t, r, 4, "checksum mismatch")
}

// A staged copy that is not byte for byte the extracted module fails before
// anything is compiled from it.
func TestSDKContractStagedCopyMismatchFails(t *testing.T) {
	r := newRunner(t)
	r.corruptStaged = true
	wantStep(t, r, 6, "differs")
}

// A scratch module whose graph is not the repository's fails before the
// tests run.
func TestSDKContractGraphMismatchFails(t *testing.T) {
	r := newRunner(t)
	r.extraGoMod = "replace golang.org/x/oauth2 => " + filepath.Join(t.TempDir(), "elsewhere") + "\n"
	wantStep(t, r, 7, "")
}

// A tool that fails may say why on stderr alone, with nothing, or with
// something that is not a result, on stdout. Its exit is what decides, before
// anything is parsed, and both streams are kept.
func TestToolFailureIsReadBeforeItsOutput(t *testing.T) {
	exit := &exec.ExitError{}
	for name, tt := range map[string]struct{ stdout, stderr string }{
		"stderr only":          {"", "verifying module: checksum mismatch\nSECURITY ERROR"},
		"an error in the JSON": {`{"Path":"m","Error":"zip has unexpected file"}`, ""},
		"both":                 {`{"Path":"m","Version":"v1","Dir":"/x"}`, "go: something else failed"},
	} {
		err := toolResult(4, "go mod download", context.Background(), exit, []byte(tt.stdout), []byte(tt.stderr))
		var se *stepError
		if !errors.As(err, &se) || se.step != 4 {
			t.Errorf("%s: a failed tool gave %v, want a step error", name, err)
			continue
		}
		if !strings.Contains(err.Error(), tt.stdout) || !strings.Contains(err.Error(), tt.stderr) {
			t.Errorf("%s: the failure dropped part of the tool's output:\n%v", name, err)
		}
	}
	if err := toolResult(4, "go mod download", context.Background(), nil, []byte("{}"), []byte("warning")); err != nil {
		t.Errorf("a tool that exited 0 gave %v", err)
	}
}
