// Command acceptance validates the acceptance case catalog and runs the
// scripted cases, producing the summary for a release record and a sealed
// evidence bundle. See docs/ddsonic/acceptance.md.
//
//	acceptance validate
//	acceptance run --dev
//	acceptance run --archive A.tar.gz --sums SHA256SUMS --version vX --source-sha SHA
//
// It reads a release's files from disk. It downloads nothing and never
// creates, edits or publishes a release.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bjarneo/cliamp/acceptance"
	"github.com/bjarneo/cliamp/acceptance/harness"
	"github.com/bjarneo/cliamp/internal/appdir"
)

// scenarioDirs are the packages that hold nothing but acceptance cases:
// every test in them must be named by a catalog case.
var scenarioDirs = []string{"acceptance/binarycases"}

// Exit statuses.
const (
	exitOK      = 0
	exitBlocked = 1 // the catalog is invalid, a case did not pass, or the gate is not met
	exitUsage   = 2 // the run could not be set up
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	root, err := harness.RepoRoot()
	if err != nil {
		fatal(err)
	}
	switch os.Args[1] {
	case "validate":
		os.Exit(validate(root))
	case "run":
		os.Exit(run(root, os.Args[2:]))
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  acceptance validate
  acceptance run --dev [--manual FILE] [--triage FILE] [--evidence-dir DIR]
  acceptance run --archive FILE --sums FILE --version vX.Y.Z --source-sha SHA [--manual FILE] [--triage FILE] [--evidence-dir DIR]`)
	os.Exit(exitUsage)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "acceptance:", err)
	os.Exit(exitUsage)
}

func validate(root string) int {
	catalog, err := acceptance.LoadCatalog(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance:", err)
		return exitBlocked
	}
	problems := catalog.Validate(root, scenarioDirs...)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, p)
	}
	if len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "the case catalog has %d problems\n", len(problems))
		return exitBlocked
	}
	counts := map[string]int{}
	for _, c := range catalog.Cases {
		switch {
		case c.Driver == acceptance.DriverManual:
			counts["manual"]++
		case c.Implemented:
			counts["scripted"]++
		default:
			counts["scripted, no runner yet"]++
		}
	}
	fmt.Printf("%s: %d cases valid (%d scripted, %d scripted with no runner yet, %d manual), %d semantics rows\n",
		acceptance.CatalogPath, len(catalog.Cases), counts["scripted"], counts["scripted, no runner yet"], counts["manual"], len(catalog.Semantics))
	return exitOK
}

func run(root string, args []string) int {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	dev := flags.Bool("dev", false, "test a local build of the checkout; never release evidence")
	var release acceptance.ReleaseInput
	flags.StringVar(&release.Archive, "archive", "", "the draft release's binary archive, already downloaded")
	flags.StringVar(&release.Sums, "sums", "", "the release's SHA256SUMS file")
	flags.StringVar(&release.Version, "version", "", "the version the candidate must report")
	flags.StringVar(&release.SourceSHA, "source-sha", "", "the commit the candidate's tag points to")
	manual := flags.String("manual", "", "results of manual cases, recorded by hand (YAML)")
	triagePath := flags.String("triage", "", "severity and disposition of the cases that did not pass (YAML)")
	evidenceDir := flags.String("evidence-dir", "", "where to keep the evidence bundle (default: in the player's data directory)")
	executedBy := flags.String("executed-by", "acceptance runner", "who ran the scripted cases")
	_ = flags.Parse(args)
	if *dev == (release.Archive != "") {
		fmt.Fprintln(os.Stderr, "acceptance: give either --dev, or --archive with --sums, --version and --source-sha")
		return exitUsage
	}

	// The catalog is the basis of everything that follows. Invalid, it
	// proves nothing, so nothing runs.
	catalog, err := acceptance.LoadCatalog(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance:", err)
		return exitBlocked
	}
	if problems := catalog.Validate(root, scenarioDirs...); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, p)
		}
		fmt.Fprintln(os.Stderr, "acceptance: the case catalog is invalid; nothing was run")
		return exitBlocked
	}

	// Evidence is durable and private; runtime files are short-lived and
	// need short paths. They are kept apart.
	evidenceRoot := *evidenceDir
	if evidenceRoot == "" {
		if evidenceRoot, err = acceptance.EvidenceRoot(); err != nil {
			fatal(err)
		}
	}
	if err := acceptance.CheckEvidenceRoot(evidenceRoot, root); err != nil {
		fatal(err)
	}
	runtimeDir, err := os.MkdirTemp("/tmp", "ddr")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(runtimeDir)

	// One redaction policy for everything this run persists or prints: the
	// bundle's files and the summary.
	redactor := acceptance.NewRedactor()

	var blockers []string
	var id acceptance.Identity
	if *dev {
		binary := filepath.Join(runtimeDir, appdir.Name)
		build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
		build.Dir = root
		if out, err := build.CombinedOutput(); err != nil {
			fatal(fmt.Errorf("build the checkout: %w: %s", err, out))
		}
		if id, err = acceptance.DevelopmentIdentity(binary, root); err != nil {
			fatal(err)
		}
		blockers = append(blockers, "a development run: a local build was tested, not a release archive's binary")
	} else {
		// No fallback: if the archive cannot be identified, nothing is
		// tested in its place.
		if id, err = acceptance.VerifyRelease(release, root, filepath.Join(runtimeDir, "release")); err != nil {
			fmt.Fprintln(os.Stderr, "acceptance: the release artifact could not be verified; nothing was run:", redactor.RedactString(err.Error()))
			return exitBlocked
		}
	}
	if err := id.Describe(root); err != nil {
		fatal(err)
	}
	if !*dev && strings.HasSuffix(id.HarnessRevision, "+dirty") {
		blockers = append(blockers, "the checkout that ran the cases has uncommitted changes: its harness and catalog are not a recorded revision")
	}

	// Every scripted case is deterministic and offline, and runs with the
	// network isolated by the kernel. Where that cannot be established and
	// verified, the cases are blocked: they are not run without it.
	isolation, isolationErr := acceptance.NewIsolation(runtimeDir)
	if isolationErr != nil {
		id.Environment["network isolation"] = "NOT ESTABLISHED: " + isolationErr.Error()
	} else {
		id.Environment["network isolation"] = "a new network namespace per run (unshare --user --net), verified for each run: not the host's, loopback only"
	}

	wantVersion := id.ExpectedVersion
	if wantVersion == "" {
		wantVersion = id.ReportedVersion // a development build: whatever it says
	}
	started := time.Now()
	runID := fmt.Sprintf("%s-%s-%s", started.UTC().Format("20060102T150405Z"), id.Mode, id.ReportedVersion)
	bundle, err := acceptance.NewBundle(evidenceRoot, runID)
	if err != nil {
		fatal(err)
	}
	captures := filepath.Join(bundle.Dir, "captures")

	// In a release run the in-process cases must be testing the release's
	// source, or they are testing something else.
	sourceDrift := ""
	if !*dev {
		sourceDrift = productionDrift(root, id.SourceSHA)
	}

	var results []acceptance.Result
	blocked := func(c acceptance.Case, why string) {
		results = append(results, acceptance.Result{CaseID: c.ID, Execution: acceptance.Blocked, EnvironmentLimitation: why})
	}
	byDir := map[string][]acceptance.Case{}
	for _, c := range catalog.Cases {
		switch {
		case c.Driver == acceptance.DriverManual:
			// Filled in from --manual below.
		case !c.Implemented:
			results = append(results, acceptance.Result{CaseID: c.ID, Execution: acceptance.NotRun, Detail: "scripted, with no runner yet"})
		case isolationErr != nil:
			blocked(c, "kernel network isolation could not be established, and a deterministic case is not run without it: "+isolationErr.Error())
		case c.Lane == acceptance.LaneGo && sourceDrift != "":
			blocked(c, "this checkout's source differs from the release's source commit ("+sourceDrift+"), so an in-process case would not test the release")
		default:
			dir, _, _ := c.RunnerParts()
			byDir[dir] = append(byDir[dir], c)
		}
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		cases := byDir[dir]
		fmt.Fprintf(os.Stderr, "running %d cases in ./%s…\n", len(cases), dir)
		var raw []byte
		for _, c := range cases {
			// Each case runs in a process of its own: its exit status is
			// then about this case alone.
			_, test, _ := c.RunnerParts()
			run, runErr := acceptance.RunGoTest(root, dir, test, isolation,
				harness.BinaryEnv+"="+id.BinaryPath, harness.ArtifactsEnv+"="+captures,
				harness.VersionEnv+"="+wantVersion, harness.BinarySHA256Env+"="+id.BinarySHA256,
				harness.SourceSHAEnv+"="+releaseCommit(id),
				// Offline by construction as well: nothing may be fetched.
				"GOPROXY=off", "GOFLAGS=-mod=mod")
			raw = append(raw, run.Raw...)
			if runErr != nil {
				blocked(c, "the case could not be run, or its isolation could not be verified: "+runErr.Error())
				continue
			}
			results = append(results, run.Result(c.ID, *executedBy, started))
		}
		name := "go-test-" + strings.NewReplacer("/", "_", ".", "root").Replace(dir) + ".jsonl"
		if err := os.WriteFile(filepath.Join(bundle.Dir, name), raw, 0o600); err != nil {
			fatal(err)
		}
	}

	if *manual != "" {
		recorded, err := acceptance.LoadManual(*manual)
		if err != nil {
			fatal(err)
		}
		accepted, problems := recorded.Accept(id, catalog, time.Now())
		results = append(results, accepted...)
		blockers = append(blockers, problems...)
		copyInto(bundle.Dir, "manual-results.yaml", *manual)
	}
	var triage []acceptance.Triage
	if *triagePath != "" {
		if triage, err = acceptance.LoadTriage(*triagePath); err != nil {
			fatal(err)
		}
		copyInto(bundle.Dir, "triage.yaml", *triagePath)
	}

	verdict := acceptance.Evaluate(catalog, nil, results, triage, id.Target(), blockers...)
	rows := make([]acceptance.Result, len(verdict.Rows))
	for i, row := range verdict.Rows {
		rows[i] = row.Result
	}
	copyInto(bundle.Dir, "cases.yaml", filepath.Join(root, acceptance.CatalogPath))
	for name, v := range map[string]any{"results.json": rows, "identity.json": id, "gate.json": verdict.Blocking} {
		if err := acceptance.WriteJSON(filepath.Join(bundle.Dir, name), v); err != nil {
			fatal(err)
		}
	}
	sealed, err := bundle.Seal(redactor)
	if err != nil {
		fatal(fmt.Errorf("seal the evidence bundle: %w", err))
	}
	summary := acceptance.Summary(id, catalog, nil, verdict, sealed, redactor)
	if err := os.WriteFile(strings.TrimSuffix(sealed.Archive, ".tar.gz")+".summary.md", []byte(summary), 0o600); err != nil {
		fatal(err)
	}
	fmt.Print(summary)
	fmt.Fprintf(os.Stderr, "\nevidence: %s\n", sealed.Archive)

	if *dev {
		// A development run answers one question: do the scripted cases pass?
		for _, row := range verdict.Rows {
			if row.Case.Driver == acceptance.DriverScripted && row.Case.Implemented && row.Result.Execution != acceptance.Pass {
				return exitBlocked
			}
		}
		return exitOK
	}
	if !verdict.Publishable() {
		return exitBlocked
	}
	return exitOK
}

// releaseCommit is the commit a release binary must report; a development
// build reports none.
func releaseCommit(id acceptance.Identity) string {
	if id.Mode != acceptance.ModeRelease {
		return ""
	}
	return id.SourceSHA
}

// productionDrift names the production files that differ between the
// release's source commit and this checkout, or "" when there are none.
// Tests, docs and the acceptance tooling may differ: they are the harness,
// whose revision is recorded.
func productionDrift(root, sourceSHA string) string {
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", sourceSHA, "--").Output()
	if err != nil {
		return "the difference could not be computed: " + err.Error()
	}
	var drift []string
	for file := range strings.FieldsSeq(string(out)) {
		switch {
		case strings.HasPrefix(file, "acceptance/"), strings.HasPrefix(file, "docs/"),
			strings.HasSuffix(file, "_test.go"), strings.HasSuffix(file, ".md"), file == "Makefile":
		default:
			drift = append(drift, file)
		}
	}
	if len(drift) > 4 {
		drift = append(drift[:4], fmt.Sprintf("and %d more", len(drift)-4))
	}
	return strings.Join(drift, ", ")
}

func copyInto(dir, name, from string) {
	data, err := os.ReadFile(from)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		fatal(err)
	}
}
