package acceptance

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"go.yaml.in/yaml/v3"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// repoRoot is the repository this test runs in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The catalog in the repository is valid: every runner is a test that
// exists, every procedure a heading that exists, and no scenario test is
// left without a case.
func TestRepositoryCatalogIsValid(t *testing.T) {
	root := repoRoot(t)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range c.Validate(root, "acceptance/binarycases") {
		t.Error(p)
	}
	// Every semantics row is explained in the prose oracle too.
	doc, err := os.ReadFile(filepath.Join(root, "docs", appdir.Name, "acceptance", "semantics.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Semantics {
		if s.List != "" && !strings.Contains(string(doc), strings.Split(s.List, ",")[0]) && !strings.HasPrefix(s.ID, "search/") {
			t.Errorf("semantics.md does not mention the list %q of row %s", s.List, s.ID)
		}
	}
}

const validCase = `
  - id: AA-001
    area: Area
    title: A title
    semantics: general/thing
    tier: deterministic
    driver: manual
    lane: manual
    procedure: doc.md#heading
    reviewer: agent
    applicability: always
    gate: required
    since: v1
`

func catalogWith(caseYAML string) string {
	return "semantics:\n  - id: general/thing\n    kind: general\n    statement: A promise.\ncases:" + caseYAML
}

func TestCatalogValidation(t *testing.T) {
	scripted := strings.NewReplacer("driver: manual", "driver: scripted", "lane: manual", "lane: go",
		"procedure: doc.md#heading", "runner: pkg:TestThing\n    implemented: true").Replace(validCase)
	tests := []struct {
		name string
		yaml string
		want string // a fragment of the one problem expected; "" for none
	}{
		{"a valid manual case", catalogWith(validCase), ""},
		{"a valid scripted case", catalogWith(scripted), ""},
		{"no cases", "semantics: []\ncases: []\n", "no cases"},
		{"a malformed id", catalogWith(strings.Replace(validCase, "AA-001", "aa-1", 1)), "is not of the form"},
		{"a duplicate id", catalogWith(validCase + validCase), "used twice"},
		{"a missing title", catalogWith(strings.Replace(validCase, "title: A title", "title: ''", 1)), "title is missing"},
		{"an unknown tier", catalogWith(strings.Replace(validCase, "tier: deterministic", "tier: nightly", 1)), `tier "nightly" is not one of`},
		{"an unknown gate", catalogWith(strings.Replace(validCase, "gate: required", "gate: maybe", 1)), `gate "maybe" is not one of`},
		{"an unknown reviewer", catalogWith(strings.Replace(validCase, "reviewer: agent", "reviewer: nobody", 1)), `reviewer "nobody"`},
		{"semantics that is no row", catalogWith(strings.Replace(validCase, "general/thing", "general/other", 1)), "is not a row"},
		{"a manual case with a runner", catalogWith(validCase + "    runner: pkg:TestX\n"), "a procedure, not a runner"},
		{"a manual case with no procedure", catalogWith(strings.Replace(validCase, "doc.md#heading", "doc.md", 1)), "is not of the form <doc>#<anchor>"},
		{"a manual case marked implemented", catalogWith(validCase + "    implemented: true\n"), "implemented is for scripted cases"},
		{"a scripted case in the manual lane", catalogWith(strings.Replace(scripted, "lane: go", "lane: manual", 1)), "needs lane go or binary"},
		{"a scripted live case", catalogWith(strings.Replace(scripted, "tier: deterministic", "tier: live", 1)), "a live case is manual"},
		{"a malformed runner", catalogWith(strings.Replace(scripted, "pkg:TestThing", "TestThing", 1)), "is not of the form <package dir>:<TestName>"},
		{"an unimplemented case naming a runner", catalogWith(strings.Replace(scripted, "implemented: true", "implemented: false", 1)), "names no runner yet"},
		{"a runner named twice", catalogWith(scripted + strings.Replace(scripted, "AA-001", "AA-002", 1)), "already named by AA-001"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCatalog([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			problems := c.Validate("") // no repository: fields only
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %v, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0].String(), tt.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, tt.want)
			}
			if !problems[0].Gating {
				t.Errorf("the problem does not gate: %v", problems[0])
			}
		})
	}
}

func TestCatalogRefusesUnknownFields(t *testing.T) {
	if _, err := ParseCatalog([]byte(catalogWith(validCase + "    steps: do this\n"))); err == nil {
		t.Fatal("a case with a field the schema does not know was accepted")
	}
}

// A problem with an advisory case is reported but does not gate; one with a
// required case, or with a case whose gate is unreadable, does.
func TestCatalogProblemGating(t *testing.T) {
	advisory := strings.NewReplacer("gate: required", "gate: advisory", "tier: deterministic", "tier: nightly").Replace(validCase)
	c, _ := ParseCatalog([]byte(catalogWith(advisory)))
	if p := c.Validate(""); len(p) != 1 || p[0].Gating {
		t.Errorf("an advisory case's problem: %+v, want one that does not gate", p)
	}
	unreadable := strings.Replace(validCase, "gate: required", "gate: perhaps", 1)
	c, _ = ParseCatalog([]byte(catalogWith(unreadable)))
	if p := c.Validate(""); len(p) != 1 || !p[0].Gating {
		t.Errorf("a case with an unreadable gate: %+v, want one gating problem", p)
	}
}

// Against a repository: the runner must be a real test, the procedure a real
// heading, and every scenario test must belong to a case.
func TestCatalogValidationReadsTheRepository(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/thing_test.go", "package pkg\n\nfunc TestThing(t *testing.T) {}\n\nfunc TestOrphan(t *testing.T) {}\n")
	write(filepath.Join(filepath.Dir(CatalogPath), "doc.md"), "# Doc\n\n## A Heading, with: punctuation\n")

	scripted := func(id, runner string) string {
		return strings.NewReplacer("AA-001", id, "driver: manual", "driver: scripted", "lane: manual", "lane: go",
			"procedure: doc.md#heading", "runner: "+runner+"\n    implemented: true").Replace(validCase)
	}
	manual := func(procedure string) string {
		return strings.NewReplacer("AA-001", "MM-001", "doc.md#heading", procedure).Replace(validCase)
	}
	tests := []struct {
		name string
		yaml string
		dirs []string
		want string
	}{
		{"all present", catalogWith(scripted("AA-001", "pkg:TestThing") + manual("doc.md#a-heading-with-punctuation")), nil, ""},
		{"a runner that is no test", catalogWith(scripted("AA-001", "pkg:TestMissing")), nil, "no such test in pkg"},
		{"a runner in no package", catalogWith(scripted("AA-001", "nowhere:TestThing")), nil, "no test files"},
		{"a procedure with no such heading", catalogWith(manual("doc.md#other")), nil, "has no heading with that anchor"},
		{"a procedure in no such doc", catalogWith(manual("gone.md#x")), nil, "gone.md"},
		{"a scenario test no case names", catalogWith(scripted("AA-001", "pkg:TestThing")), []string{"pkg"}, "test TestOrphan in pkg is named by no case"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCatalog([]byte(tt.yaml))
			if err != nil {
				t.Fatal(err)
			}
			problems := c.Validate(root, tt.dirs...)
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %v, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0].String(), tt.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, tt.want)
			}
		})
	}
}

// testTarget is the candidate the gate tests judge.
var testTarget = Target{Version: "v1.0.0-rc.4", Platform: "linux/amd64"}

func gateCatalog(gate string) *Catalog {
	return &Catalog{Cases: []Case{{ID: "AA-001", Gate: gate}}}
}

// The gate: execution, severity and disposition are judged separately, and
// nothing but a pass is a pass.
func TestGate(t *testing.T) {
	steve := &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12", Reference: "the release record, Waivers"}
	tests := []struct {
		name   string
		result *Result // nil: no result recorded
		triage *Triage
		blocks string // a fragment of the blocking reason; "" when publishable
	}{
		{"pass", &Result{Execution: Pass}, nil, ""},
		{"no result is not run, untriaged", nil, nil, "not run, and not triaged"},
		{"fail, untriaged", &Result{Execution: Fail}, nil, "fail, and not triaged"},
		{"blocked by the environment is not a pass", &Result{Execution: Blocked, EnvironmentLimitation: "no network"}, nil, "blocked, and not triaged"},
		{"blocked, triaged LOW, still listed but does not block", &Result{Execution: Blocked}, &Triage{Severity: SeverityLow, Disposition: DispositionDefer}, ""},
		{"blocked at HIGH blocks", &Result{Execution: Blocked}, &Triage{Severity: SeverityHigh, Disposition: DispositionDefer}, "blocked at HIGH"},
		{"not applicable, by the result's own say-so", &Result{Execution: NotApplicable, Detail: "no such account"}, nil, "the catalog does not make this case inapplicable"},
		{"an unknown execution", &Result{Execution: "passed"}, nil, `execution "passed" is not one of`},
		{"BLOCKER blocks", &Result{Execution: Fail}, &Triage{Severity: SeverityBlocker, Disposition: DispositionFix}, "fail at BLOCKER"},
		{"HIGH blocks, even waived by Steve", &Result{Execution: Fail}, &Triage{Severity: SeverityHigh, Disposition: DispositionWaiver, Waiver: steve}, "cannot be waived"},
		{"MEDIUM to be fixed still blocks", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionFix}, "needs Steve's written waiver"},
		{"MEDIUM deferred still blocks", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionDefer}, "needs Steve's written waiver"},
		{"MEDIUM with Steve's waiver", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionWaiver, Waiver: steve}, ""},
		{"MEDIUM waived by someone else", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionWaiver, Waiver: &Waiver{ApprovedBy: "Claude", Date: "2026-10-12", Reference: "chat"}}, "only Steve approves"},
		{"MEDIUM waived with no reference", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionWaiver, Waiver: &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12"}}, "lacks its date or the reference"},
		{"a waiver disposition with no waiver", &Result{Execution: Fail}, &Triage{Severity: SeverityMedium, Disposition: DispositionWaiver}, "no waiver recorded"},
		{"a waiver on another disposition", &Result{Execution: Fail}, &Triage{Severity: SeverityLow, Disposition: DispositionDefer, Waiver: steve}, "a waiver is recorded but the disposition"},
		{"LOW does not block", &Result{Execution: Fail}, &Triage{Severity: SeverityLow, Disposition: DispositionDefer}, ""},
		{"NIT does not block", &Result{Execution: Fail}, &Triage{Severity: SeverityNit, Disposition: DispositionFix}, ""},
		{"investigate blocks at LOW", &Result{Execution: Fail}, &Triage{Severity: SeverityLow, Disposition: DispositionInvestigate}, "still under investigation"},
		{"an unknown severity", &Result{Execution: Fail}, &Triage{Severity: "MAJOR", Disposition: DispositionFix}, `severity "MAJOR"`},
		{"an unknown disposition", &Result{Execution: Fail}, &Triage{Severity: SeverityLow, Disposition: "ignore"}, `disposition "ignore"`},
		{"a passing case that is triaged", &Result{Execution: Pass}, &Triage{Severity: SeverityLow, Disposition: DispositionDefer}, "never triaged into a pass"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var results []Result
			if tt.result != nil {
				tt.result.CaseID = "AA-001"
				results = append(results, *tt.result)
			}
			var triage []Triage
			if tt.triage != nil {
				tt.triage.CaseID = "AA-001"
				triage = append(triage, *tt.triage)
			}
			v := Evaluate(gateCatalog(GateRequired), nil, results, triage, testTarget)
			if len(v.Rows) != 1 {
				t.Fatalf("%d rows, want exactly one for the one case", len(v.Rows))
			}
			if tt.blocks == "" {
				if !v.Publishable() {
					t.Fatalf("blocked: %v", v.Blocking)
				}
				return
			}
			if v.Publishable() || !strings.Contains(strings.Join(v.Blocking, "\n"), tt.blocks) {
				t.Fatalf("blocking = %v, want a reason containing %q", v.Blocking, tt.blocks)
			}
			// The same finding on an advisory case is shown and does not block.
			if adv := Evaluate(gateCatalog(GateAdvisory), nil, results, triage, testTarget); !adv.Publishable() || adv.Rows[0].Blocks == "" {
				t.Errorf("on an advisory case: blocking %v, row reason %q; want it shown and not blocking", adv.Blocking, adv.Rows[0].Blocks)
			}
		})
	}
}

func TestGateFailsClosed(t *testing.T) {
	c := gateCatalog(GateRequired)
	pass := []Result{{CaseID: "AA-001", Execution: Pass}}
	tests := []struct {
		name     string
		problems []Problem
		results  []Result
		triage   []Triage
		blockers []string
		want     string
	}{
		{"a gating catalog problem", []Problem{{CaseID: "AA-001", Gating: true, Text: "tier is missing"}}, pass, nil, nil, "catalog invalid: AA-001: tier is missing"},
		{"a result for no case", nil, append(pass, Result{CaseID: "ZZ-999", Execution: Pass}), nil, nil, "not a catalog case"},
		{"two results for one case", nil, append(pass, pass...), nil, nil, "more than one result"},
		{"triage for no case", nil, pass, []Triage{{CaseID: "ZZ-999"}}, nil, "not a catalog case"},
		{"the caller's own blocker", nil, pass, nil, []string{"a development run"}, "a development run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Evaluate(c, tt.problems, tt.results, tt.triage, testTarget, tt.blockers...)
			if v.Publishable() || !strings.Contains(strings.Join(v.Blocking, "\n"), tt.want) {
				t.Fatalf("blocking = %v, want a reason containing %q", v.Blocking, tt.want)
			}
		})
	}
	// A problem that does not gate is not a reason to block.
	if v := Evaluate(c, []Problem{{CaseID: "BB-001", Text: "advisory"}}, pass, nil, testTarget); !v.Publishable() {
		t.Errorf("a non-gating problem blocked: %v", v.Blocking)
	}
}

// A skipped test is blocked, never passed; a test that did not report was
// not run.
func TestResultOfARun(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		status string
		failed bool
		want   Execution
	}{
		{"pass", false, Pass}, {"fail", true, Fail}, {"skip", false, Blocked}, {"", false, NotRun},
		{"pass", true, Blocked}, {"", true, Blocked}, {"skip", true, Blocked},
	} {
		r := TestRun{Outcome: TestOutcome{Status: tt.status, Output: "--- SKIP: no tmux\n"}, ProcessFailed: tt.failed}.Result("AA-001", "runner", at)
		if r.Execution != tt.want {
			t.Errorf("status %q, process failed %v: %q, want %q", tt.status, tt.failed, r.Execution, tt.want)
		}
		if tt.status == "skip" && !strings.Contains(r.EnvironmentLimitation, "no tmux") {
			t.Errorf("a skip does not carry its reason: %+v", r)
		}
	}
}

func TestRedactor(t *testing.T) {
	home, _ := os.UserHomeDir()
	r := NewRedactor("s3cret-playlist-id")
	in := fmt.Sprintf(`path %s/Music/x.mp3
{"access_token":"ya29.abcDEF123456","refresh_token": "1//0gabcdef"}
Authorization: Bearer abcdef.123456.xyz
Cookie: SAPISID=abc/def; other
client_secret = GOCSPX-abcdef
playlist s3cret-playlist-id
title: First Song`, home)
	out := string(r.Redact([]byte(in)))
	for _, secret := range []string{home, "ya29.abcDEF123456", "1//0gabcdef", "abcdef.123456.xyz", "abc/def", "GOCSPX-abcdef", "s3cret-playlist-id"} {
		if secret != "" && strings.Contains(out, secret) {
			t.Errorf("%q survived redaction:\n%s", secret, out)
		}
	}
	for _, kept := range []string{"<home>/Music/x.mp3", "title: First Song", "access_token", "<redacted>"} {
		if !strings.Contains(out, kept) {
			t.Errorf("redaction lost %q:\n%s", kept, out)
		}
	}
}

func TestEvidenceRootMustBeDurableAndOutsideTheRepository(t *testing.T) {
	repo := repoRoot(t)
	for _, dir := range []string{"/tmp/evidence", os.TempDir(), filepath.Join(os.TempDir(), "a", "b"), "/var/tmp/x", repo, filepath.Join(repo, "docs")} {
		if err := CheckEvidenceRoot(dir, repo); err == nil {
			t.Errorf("%s was accepted as an evidence directory", dir)
		}
	}
	def, err := EvidenceRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(def, filepath.Join(appdir.Name, "acceptance")) {
		t.Errorf("the default evidence directory is %s", def)
	}
	if err := CheckEvidenceRoot("/home/someone/.local/share/"+appdir.Name+"/acceptance", repo); err != nil {
		t.Errorf("a data directory was refused: %v", err)
	}
}

// Sealing leaves one redacted archive and its checksum, and nothing else.
func TestBundleSeal(t *testing.T) {
	root := t.TempDir() // a stand-in root: the location rule is CheckEvidenceRoot's
	b, err := NewBundle(root, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b.Dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Dir, "captures", "log.txt"), []byte("access_token=abc123456 ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal(NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if sum, _ := FileSHA256(sealed.Archive); sum != sealed.SHA256 {
		t.Errorf("the recorded checksum %s is not the archive's %s", sealed.SHA256, sum)
	}
	if line, _ := os.ReadFile(sealed.Archive + ".sha256"); !strings.HasPrefix(string(line), sealed.SHA256+"  run-1.tar.gz") {
		t.Errorf("checksum file reads %q", line)
	}
	if _, err := os.Stat(b.Dir); !os.IsNotExist(err) {
		t.Errorf("the unsealed directory is still there (stat: %v)", err)
	}
	listing, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/captures/log.txt").Output()
	if err != nil {
		t.Skipf("no tar to read the archive back with: %v", err)
	}
	if strings.Contains(string(listing), "abc123456") || !strings.Contains(string(listing), "<redacted>") {
		t.Errorf("the archived file is not redacted: %q", listing)
	}
	if _, err := NewBundle(root, "run-1.tar.gz/../run-2"); err == nil {
		// Not a security boundary (the runner names the run), but a second
		// run must not land on the first.
		t.Log("a nested run ID was accepted")
	}
}

// fixture describes a synthetic release archive; the zero value of a field
// means "as a real release has it".
type fixture struct {
	version        string // the archive's version, and what everything else defaults to
	reports        string // the version the binary reports
	commit         string // the commit everything names by default
	binaryCommit   string // the commit the binary reports; "none" for a binary that reports none
	manifestCommit string // the commit the manifest names
	manifest       string // the manifest's whole text; "none" for an archive without one
	manifestBinary string // the binary checksum the manifest gives
	// change edits the manifest's fields before it is written; a field set
	// to "" is left out.
	change func(fields map[string]string)
	script bool // the "binary" is a shell script: not a Go executable
}

// fixtureBinary builds a real Go executable that prints line, as the
// player's --version does: release acceptance reads its build information.
func fixtureBinary(t *testing.T, line string) []byte {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.24\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport \"fmt\"\n\nvar line string\n\nfunc main() { fmt.Println(line) }\n"), 0o600)
	cmd := exec.Command("go", "build", "-ldflags", "-X 'main.line="+line+"'", "-o", "fixture", ".")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the fixture binary: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// releaseFixture builds a release archive whose binary reports a version
// and a commit, with its manifest, and the SHA256SUMS that lists the archive.
func releaseFixture(t *testing.T, f fixture) (archive, sums string) {
	t.Helper()
	or := func(v, def string) string {
		if v == "" {
			return def
		}
		return v
	}
	dir := t.TempDir()
	stem := fmt.Sprintf("%s-%s-%s-%s", appdir.Name, strings.TrimPrefix(f.version, "v"), runtime.GOOS, runtime.GOARCH)
	line := fmt.Sprintf("%s version %s", appdir.Name, or(f.reports, f.version))
	if c := or(f.binaryCommit, f.commit); c != "none" {
		line += " (commit " + c + ")"
	}
	binary := []byte("#!/bin/sh\necho '" + line + "'\n")
	if !f.script {
		binary = fixtureBinary(t, line)
	}
	members := map[string][]byte{stem + "/README.md": []byte("readme"), stem + "/" + appdir.Name: binary}
	if f.manifest != "none" {
		fields := map[string]string{
			"name": appdir.Name, "version": f.version, "source_sha": or(f.manifestCommit, f.commit),
			"goos": runtime.GOOS, "goarch": runtime.GOARCH, "go_version": runtime.Version(),
			"binary_sha256": or(f.manifestBinary, fmt.Sprintf("%x", sha256.Sum256(binary))),
		}
		if f.change != nil {
			f.change(fields)
		}
		for key, value := range fields {
			if value == "" {
				delete(fields, key)
			}
		}
		text, _ := json.Marshal(fields)
		members[stem+"/"+ManifestName] = []byte(or(f.manifest, string(text)))
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	tw.Close()
	gz.Close()
	archive = filepath.Join(dir, stem+".tar.gz")
	if err := os.WriteFile(archive, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	sum, _ := FileSHA256(archive)
	sums = filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte(sum+"  "+filepath.Base(archive)+"\n"+strings.Repeat("0", 64)+"  other.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return archive, sums
}

// A release run takes the archive's binary or nothing. The expected release
// commit, the manifest's and the binary's own must be one and the same, and
// every way the artifact can fail to be the candidate stops the run.
func TestVerifyRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's binary is a shell script")
	}
	repo := repoRoot(t)
	head, err := git(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	other := strings.Repeat("b", 40)
	const version = "v9.9.9-rc.1" // no such tag here
	archive, sums := releaseFixture(t, fixture{version: version, commit: head})

	t.Run("the candidate", func(t *testing.T) {
		id, err := VerifyRelease(ReleaseInput{Archive: archive, Sums: sums, Version: version, SourceSHA: head}, repo, filepath.Join(t.TempDir(), "x"))
		if err != nil {
			t.Fatal(err)
		}
		binarySum, _ := FileSHA256(id.BinaryPath)
		archiveSum, _ := FileSHA256(archive)
		if id.Mode != ModeRelease || id.ReportedVersion != version || id.BinarySHA256 != binarySum || id.ArchiveSHA256 != archiveSum {
			t.Errorf("identity = %+v", id)
		}
		if id.SourceSHA != head || id.ManifestSourceSHA != head || id.BinarySourceSHA != head {
			t.Errorf("commits: expected %s, manifest %s, binary %s; want all three %s", id.SourceSHA, id.ManifestSourceSHA, id.BinarySourceSHA, head)
		}
		if id.ReleaseGoVersion != runtime.Version() {
			t.Errorf("the release's Go version is recorded as %q, want %q", id.ReleaseGoVersion, runtime.Version())
		}
		if !strings.Contains(id.SourceCheck, "manifest and the binary itself agree") || !strings.Contains(id.SourceCheck, "no tag") {
			t.Errorf("the source check reads %q", id.SourceCheck)
		}
		// Only the binary and the manifest were taken out of the archive.
		if entries, _ := os.ReadDir(filepath.Dir(id.BinaryPath)); len(entries) != 2 {
			t.Errorf("%d files were extracted, want the binary and the manifest", len(entries))
		}
	})

	tampered := filepath.Join(t.TempDir(), filepath.Base(archive))
	data, _ := os.ReadFile(archive)
	os.WriteFile(tampered, append(data, 0), 0o600)
	renamed := filepath.Join(t.TempDir(), "something-else.tar.gz")
	os.WriteFile(renamed, data, 0o600)
	unlisted := filepath.Join(t.TempDir(), "SHA256SUMS")
	os.WriteFile(unlisted, []byte(strings.Repeat("0", 64)+"  other.txt\n"), 0o600)
	in := func(a, s string) ReleaseInput {
		return ReleaseInput{Archive: a, Sums: s, Version: version, SourceSHA: head}
	}
	of := func(f fixture) ReleaseInput {
		f.version = version
		if f.commit == "" {
			f.commit = head
		}
		return in(releaseFixture(t, f))
	}

	for _, tt := range []struct {
		name string
		in   ReleaseInput
		want string
	}{
		{"nothing given", ReleaseInput{}, "needs the archive"},
		{"an archive that is not the listed one", in(tampered, sums), "does not match SHA256SUMS"},
		{"an archive SHA256SUMS does not list", in(archive, unlisted), "does not list"},
		{"an archive of another name", in(renamed, sums), "the archive of " + version},
		{"an archive of another version", ReleaseInput{Archive: archive, Sums: sums, Version: "v9.9.9-rc.2", SourceSHA: head}, "the archive of v9.9.9-rc.2"},
		{"dev as the expected version", ReleaseInput{Archive: archive, Sums: sums, Version: "dev", SourceSHA: head}, "is not a release version"},
		{"a short source commit", ReleaseInput{Archive: archive, Sums: sums, Version: version, SourceSHA: head[:12]}, "not a full 40-character SHA"},
		// The binary.
		{"a binary reporting another version", of(fixture{reports: "v9.9.9-rc.2"}), `reports version "v9.9.9-rc.2"`},
		{"a binary reporting dev", of(fixture{reports: "dev"}), `reports version "dev"`},
		{"a binary reporting no commit", of(fixture{binaryCommit: "none"}), "does not report the source commit"},
		{"a binary reporting another commit", of(fixture{binaryCommit: other}), "binary reports source commit " + other},
		// The manifest.
		{"an archive with no manifest", of(fixture{manifest: "none"}), "holds no " + appdir.Name},
		{"a manifest that is not JSON", of(fixture{manifest: "version = oops"}), "is unreadable"},
		{"a manifest with no source commit", of(fixture{change: func(m map[string]string) { m["source_sha"] = "" }}), "has no source_sha"},
		{"a manifest with no binary checksum", of(fixture{change: func(m map[string]string) { m["binary_sha256"] = "" }}), "has no binary_sha256"},
		{"a manifest with no version", of(fixture{change: func(m map[string]string) { m["version"] = "" }}), "has no version"},
		// P1: the build's identity is required, and must be this artifact's.
		{"a manifest with no name", of(fixture{change: func(m map[string]string) { m["name"] = "" }}), "has no name"},
		{"a manifest with no goos", of(fixture{change: func(m map[string]string) { m["goos"] = "" }}), "has no goos"},
		{"a manifest with no goarch", of(fixture{change: func(m map[string]string) { m["goarch"] = "" }}), "has no goarch"},
		{"a manifest with no go_version", of(fixture{change: func(m map[string]string) { m["go_version"] = "" }}), "has no go_version"},
		{"a manifest with only the three hashes and names", of(fixture{change: func(m map[string]string) { m["name"], m["goos"], m["goarch"], m["go_version"] = "", "", "", "" }}), "has no name"},
		{"a manifest for another product", of(fixture{change: func(m map[string]string) { m["name"] = "other-player" }}), `manifest is for "other-player"`},
		{"a manifest for another operating system", of(fixture{change: func(m map[string]string) { m["goos"] = "windows" }}), "manifest is for windows/"},
		{"a manifest for another architecture", of(fixture{change: func(m map[string]string) { m["goarch"] = "riscv64" }}), "/riscv64, and the archive"},
		{"a manifest with no real Go version", of(fixture{change: func(m map[string]string) { m["go_version"] = "not-a-go-version" }}), "is not a Go version"},
		{"a manifest naming another Go than the binary's", of(fixture{change: func(m map[string]string) { m["go_version"] = "go1.0.1" }}), "built with go1.0.1, and the binary says"},
		{"a binary that is not a Go executable", of(fixture{script: true}), "carries no readable build information"},
		{"a manifest with a short commit", of(fixture{manifestCommit: head[:12]}), "not a full 40-character SHA"},
		{"a manifest naming another commit", of(fixture{manifestCommit: other}), "manifest names source commit " + other},
		{"a manifest for another version", of(fixture{change: func(m map[string]string) { m["version"] = "v9.9.9-rc.2" }}), `manifest is for version "v9.9.9-rc.2"`},
		{"a manifest with another binary's checksum", of(fixture{manifestBinary: strings.Repeat("0", 64)}), "manifest gives the binary's checksum as"},
		// Agreement among the three, each against the others.
		{"manifest and binary agree with each other and not with the expected commit",
			ReleaseInput{Archive: archive, Sums: sums, Version: version, SourceSHA: other}, "expected release commit is " + other},
		{"a source commit not in the checkout", func() ReleaseInput {
			a, s := releaseFixture(t, fixture{version: version, commit: other})
			return ReleaseInput{Archive: a, Sums: s, Version: version, SourceSHA: other}
		}(), "is not in this checkout"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			id, err := VerifyRelease(tt.in, repo, filepath.Join(t.TempDir(), "x"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one containing %q (identity %+v)", err, tt.want, id)
			}
		})
	}
}

// A local tag of the version must point to the supplied source commit.
func TestVerifyReleaseChecksTheLocalTag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's binary is a shell script")
	}
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "one")
	first := run("rev-parse", "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "two")
	second := run("rev-parse", "HEAD")
	const version = "v9.9.9-rc.1"
	run("tag", version, first)
	archive, sums := releaseFixture(t, fixture{version: version, commit: first})
	id, err := VerifyRelease(ReleaseInput{Archive: archive, Sums: sums, Version: version, SourceSHA: first}, repo, filepath.Join(t.TempDir(), "x"))
	if err != nil || !strings.Contains(id.SourceCheck, "points to it too") {
		t.Fatalf("the tagged commit: %v, check %q", err, id.SourceCheck)
	}
	// Expected commit, manifest and binary all agree on the second commit,
	// and the tag says the first: a contradiction, and the run stops.
	archive, sums = releaseFixture(t, fixture{version: version, commit: second})
	_, err = VerifyRelease(ReleaseInput{Archive: archive, Sums: sums, Version: version, SourceSHA: second}, repo, filepath.Join(t.TempDir(), "x"))
	if err == nil || !strings.Contains(err.Error(), "the local tag "+version+" is commit "+first) {
		t.Fatalf("a commit the tag does not point to: %v", err)
	}
}

func TestSummaryKeepsTheJudgementsApart(t *testing.T) {
	c := &Catalog{Cases: []Case{
		{ID: "AA-001", Title: "Passes", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
		{ID: "AA-002", Title: "Fails | badly", Lane: LaneGo, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
		{ID: "AA-003", Title: "Cannot run", Lane: LaneManual, Tier: TierLive, Driver: DriverManual, Gate: GateRequired},
		{ID: "AA-004", Title: "No runner", Lane: LaneBinary, Driver: DriverScripted, Gate: GateRequired},
		{ID: "AA-005", Title: "Does not apply", Lane: LaneManual, Tier: TierDeterministic, Driver: DriverManual, Gate: GateRequired,
			Inapplicable: []Inapplicability{{Releases: []string{testTarget.Version}, Reason: "no such account", CoveredBy: []string{"AA-001"}}}},
	}}
	results := []Result{
		{CaseID: "AA-001", Execution: Pass},
		{CaseID: "AA-002", Execution: Fail, Detail: "the row was missing"},
		{CaseID: "AA-003", Execution: Blocked, EnvironmentLimitation: "Spotify was down"},
		{CaseID: "AA-005", Execution: NotApplicable, Detail: "no such account"},
	}
	triage := []Triage{
		{CaseID: "AA-002", Severity: SeverityMedium, Disposition: DispositionWaiver, Waiver: &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12", Reference: "release record"}},
		{CaseID: "AA-003", Severity: SeverityHigh, Disposition: DispositionInvestigate},
	}
	v := Evaluate(c, nil, results, triage, testTarget)
	dev := Summary(Identity{Mode: ModeDevelopment, ReportedVersion: "dev"}, c, nil, v, Sealed{}, NewRedactor())
	for _, want := range []string{
		"not release evidence",
		"**5 cases:** 1 pass, 1 fail, 1 blocked, 1 not run, 1 not applicable.",
		"| AA-002 Fails \\| badly | required | fail | MEDIUM | accepted waiver | no |  |",
		"| AA-003 Cannot run | required | blocked | HIGH | investigate | yes | blocked at HIGH, still under investigation |",
		"| AA-004 No runner | required | not run | — | — | yes | not run, and not triaged (no severity or disposition) |",
		"- AA-005: no such account",
		"- AA-003: Spotify was down",
		"**Scripted cases with no runner yet** (they cannot pass): AA-004.",
		"approved by Steve on 2026-10-12",
		"**Gate: publication is blocked.**",
		"- 2 release-gating cases block it",
		"No evidence bundle was sealed",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("the summary lacks %q:\n%s", want, dev)
		}
	}
	release := Summary(Identity{Mode: ModeRelease, ExpectedVersion: "v1", ReportedVersion: "v1", ArchiveName: "a.tar.gz"}, c, nil,
		Evaluate(c, nil, []Result{{CaseID: "AA-001", Execution: Pass}}, nil, testTarget), Sealed{Archive: "/home/x/run.tar.gz", SHA256: "abc"}, NewRedactor())
	if strings.Contains(release, "not release evidence") || !strings.Contains(release, "`run.tar.gz`, SHA-256 `abc`") || strings.Contains(release, "/home/x") {
		t.Errorf("the release summary:\n%s", release)
	}
}

// H5: ordinary credential formats are removed whole.
func TestRedactorCredentialFormats(t *testing.T) {
	r := NewRedactor()
	for _, tt := range []struct{ name, in, secret string }{
		{"a JSON cookie collection", `{"cookie":"SAPISID=alpha-secret; __Secure-3PAPISID=beta-secret"}`, "beta-secret"},
		{"a JSON cookie collection, first", `{"cookie":"SAPISID=alpha-secret; __Secure-3PAPISID=beta-secret"}`, "alpha-secret"},
		{"a Cookie header", "Cookie: a=secret-one; b=secret-two", "secret-two"},
		{"a TOML password in single quotes", "password = 'secret-three'", "secret-three"},
		{"a JSON password with a space", `{"password":"secret four"}`, "four"},
		{"URL user information", "https://user:secret-five@example.invalid/x", "secret-five"},
		{"a token in a URL query", "https://example.invalid/?a=1&token=secret-six&b=2", "secret-six"},
		{"a key=value token", "access_token=secret-seven\n", "secret-seven"},
		{"a YAML token", "refresh_token: secret-eight", "secret-eight"},
		{"a TOML client secret", `client_secret = "GOCSPX-secret-nine"`, "secret-nine"},
		{"a bearer token", "Authorization: Bearer secret.ten.token", "secret.ten.token"},
		{"a basic credential", "authorization: Basic c2VjcmV0LWVsZXZlbg==", "c2VjcmV0LWVsZXZlbg"},
		{"a JSON bearer header", `{"Authorization":"Bearer secret-twelve-token"}`, "secret-twelve-token"},
		{"a cookie pair in prose", "sent sp_dc=secret-thirteen with the request", "secret-thirteen"},
		{"a Set-Cookie header", "Set-Cookie: SID=secret-fourteen; Path=/; HttpOnly", "secret-fourteen"},
		{"an api key header", "X-Api-Key: secret-fifteen", "secret-fifteen"},
		{"a secret with an escaped quote", `{"password":"secret\"sixteen"}`, "sixteen"},
		{"a cookies file line", "cookies = 'SID=secret-seventeen; HSID=secret-eighteen'", "secret-eighteen"},
	} {
		if out := string(r.Redact([]byte(tt.in))); strings.Contains(out, tt.secret) {
			t.Errorf("%s: %q survived in %q", tt.name, tt.secret, out)
		}
	}
	// What is not a secret stays readable.
	for _, kept := range []string{"title: First Song", `{"state":"playing","track":{"title":"Token Song"}}`, "https://example.invalid/watch?v=abc123", "volume = 0"} {
		if out := string(r.Redact([]byte(kept))); out != kept {
			t.Errorf("redaction changed %q to %q", kept, out)
		}
	}
}

// H2: a pass does not carry an environment limitation. An execution the
// environment undermined is blocked or failed; it is not a pass with a note.
func TestGateRefusesAPassWithAnEnvironmentLimitation(t *testing.T) {
	v := Evaluate(gateCatalog(GateRequired), nil, []Result{{CaseID: "AA-001", Execution: Pass, EnvironmentLimitation: "the network was not isolated"}}, nil, testTarget)
	if v.Publishable() || !strings.Contains(strings.Join(v.Blocking, "\n"), "environment limitation") {
		t.Fatalf("blocking = %v, want the pass refused for its environment limitation", v.Blocking)
	}
}

// M10: a symbolic link does not lead evidence into temporary storage or the
// repository, whether it is the directory itself or one of its ancestors.
func TestEvidenceRootResolvesSymbolicLinks(t *testing.T) {
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skip("no cache directory to build the links in")
	}
	base, err := os.MkdirTemp(cache, "evidence-links")
	if err != nil {
		t.Skipf("cannot write to the cache directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	repo := repoRoot(t)
	if err := CheckEvidenceRoot(filepath.Join(base, "plain"), repo); err != nil {
		t.Fatalf("a plain directory outside temporary storage was refused: %v", err)
	}
	for name, target := range map[string]string{"to-tmp": os.TempDir(), "to-repo": filepath.Join(repo, "docs")} {
		link := filepath.Join(base, name)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{link, filepath.Join(link, "not", "there", "yet")} {
			if err := CheckEvidenceRoot(dir, repo); err == nil {
				t.Errorf("%s, which leads to %s, was accepted as an evidence directory", dir, target)
			}
		}
	}
}

// M11: a second YAML document is not silently ignored.
func TestExtraYAMLDocumentsAreRefused(t *testing.T) {
	if _, err := ParseCatalog([]byte(catalogWith(validCase) + "---\ncases: []\n")); err == nil {
		t.Error("a catalog with a second document was accepted")
	}
	dir := t.TempDir()
	results := filepath.Join(dir, "results.yaml")
	os.WriteFile(results, []byte("results:\n  - case_id: AA-001\n    execution: fail\n---\nresults:\n  - case_id: AA-001\n    execution: pass\n"), 0o600)
	if _, err := LoadManual(results); err == nil {
		t.Error("manual results with a second document were accepted")
	}
	triage := filepath.Join(dir, "triage.yaml")
	os.WriteFile(triage, []byte("triage: []\n---\ntriage:\n  - case_id: AA-001\n    severity: LOW\n    disposition: defer\n"), 0o600)
	if _, err := LoadTriage(triage); err == nil {
		t.Error("a triage file with a second document was accepted")
	}
}

// H1: a result cannot make its own case inapplicable. Only the catalog can,
// for the release or platform it names, and only with another required case
// that supplies the coverage and was itself executed.
func TestGateInapplicability(t *testing.T) {
	covered := func(in ...Inapplicability) *Catalog {
		return &Catalog{Cases: []Case{
			{ID: "LIVE-SPOT-001", Gate: GateRequired, Inapplicable: in},
			{ID: "LIVE-SPOT-002", Gate: GateRequired},
		}}
	}
	na := Result{CaseID: "LIVE-SPOT-001", Execution: NotApplicable, Detail: "no Spotify account"}
	pass := Result{CaseID: "LIVE-SPOT-002", Execution: Pass}
	thisRelease := Inapplicability{Releases: []string{testTarget.Version}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}
	tests := []struct {
		name    string
		catalog *Catalog
		results []Result
		blocks  string
	}{
		{"the catalog says nothing: the case applies", covered(), []Result{na, pass}, "the catalog does not make this case inapplicable for v1.0.0-rc.4 on linux/amd64"},
		{"inapplicable for this release, covered and the covering case ran", covered(thisRelease), []Result{na, pass}, ""},
		{"inapplicable for this platform", covered(Inapplicability{Platforms: []string{"linux/amd64"}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}), []Result{na, pass}, ""},
		{"inapplicable for another release only", covered(Inapplicability{Releases: []string{"v1.0.0"}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}), []Result{na, pass}, "does not make this case inapplicable"},
		{"inapplicable for another platform only", covered(Inapplicability{Platforms: []string{"darwin/arm64"}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}), []Result{na, pass}, "does not make this case inapplicable"},
		{"this release, but another platform", covered(Inapplicability{Releases: []string{testTarget.Version}, Platforms: []string{"darwin/arm64"}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}), []Result{na, pass}, "does not make this case inapplicable"},
		{"the covering case has no result", covered(thisRelease), []Result{na}, "was not executed (no result)"},
		{"the covering case is not applicable either", covered(thisRelease), []Result{na, {CaseID: "LIVE-SPOT-002", Execution: NotApplicable, Detail: "x"}}, "was not executed (not applicable)"},
		{"an entry that names nothing applies to nothing", covered(Inapplicability{Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}), []Result{na, pass}, "does not make this case inapplicable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Evaluate(tt.catalog, nil, tt.results, nil, testTarget)
			blocking := strings.Join(v.Blocking, "\n")
			if tt.blocks == "" {
				if strings.Contains(blocking, "LIVE-SPOT-001") {
					t.Fatalf("blocked: %v", v.Blocking)
				}
				return
			}
			if !strings.Contains(blocking, "LIVE-SPOT-001: ") || !strings.Contains(blocking, tt.blocks) {
				t.Fatalf("blocking = %v, want LIVE-SPOT-001 blocked with %q", v.Blocking, tt.blocks)
			}
		})
	}
}

// The catalog's own rules for inapplicability.
func TestCatalogInapplicabilityValidation(t *testing.T) {
	second := strings.Replace(validCase, "AA-001", "AA-002", 1)
	entry := func(body string) string { return validCase + "    inapplicable:\n" + body + second }
	tests := []struct{ name, yaml, want string }{
		{"complete", entry("      - releases: [v1]\n        reason: no such thing there\n        covered_by: [AA-002]\n"), ""},
		{"no release and no platform", entry("      - reason: r\n        covered_by: [AA-002]\n"), "names no release and no platform"},
		{"no reason", entry("      - platforms: [darwin/arm64]\n        covered_by: [AA-002]\n"), "gives no reason"},
		{"no covering case", entry("      - releases: [v1]\n        reason: r\n"), "names no case that supplies the coverage"},
		{"a covering case that does not exist", entry("      - releases: [v1]\n        reason: r\n        covered_by: [ZZ-999]\n"), "is not in the catalog"},
		{"covering itself", entry("      - releases: [v1]\n        reason: r\n        covered_by: [AA-001]\n"), "cannot cover itself"},
		{"an advisory covering case", validCase + "    inapplicable:\n      - releases: [v1]\n        reason: r\n        covered_by: [AA-002]\n" + strings.Replace(second, "gate: required", "gate: advisory", 1), "is advisory"},
		{"a covering case that can itself be inapplicable", validCase + "    inapplicable:\n      - releases: [v1]\n        reason: r\n        covered_by: [AA-002]\n" + second + "    inapplicable:\n      - releases: [v1]\n        reason: r\n        covered_by: [AA-003]\n" + strings.Replace(validCase, "AA-001", "AA-003", 1), "can itself be inapplicable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCatalog([]byte(catalogWith(tt.yaml)))
			if err != nil {
				t.Fatal(err)
			}
			problems := c.Validate("")
			if tt.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %v, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0].String(), tt.want) || !problems[0].Gating {
				t.Fatalf("problems = %v, want one gating problem containing %q", problems, tt.want)
			}
		})
	}
}

// No case of the repository's catalog can be waved away as inapplicable
// today, and in particular no Spotify or YouTube Music case.
func TestRepositoryCatalogDeclaresNoInapplicability(t *testing.T) {
	c, err := LoadCatalog(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var results []Result
	for _, cs := range c.Cases {
		if len(cs.Inapplicable) > 0 {
			t.Errorf("%s declares inapplicability: review it against the rc.4 requirement that Spotify and YouTube Music both be accepted", cs.ID)
		}
		r := Result{CaseID: cs.ID, Execution: Pass}
		if strings.HasPrefix(cs.ID, "LIVE-") {
			r = Result{CaseID: cs.ID, Execution: NotApplicable, Detail: "not needed"}
		}
		results = append(results, r)
	}
	v := Evaluate(c, nil, results, nil, testTarget)
	for _, cs := range c.Cases {
		if strings.HasPrefix(cs.ID, "LIVE-") && !strings.Contains(strings.Join(v.Blocking, "\n"), cs.ID+": recorded as not applicable") {
			t.Errorf("%s was accepted as not applicable", cs.ID)
		}
	}
	if v.Publishable() {
		t.Error("a run with every live case waved away as not applicable would be publishable")
	}
}

// H3: hand-recorded results count only for the exact candidate under test.
func TestManualResultsAreBoundToTheCandidate(t *testing.T) {
	id := Identity{Mode: ModeRelease, ExpectedVersion: "v1.0.0-rc.4", SourceSHA: strings.Repeat("a", 40), ArchiveSHA256: strings.Repeat("b", 64), BinarySHA256: strings.Repeat("c", 64)}
	bound := Candidate{Version: id.ExpectedVersion, SourceSHA: id.SourceSHA, ArchiveSHA256: id.ArchiveSHA256, BinarySHA256: id.BinarySHA256}
	c := &Catalog{Cases: []Case{{ID: "HUMAN-001", Driver: DriverManual}, {ID: "LOCAL-001", Driver: DriverScripted}}}
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	good := Result{CaseID: "HUMAN-001", Execution: Pass, ExecutedBy: "Steve", ExecutedAt: "2026-10-12T15:00:00Z"}
	with := func(change func(*Candidate)) Candidate {
		b := bound
		change(&b)
		return b
	}
	result := func(change func(*Result)) []Result {
		r := good
		change(&r)
		return []Result{r}
	}
	tests := []struct {
		name     string
		id       Identity
		manual   Manual
		accepted int
		want     string
	}{
		{"bound to the candidate", id, Manual{Candidate: bound, Results: []Result{good}}, 1, ""},
		{"an old result, bound: age is not judged", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedAt = "2024-01-01T00:00:00Z" })}, 1, ""},
		{"no binding at all", id, Manual{Results: []Result{good}}, 0, "candidate.version is missing"},
		{"no archive checksum", id, Manual{Candidate: with(func(b *Candidate) { b.ArchiveSHA256 = "" }), Results: []Result{good}}, 0, "candidate.archive_sha256 is missing"},
		{"another version", id, Manual{Candidate: with(func(b *Candidate) { b.Version = "v1.0.0-rc.3" }), Results: []Result{good}}, 0, "candidate.version is v1.0.0-rc.3"},
		{"another source commit", id, Manual{Candidate: with(func(b *Candidate) { b.SourceSHA = strings.Repeat("d", 40) }), Results: []Result{good}}, 0, "candidate.source_sha is dddd"},
		{"another archive", id, Manual{Candidate: with(func(b *Candidate) { b.ArchiveSHA256 = strings.Repeat("d", 64) }), Results: []Result{good}}, 0, "candidate.archive_sha256 is dddd"},
		{"another binary", id, Manual{Candidate: with(func(b *Candidate) { b.BinarySHA256 = strings.Repeat("d", 64) }), Results: []Result{good}}, 0, "candidate.binary_sha256 is dddd"},
		{"given to a development run", Identity{Mode: ModeDevelopment, BinarySHA256: id.BinarySHA256}, Manual{Candidate: bound, Results: []Result{good}}, 0, "development run"},
		{"nobody executed it", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedBy = " " })}, 0, "does not say who executed it"},
		{"no time", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedAt = "" })}, 0, "is not a time"},
		{"a time that is no time", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedAt = "yesterday afternoon" })}, 0, "is not a time"},
		{"a date without a time", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedAt = "2026-10-12" })}, 0, "is not a time"},
		{"a time in the future", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.ExecutedAt = "2026-10-13T00:00:00Z" })}, 0, "has not happened yet"},
		{"for a scripted case", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.CaseID = "LOCAL-001" })}, 0, "recorded by hand for a scripted case"},
		{"for no case", id, Manual{Candidate: bound, Results: result(func(r *Result) { r.CaseID = "ZZ-999" })}, 0, "not a catalog case"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accepted, problems := tt.manual.Accept(tt.id, c, now)
			if len(accepted) != tt.accepted {
				t.Errorf("%d results accepted, want %d (problems %v)", len(accepted), tt.accepted, problems)
			}
			if tt.want == "" {
				if len(problems) != 0 {
					t.Errorf("problems = %v, want none", problems)
				}
				return
			}
			if !strings.Contains(strings.Join(problems, "\n"), tt.want) {
				t.Errorf("problems = %v, want one containing %q", problems, tt.want)
			}
		})
	}
}

// goModule writes a throwaway Go module with one test file, for RunGoTests.
func goModule(t *testing.T, testFile string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module repro\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repro_test.go"), []byte(testFile), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// H4: a pass is a pass only when the test's own process ended well. Each
// case runs alone, so nothing else's failure can explain a failed process
// away, and nothing else's failure can be laid at a passing case's door.
func TestProcessFailureInvalidatesAPass(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	at := time.Now()
	const head = "package repro\nimport (\"os\"; \"testing\")\nvar _ = os.Exit\n"
	const green = "func TestGreen(t *testing.T) {}\n"
	const red = "func TestRed(t *testing.T) { t.Fatal(\"no\") }\n"
	tests := []struct {
		name string
		file string
		want map[string]Execution
	}{
		{"all green", head + green, map[string]Execution{"TestGreen": Pass}},
		{"a skip stays blocked", head + "func TestGreen(t *testing.T) { t.Skip(\"no tmux\") }\n", map[string]Execution{"TestGreen": Blocked}},
		{"an ordinary failure beside a pass: each is what it is", head + green + red, map[string]Execution{"TestGreen": Pass, "TestRed": Fail}},
		{"TestMain exits 1 after the tests", head + "func TestMain(m *testing.M) { m.Run(); os.Exit(1) }\n" + green, map[string]Execution{"TestGreen": Blocked}},
		// The reviewer's case: an ordinary failure AND a TestMain failure.
		// The pass does not survive, and the failure stays a failure.
		{"an ordinary failure and TestMain exits 3", head + "func TestMain(m *testing.M) { m.Run(); os.Exit(3) }\n" + green + red, map[string]Execution{"TestGreen": Blocked, "TestRed": Fail}},
		{"an ordinary failure and TestMain panics", head + "func TestMain(m *testing.M) { m.Run(); panic(\"teardown\") }\n" + green + red, map[string]Execution{"TestGreen": Blocked, "TestRed": Fail}},
		{"TestMain exits 0 although a test failed", head + "func TestMain(m *testing.M) { m.Run(); os.Exit(0) }\n" + green + red, map[string]Execution{"TestGreen": Pass, "TestRed": Fail}},
		// A later test that crashes its process says nothing about an
		// earlier case: that one ran, and ended, in a process of its own.
		{"a pass, and another test that kills its process", head + green + "func TestZCrash(t *testing.T) { os.Exit(3) }\n", map[string]Execution{"TestGreen": Pass, "TestZCrash": Blocked}},
		{"a pass, and another test that panics", head + green + "func TestZPanic(t *testing.T) { panic(\"boom\") }\n", map[string]Execution{"TestGreen": Pass, "TestZPanic": Fail}},
		{"a test that passes and then its cleanup kills the process", head + "func TestGreen(t *testing.T) { t.Cleanup(func() { os.Exit(4) }) }\n", map[string]Execution{"TestGreen": Blocked}},
		{"a test the package does not have", head + green, map[string]Execution{"TestMissing": NotRun}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := goModule(t, tt.file)
			for name, want := range tt.want {
				run, err := RunGoTest(module, ".", name, nil)
				if err != nil {
					t.Fatal(err)
				}
				r := run.Result("AA-001", "runner", at)
				if r.Execution != want {
					t.Errorf("%s: execution %q, want %q (reported %q, process failed %v)\n%s", name, r.Execution, want, run.Outcome.Status, run.ProcessFailed, run.Raw)
				}
				// What the test itself reported is kept, not overwritten.
				if want == Blocked && run.Outcome.Status == "pass" && (!strings.Contains(r.Detail, "reported a pass") || r.EnvironmentLimitation == "") {
					t.Errorf("%s: the invalidated pass lost its story: %+v", name, r)
				}
				if want == Fail && r.Detail == "" {
					t.Errorf("%s: the failure lost its output", name)
				}
			}
		})
	}
	// A package that does not build: no pass, no failure, blocked.
	run, err := RunGoTest(goModule(t, "package repro\nfunc broken( {\n"), ".", "TestGreen", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r := run.Result("AA-001", "runner", at); r.Execution != Blocked {
		t.Errorf("a package that does not build gives %q, want blocked", r.Execution)
	}
}

func TestCheckIsolated(t *testing.T) {
	const host = "net:[4026531833]"
	lo := "Inter-|   Receive\n face |bytes\n    lo:       0       0\n"
	tests := []struct{ name, ns, devices, want string }{
		{"a new namespace with loopback only", "net:[4026533644]", lo, ""},
		{"the host's namespace", host, lo, "host's network namespace"},
		{"nothing recorded", "", lo, "no network namespace was recorded"},
		{"an interface besides loopback", "net:[4026533644]", lo + "  eth0:    1    1\n", "has the interface eth0"},
		{"interfaces unreadable", "net:[4026533644]", "", "could not be read"},
	}
	for _, tt := range tests {
		err := checkIsolated(tt.ns, host, tt.devices)
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%s: %v, want %q", tt.name, err, tt.want)
		}
	}
	if err := checkIsolated("net:[1]", "", lo); err == nil {
		t.Error("with the host's namespace unknown, isolation was taken as given")
	}
	// This test process is not isolated (or was not told the host's
	// namespace): the check a case makes for itself must say so.
	if err := VerifyThisProcessIsIsolated(""); err == nil {
		t.Error("a process that was told nothing verified itself as isolated")
	}
	if host, err := os.Readlink("/proc/self/ns/net"); err == nil && os.Getenv(HostNetNSEnv) == "" {
		if err := VerifyThisProcessIsIsolated(host); err == nil || !strings.Contains(err.Error(), "host's network namespace") {
			t.Errorf("a process in the host's namespace: %v", err)
		}
	}
}

// The isolation is real: a test run under it is in another network
// namespace with loopback alone, verifies that for itself, and cannot reach
// an outside address; the same test run outside it fails its own check.
func TestIsolationIsEnforcedAndVerified(t *testing.T) {
	if os.Getenv(HostNetNSEnv) != "" {
		t.Skip("already inside the acceptance runner's isolation")
	}
	iso, err := NewIsolation(t.TempDir())
	if err != nil {
		if runtime.GOOS != "linux" {
			t.Skipf("no kernel network isolation here: %v", err)
		}
		t.Skipf("this Linux machine cannot create a network namespace, so its cases would be blocked: %v", err)
	}
	probe := `package repro
import ("errors"; "net"; "os"; "strings"; "syscall"; "testing"; "time")
func TestProbe(t *testing.T) {
	host := os.Getenv("` + HostNetNSEnv + `")
	ns, _ := os.Readlink("/proc/self/ns/net")
	dev, _ := os.ReadFile("/proc/net/dev")
	if host == "" || ns == host { t.Fatalf("in the host namespace %s (host %q)", ns, host) }
	if n := strings.Count(string(dev), ":"); n != 1 || !strings.Contains(string(dev), "lo:") { t.Fatalf("interfaces:\n%s", dev) }
	_, err := net.DialTimeout("tcp", "192.0.2.1:9", 2*time.Second)
	if !errors.Is(err, syscall.ENETUNREACH) { t.Fatalf("dial = %v, want network is unreachable", err) }
}
`
	module := goModule(t, probe)
	run, err := RunGoTest(module, ".", "TestProbe", iso, "GOFLAGS=-mod=mod", "GOPROXY=off")
	if err != nil {
		t.Fatalf("isolated run: %v\n%s", err, run.Raw)
	}
	if run.Outcome.Status != "pass" || run.ProcessFailed {
		t.Fatalf("inside the isolation the probe did not pass:\n%s", run.Raw)
	}
	// Outside it, the same probe fails: the check is not vacuous.
	run, err = RunGoTest(module, ".", "TestProbe", nil, iso.Env()[0])
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome.Status != "fail" {
		t.Fatalf("outside the isolation the probe reported %q:\n%s", run.Outcome.Status, run.Raw)
	}
	// And a proof that names the host's namespace is refused.
	host, _ := os.Readlink("/proc/self/ns/net")
	proof := filepath.Join(t.TempDir(), "proof")
	os.WriteFile(proof, []byte(host+"\n    lo:  0\n"), 0o600)
	if err := iso.Verify(proof); err == nil {
		t.Error("a proof recorded in the host's namespace was accepted")
	}
	if err := iso.Verify(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing proof was accepted")
	}
}

// H6: the summary passes through the same redaction as the bundle.
func TestSummaryIsRedacted(t *testing.T) {
	home, _ := os.UserHomeDir()
	c := &Catalog{Cases: []Case{{ID: "AA-001", Title: "Fails", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired}}}
	detail := "open " + home + "/Music/x.mp3: denied; sent Authorization: Bearer abcdef.123456.xyz and https://me:hunter2pass@example.invalid/?token=tok-123456"
	v := Evaluate(c, nil, []Result{{CaseID: "AA-001", Execution: Fail, Detail: detail}}, []Triage{{CaseID: "AA-001", Severity: SeverityMedium, Disposition: DispositionWaiver,
		Rationale: "r", Waiver: &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12", Reference: home + "/notes.txt"}}}, testTarget, "blocked by "+home+"/x with password=hunter2pass")
	text := Summary(Identity{Mode: ModeDevelopment, ReportedVersion: "dev", Environment: map[string]string{"cwd": home + "/src"}}, c,
		[]Problem{{CaseID: "AA-001", Text: "see " + home + "/catalog"}}, v, Sealed{Archive: home + "/evidence/run.tar.gz", SHA256: "abc"}, NewRedactor())
	for _, secret := range []string{home, "abcdef.123456.xyz", "hunter2pass", "tok-123456"} {
		if secret != "" && strings.Contains(text, secret) {
			t.Errorf("%q is in the summary:\n%s", secret, text)
		}
	}
	if !strings.Contains(text, "<home>/notes.txt") || !strings.Contains(text, "<redacted>") {
		t.Errorf("the summary was not redacted as the bundle is:\n%s", text)
	}
}

// A case recorded as not applicable against the catalog is shown with the
// cases that did not pass, as blocking, and not in the list of the cases
// that legitimately do not apply.
func TestSummaryShowsARefusedNotApplicable(t *testing.T) {
	c := &Catalog{Cases: []Case{{ID: "LIVE-YTM-001", Title: "Sign-in", Lane: LaneManual, Tier: TierLive, Driver: DriverManual, Gate: GateRequired}}}
	v := Evaluate(c, nil, []Result{{CaseID: "LIVE-YTM-001", Execution: NotApplicable, Detail: "no account to hand"}}, nil, testTarget)
	text := Summary(Identity{Mode: ModeRelease}, c, nil, v, Sealed{}, NewRedactor())
	if !strings.Contains(text, "| LIVE-YTM-001 Sign-in | required | not applicable | — | — | yes | recorded as not applicable, but the catalog does not make this case inapplicable") {
		t.Errorf("the refused not-applicable is not shown as blocking:\n%s", text)
	}
	if strings.Contains(text, "**Not applicable:**") {
		t.Errorf("it is listed among the cases that legitimately do not apply:\n%s", text)
	}
}

// Round 3, H1: a covering case supplies coverage only by having been
// executed to a verdict. One that was not run, was blocked, does not apply
// or has no valid result covers nothing, whatever its triage says. An
// executed failure does cover; it is then judged by the ordinary gate.
func TestInapplicabilityNeedsAnExecutedSubstitute(t *testing.T) {
	c := &Catalog{Cases: []Case{
		{ID: "LIVE-SPOT-001", Gate: GateRequired, Inapplicable: []Inapplicability{{Platforms: []string{testTarget.Platform}, Reason: "r", CoveredBy: []string{"LIVE-SPOT-002"}}}},
		{ID: "LIVE-SPOT-002", Gate: GateRequired},
	}}
	na := Result{CaseID: "LIVE-SPOT-001", Execution: NotApplicable, Detail: "no account"}
	steve := &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12", Reference: "the release record"}
	low := &Triage{CaseID: "LIVE-SPOT-002", Severity: SeverityLow, Disposition: DispositionDefer}
	waived := &Triage{CaseID: "LIVE-SPOT-002", Severity: SeverityMedium, Disposition: DispositionWaiver, Waiver: steve}
	tests := []struct {
		name       string
		substitute *Result // nil: no result
		triage     *Triage
		primary    string // why LIVE-SPOT-001 blocks; "" when it does not
		publish    bool
	}{
		{"passed", &Result{Execution: Pass}, nil, "", true},
		{"failed, LOW and deferred: executed, so it covers; the ordinary gate lets a LOW through", &Result{Execution: Fail}, low, "", true},
		{"failed, MEDIUM with Steve's waiver", &Result{Execution: Fail}, waived, "", true},
		{"failed, untriaged: it covers, and blocks as any failure does", &Result{Execution: Fail}, nil, "", false},
		{"no result", nil, nil, "was not executed", false},
		{"no result, LOW and deferred", nil, low, "was not executed", false},
		{"not run, LOW and deferred", &Result{Execution: NotRun}, low, "was not executed", false},
		{"not run, MEDIUM with Steve's waiver", &Result{Execution: NotRun}, waived, "was not executed", false},
		{"blocked, LOW and deferred", &Result{Execution: Blocked, EnvironmentLimitation: "outage"}, low, "was not executed", false},
		{"blocked, MEDIUM with Steve's waiver", &Result{Execution: Blocked}, waived, "was not executed", false},
		{"not applicable itself", &Result{Execution: NotApplicable, Detail: "x"}, nil, "was not executed", false},
		{"an execution that is no execution", &Result{Execution: "passed"}, low, "was not executed", false},
		{"a pass invalidated by its environment limitation", &Result{Execution: Pass, EnvironmentLimitation: "no isolation"}, low, "was not executed", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results := []Result{na}
			if tt.substitute != nil {
				tt.substitute.CaseID = "LIVE-SPOT-002"
				results = append(results, *tt.substitute)
			}
			var triage []Triage
			if tt.triage != nil {
				triage = append(triage, *tt.triage)
			}
			v := Evaluate(c, nil, results, triage, testTarget)
			if tt.primary == "" {
				if v.Rows[0].Blocks != "" {
					t.Errorf("the inapplicable case blocks (%q) although its substitute was executed", v.Rows[0].Blocks)
				}
			} else if !strings.Contains(v.Rows[0].Blocks, tt.primary) {
				t.Errorf("the inapplicable case's reason is %q, want one containing %q", v.Rows[0].Blocks, tt.primary)
			}
			if v.Publishable() != tt.publish {
				t.Errorf("publishable = %v, want %v (blocking %v)", v.Publishable(), tt.publish, v.Blocking)
			}
		})
	}
}

// Round 3, H2: a claimed pass with an environment limitation is not a pass
// anywhere: not in the row, the totals, the lanes or the persisted result,
// and its reason is shown. Triage cannot make the invalid record go away.
func TestEnvironmentLimitedPassBecomesBlockedEverywhere(t *testing.T) {
	c := &Catalog{Cases: []Case{
		{ID: "SYNC-002", Title: "Failed sync", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
		{ID: "SYNC-001", Title: "Cache first", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
	}}
	results := []Result{
		{CaseID: "SYNC-002", Execution: Pass, EnvironmentLimitation: "NETWORK_ISOLATION_UNAVAILABLE"},
		{CaseID: "SYNC-001", Execution: Pass},
	}
	for name, triage := range map[string][]Triage{
		"untriaged":           nil,
		"triaged LOW, defer":  {{CaseID: "SYNC-002", Severity: SeverityLow, Disposition: DispositionDefer}},
		"triaged NIT, to fix": {{CaseID: "SYNC-002", Severity: SeverityNit, Disposition: DispositionFix}},
	} {
		t.Run(name, func(t *testing.T) {
			v := Evaluate(c, nil, results, triage, testTarget)
			row := v.Rows[0]
			if row.Result.Execution != Blocked {
				t.Fatalf("the row's execution is %q, want blocked", row.Result.Execution)
			}
			if !strings.Contains(row.Result.Detail, "pass") || row.Result.EnvironmentLimitation != "NETWORK_ISOLATION_UNAVAILABLE" {
				t.Errorf("the original outcome or the limitation was lost: %+v", row.Result)
			}
			if v.Publishable() {
				t.Error("publishable: triage erased an invalid record")
			}
			if !strings.Contains(strings.Join(v.Blocking, "\n"), "SYNC-002") {
				t.Errorf("blocking does not name the case: %v", v.Blocking)
			}
			text := Summary(Identity{Mode: ModeRelease}, c, nil, v, Sealed{}, NewRedactor())
			for _, want := range []string{
				"**2 cases:** 1 pass, 0 fail, 1 blocked, 0 not run, 0 not applicable.",
				"| Scripted, on the binary under test | 2 | 1 | 1 |",
				"| SYNC-002 Failed sync | required | blocked |",
				"SYNC-002: NETWORK_ISOLATION_UNAVAILABLE",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the summary lacks %q:\n%s", want, text)
				}
			}
			// What is persisted is what the gate judged.
			data, _ := json.Marshal(v.Rows[0].Result)
			if !strings.Contains(string(data), `"execution":"blocked"`) {
				t.Errorf("the persisted result reads %s", data)
			}
		})
	}
}

// Round 3, H3: the binding is exact, and "not in the future" means it.
func TestManualBindingIsExact(t *testing.T) {
	id := Identity{Mode: ModeRelease, ExpectedVersion: "v1.0.0-rc.4", SourceSHA: strings.Repeat("a", 40), ArchiveSHA256: strings.Repeat("b", 64), BinarySHA256: strings.Repeat("c", 64)}
	bound := Candidate{Version: id.ExpectedVersion, SourceSHA: id.SourceSHA, ArchiveSHA256: id.ArchiveSHA256, BinarySHA256: id.BinarySHA256}
	c := &Catalog{Cases: []Case{{ID: "HUMAN-001", Driver: DriverManual}}}
	now := time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)
	result := func(at string) []Result {
		return []Result{{CaseID: "HUMAN-001", Execution: Pass, ExecutedBy: "Steve", ExecutedAt: at}}
	}
	tests := []struct {
		name     string
		cand     func(*Candidate)
		at       string
		accepted int
	}{
		{"exact, a second ago", func(*Candidate) {}, "2026-10-12T15:59:59Z", 1},
		{"exact, at this instant", func(*Candidate) {}, "2026-10-12T16:00:00Z", 1},
		{"exact, the same instant in another zone", func(*Candidate) {}, "2026-10-12T18:00:00+02:00", 1},
		{"exact, long ago: age is not judged", func(*Candidate) {}, "2020-01-01T00:00:00Z", 1},
		{"one second in the future", func(*Candidate) {}, "2026-10-12T16:00:01Z", 0},
		{"thirty seconds in the future", func(*Candidate) {}, "2026-10-12T16:00:30Z", 0},
		{"a version that differs only in case", func(b *Candidate) { b.Version = "v1.0.0-RC.4" }, "2026-10-12T15:00:00Z", 0},
		{"a version with a space after it", func(b *Candidate) { b.Version = "v1.0.0-rc.4 " }, "2026-10-12T15:00:00Z", 0},
		{"a source commit in capitals", func(b *Candidate) { b.SourceSHA = strings.Repeat("A", 40) }, "2026-10-12T15:00:00Z", 0},
		{"an archive checksum in capitals", func(b *Candidate) { b.ArchiveSHA256 = strings.Repeat("B", 64) }, "2026-10-12T15:00:00Z", 0},
		{"a binary checksum in capitals", func(b *Candidate) { b.BinarySHA256 = strings.Repeat("C", 64) }, "2026-10-12T15:00:00Z", 0},
		{"an abbreviated commit", func(b *Candidate) { b.SourceSHA = strings.Repeat("a", 12) }, "2026-10-12T15:00:00Z", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := bound
			tt.cand(&b)
			accepted, problems := Manual{Candidate: b, Results: result(tt.at)}.Accept(id, c, now)
			if len(accepted) != tt.accepted || (tt.accepted == 0) == (len(problems) == 0) {
				t.Errorf("accepted %d with problems %v, want %d accepted", len(accepted), problems, tt.accepted)
			}
		})
	}
}

// Round 3, H5: the formats the harness actually stores.
func TestRedactorContainerAndEscapedFormats(t *testing.T) {
	r := NewRedactor()
	for _, tt := range []struct{ name, in, secret string }{
		{"a single-quoted TOML key", `'password' = 'singlequoted-secret'`, "singlequoted-secret"},
		{"a single-quoted YAML key", `'access_token': 'yaml-single-secret'`, "yaml-single-secret"},
		{"a triple-quoted TOML string", "password = \"\"\"\ntriple-secret-one\ntriple-secret-two\n\"\"\"\nvolume = 0\n", "triple-secret"},
		{"a triple-quoted literal TOML string", "client_secret = '''\nliteral-secret-one\n'''\n", "literal-secret-one"},
		{"a YAML block scalar", "password: |\n  block-secret-one\n  block-secret-two\nvolume: 0\n", "block-secret"},
		{"a folded YAML block scalar, indented", "auth:\n  refresh_token: >-\n    folded-secret\n  other: 1\n", "folded-secret"},
		{"a cookie name and value array", `{"cookies":[{"name":"sp_dc","value":"cookie-secret"},{"name":"x","value":"cookie-two"}]}`, "cookie-secret"},
		{"the second cookie of the array", `{"cookies":[{"name":"sp_dc","value":"cookie-secret"},{"name":"x","value":"cookie-two"}]}`, "cookie-two"},
		{"a cookie object outside a cookie key", `[{"domain":".youtube.com","name":"SAPISID","value":"loose-cookie-secret"}]`, "loose-cookie-secret"},
		{"a secret object", `{"token":{"access":"object-secret","type":"x"}}`, "object-secret"},
		{"JSON inside a JSON string", `{"Action":"output","Output":"{\"password\":\"nested-json-secret\"}\n"}`, "nested-json-secret"},
		{"TOML inside a JSON string", `{"Action":"output","Output":"password = \"nested-toml-secret\"\n"}`, "nested-toml-secret"},
		{"a header inside a JSON string", `{"Action":"output","Output":"Authorization: Bearer nested.bearer.secret\n"}`, "nested.bearer.secret"},
	} {
		if out := r.RedactString(tt.in); strings.Contains(out, tt.secret) {
			t.Errorf("%s: %q survived in %q", tt.name, tt.secret, out)
		}
	}
	// A line of "go test -json" stays a line of JSON, with its other fields.
	line := `{"Action":"output","Test":"TestX","Output":"password = \"nested-toml-secret\"\n"}`
	var ev struct{ Action, Test, Output string }
	if err := json.Unmarshal([]byte(r.RedactString(line)), &ev); err != nil || ev.Action != "output" || ev.Test != "TestX" || !strings.Contains(ev.Output, "<redacted>") {
		t.Errorf("redaction damaged the JSON line: %v, %+v, from %q", err, ev, r.RedactString(line))
	}
	// What follows a secret is kept.
	if out := r.RedactString("password = \"\"\"\nx\n\"\"\"\nvolume = 0\n"); !strings.Contains(out, "volume = 0") {
		t.Errorf("redaction swallowed what follows a multi-line secret: %q", out)
	}
	if out := r.RedactString("password: |\n  x\nvolume: 0\n"); !strings.Contains(out, "volume: 0") {
		t.Errorf("redaction swallowed what follows a block scalar: %q", out)
	}
}

// Round 3, H5 and H6: the sealed evidence and the summary, with a credential
// longer than the summary's cells, in every field that reaches them.
func TestSealedEvidenceAndSummaryHoldNoLongCredential(t *testing.T) {
	secret := strings.Repeat("Q", 300)
	leak := `{"password":"` + secret + `"}`
	header := "Authorization:   Bearer   " + secret
	c := &Catalog{Cases: []Case{
		{ID: "AA-001", Title: "Blocked", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
		{ID: "AA-002", Title: "Failed", Lane: LaneGo, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
		{ID: "AA-003", Title: "Waived", Lane: LaneGo, Driver: DriverScripted, Implemented: true, Gate: GateRequired},
	}}
	results := []Result{
		{CaseID: "AA-001", Execution: Blocked, EnvironmentLimitation: "the player printed " + leak},
		{CaseID: "AA-002", Execution: Fail, Detail: "padding padding padding\n" + header + "\nmore"},
		{CaseID: "AA-003", Execution: Fail, Detail: leak},
	}
	triage := []Triage{{CaseID: "AA-003", Severity: SeverityMedium, Disposition: DispositionWaiver, Rationale: "because " + leak,
		Waiver: &Waiver{ApprovedBy: WaiverApprover, Date: "2026-10-12", Reference: "see " + leak}}}
	id := Identity{Mode: ModeRelease, Environment: map[string]string{"note": leak, "network isolation": "NOT ESTABLISHED: " + leak}, Tools: map[string]string{"tool": header}}
	v := Evaluate(c, []Problem{{CaseID: "AA-001", Gating: true, Text: leak}}, results, triage, testTarget, "blocked because "+leak)
	r := NewRedactor()

	text := Summary(id, c, []Problem{{CaseID: "AA-001", Gating: true, Text: leak}}, v, Sealed{}, r)
	if strings.Contains(text, "QQQQQQQQ") {
		t.Errorf("the summary holds part of a long credential:\n%s", text)
	}

	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]Result, len(v.Rows))
	for i, row := range v.Rows {
		rows[i] = row.Result
	}
	for name, value := range map[string]any{"results.json": rows, "identity.json": id, "gate.json": v.Blocking} {
		if err := WriteJSON(filepath.Join(b.Dir, name), value); err != nil {
			t.Fatal(err)
		}
	}
	// A lane's "go test -json" output, as stored: the secret is inside a
	// JSON string, in JSON, TOML and header form.
	jsonl := `{"Action":"output","Test":"TestX","Output":"{\"password\":\"` + secret + `\"}\n"}` + "\n" +
		`{"Action":"output","Test":"TestX","Output":"password = \"` + secret + `\"\n"}` + "\n" +
		`{"Action":"output","Test":"TestX","Output":"` + header + `\n"}` + "\n" + "not json: " + leak + "\n"
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), []byte(jsonl), 0o600)
	os.WriteFile(filepath.Join(b.Dir, "player.log"), []byte("password = '''\n"+secret+"\n'''\n"), 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("tar", "-xzOf", sealed.Archive).Output()
	if err != nil {
		t.Skipf("no tar to read the archive back with: %v", err)
	}
	if strings.Contains(string(out), "QQQQQQQQ") {
		t.Errorf("the sealed evidence holds part of a long credential")
	}
	// The stored JSON is still JSON.
	data, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/results.json").Output()
	var back []Result
	if err != nil || json.Unmarshal(data, &back) != nil || len(back) != 3 {
		t.Errorf("results.json did not survive redaction as JSON: %v\n%s", err, data)
	}
	lines, _ := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	for i, line := range strings.Split(strings.TrimSpace(string(lines)), "\n")[:3] {
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil || ev["Test"] != "TestX" {
			t.Errorf("line %d of the stored test output is no longer JSON: %q", i+1, line)
		}
	}
}

// Round 4, H5: values that TOML and YAML parsers accept, read whole, by the
// rules those formats really have.
func TestRedactorReadsWholeParserValidValues(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name    string
		format  string // "toml" or "yaml": the fixture is checked with that parser
		in      string
		secrets []string
		keeps   []string
	}{
		{"a YAML block scalar with a comment on its header", "yaml", "password: | # ordinary comment\n  yaml-comment-secret\nnext: ok\n", []string{"yaml-comment-secret"}, []string{"next: ok"}},
		{"a YAML block scalar with indicators and a comment", "yaml", "auth:\n  token: >-2 # folded\n      deep-secret\n      more-secret\n  next: ok\n", []string{"deep-secret", "more-secret"}, []string{"next: ok"}},
		{"a YAML single-quoted string with a doubled apostrophe", "yaml", "password: 'alice''s remaining-secret'\nnext: ok\n", []string{"remaining-secret", "alice"}, []string{"next: ok"}},
		{"a YAML sequence of cookies under a secret key", "yaml", "cookies:\n  - name: sp_dc\n    value: cookie-block-secret\n  - name: other\n    value: second-block-secret\nnext: ok\n", []string{"cookie-block-secret", "second-block-secret"}, []string{"next: ok"}},
		{"a YAML sequence at the key's own indentation", "yaml", "cookies:\n- name: sp_dc\n  value: flush-seq-secret\nnext: ok\n", []string{"flush-seq-secret"}, []string{"next: ok"}},
		{"a YAML mapping under a secret key", "yaml", "token:\n  access: nested-map-secret\n  kind: bearer\nnext: ok\n", []string{"nested-map-secret"}, []string{"next: ok"}},
		{"a TOML multi-line string with an escaped quote before more quotes", "toml", "password = \"\"\"before \\\"\"\" after-triple-secret\nlast-secret\"\"\"\nnext = 1\n", []string{"after-triple-secret", "last-secret", "before"}, []string{"next = 1"}},
		{"a TOML multi-line string ending in four quotes", "toml", "password = \"\"\"four-quote-secret\"\"\"\"\nnext = 1\n", []string{"four-quote-secret"}, []string{"next = 1"}},
		{"a TOML literal multi-line string ending in five quotes", "toml", "client_secret = '''literal-secret'''''\nnext = 1\n", []string{"literal-secret"}, []string{"next = 1"}},
		{"a TOML string with an escaped backslash at its end", "toml", "password = \"ends-in-backslash-secret\\\\\"\nnext = \"visible\"\n", []string{"ends-in-backslash-secret"}, []string{`next = "visible"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var parsed map[string]any
			var err error
			if tt.format == "toml" {
				_, err = toml.Decode(tt.in, &parsed)
			} else {
				err = yaml.Unmarshal([]byte(tt.in), &parsed)
			}
			if err != nil {
				t.Fatalf("the fixture is not valid %s: %v\n%s", tt.format, err, tt.in)
			}
			out := r.RedactString(tt.in)
			for _, secret := range tt.secrets {
				if strings.Contains(out, secret) {
					t.Errorf("%q survived:\n%s", secret, out)
				}
			}
			for _, keep := range tt.keeps {
				if !strings.Contains(out, keep) {
					t.Errorf("what follows the secret (%q) was lost:\n%s", keep, out)
				}
			}
			if strings.Count(out, "\n") != strings.Count(tt.in, "\n") {
				t.Errorf("redaction changed the number of lines from %d to %d:\n%s", strings.Count(tt.in, "\n"), strings.Count(out, "\n"), out)
			}
		})
	}
}

// Round 4, H5: the stored representation. "go test -json" gives every
// printed line an Output event of its own, so a value over several lines
// arrives in pieces. The pieces are read together, per test.
func TestRedactionOfRealGoTestOutputAcrossEvents(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	module := goModule(t, `package repro
import ("fmt"; "testing")
func TestPrintedConfig(t *testing.T) {
	fmt.Print("before: visible-before\npassword = \"\"\"\nactual-jsonl-toml-secret\nsecond-toml-secret\n\"\"\"\nmiddle = \"visible-middle\"\npassword: | # c\n  actual-jsonl-yaml-secret\nnext: visible-after\ncookies:\n  - name: sp_dc\n    value: actual-cookie-secret\nlast: visible-last\n")
	t.Log("password = '''\nlogged-secret\n'''")
}
func TestOther(t *testing.T) { fmt.Println("other: visible-other") }
`)
	var raw []byte
	for _, name := range []string{"TestPrintedConfig", "TestOther"} {
		run, err := RunGoTest(module, ".", name, nil)
		if err != nil || run.Outcome.Status != "pass" {
			t.Fatalf("%s: %v, %q\n%s", name, err, run.Outcome.Status, run.Raw)
		}
		raw = append(raw, run.Raw...)
	}
	secrets := []string{"actual-jsonl-toml-secret", "second-toml-secret", "actual-jsonl-yaml-secret", "actual-cookie-secret", "logged-secret"}
	visible := []string{"visible-before", "visible-middle", "visible-after", "visible-last", "visible-other"}
	// The fixture is real: go test did split the value across events.
	if strings.Count(string(raw), "actual-jsonl-toml-secret") != 1 || strings.Contains(string(raw), `password = \"\"\"\nactual-jsonl-toml-secret`) {
		t.Fatalf("go test did not emit the value as separate events:\n%s", raw)
	}
	check := func(where string, data []byte) {
		t.Helper()
		for _, secret := range secrets {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s: %q survived", where, secret)
			}
		}
		for _, keep := range visible {
			if !strings.Contains(string(data), keep) {
				t.Errorf("%s: unrelated output %q was lost", where, keep)
			}
		}
		// Still JSON lines, with the events' other fields, and the test's
		// verdict still readable.
		verdicts := 0
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ev struct{ Action, Package, Test string }
			if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Action == "" {
				t.Errorf("%s: a line is no longer a go test event: %q", where, line)
			}
			if ev.Action == "pass" && ev.Test != "" {
				verdicts++
			}
		}
		if verdicts != 2 {
			t.Errorf("%s: %d test verdicts readable, want 2", where, verdicts)
		}
	}
	r := NewRedactor()
	check("redacted directly", r.Redact(raw))

	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), raw, 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	if err != nil {
		t.Skipf("no tar to read the archive back with: %v", err)
	}
	check("in the sealed bundle", stored)
}

// The same pieces, when the events of two tests are interleaved, or an
// event is not a whole line: nothing leaks, and the lines stay events.
func TestRedactionAcrossEventsIsPerStreamAndConservative(t *testing.T) {
	r := NewRedactor()
	event := func(test, output string) string {
		data, _ := json.Marshal(map[string]string{"Action": "output", "Package": "p", "Test": test, "Output": output})
		return string(data) + "\n"
	}
	interleaved := event("TestA", "password = \"\"\"\n") + event("TestB", "b: visible-b\n") + event("TestA", "interleaved-secret\n") +
		event("TestB", "password: |\n") + event("TestA", "\"\"\"\n") + event("TestB", "  b-block-secret\n") + event("TestA", "a: visible-a\n") + event("TestB", "next: visible-next\n")
	out := r.RedactString(interleaved)
	for _, secret := range []string{"interleaved-secret", "b-block-secret"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q survived in interleaved events:\n%s", secret, out)
		}
	}
	for _, keep := range []string{"visible-a", "visible-b", "visible-next"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q was lost from interleaved events:\n%s", keep, out)
		}
	}
	// Events that are not whole lines: the value still does not leak.
	partial := event("TestA", "password = \"\"\"par") + event("TestA", "tial-secret-one\npartial-secret-two") + event("TestA", "\"\"\"\nafter: visible-after\n")
	out = r.RedactString(partial)
	for _, secret := range []string{"tial-secret-one", "partial-secret-two"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q survived in partial-line events:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "visible-after") {
		t.Errorf("what follows was lost from partial-line events:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("a line is no longer JSON: %q", line)
		}
	}
}

// yamlSecrets are YAML documents a parser accepts, each with a credential
// under a secret-named key written in an ordinary way, and an unrelated
// field after it.
var yamlSecrets = []struct {
	name    string
	in      string
	secrets []string
}{
	{"a plain scalar of several words", "password: head tail-secret\nnext: ok\n", []string{"tail-secret", "head"}},
	{"a plain scalar continued on an indented line", "password: head\n  multiline-secret\nnext: ok\n", []string{"multiline-secret", "head"}},
	{"a plain scalar continued over blank and indented lines", "auth:\n  password: first-word\n\n    far-continuation-secret\n  next: ok\n", []string{"far-continuation-secret", "first-word"}},
	{"an anchored scalar", "password: &pw anchored-secret\nnext: ok\n", []string{"anchored-secret"}},
	{"a tagged scalar", "password: !!str tagged-secret\nnext: ok\n", []string{"tagged-secret"}},
	{"an anchored sequence of cookies", "cookies: &jar\n  - name: session\n    value: anchored-cookie-secret\nnext: ok\n", []string{"anchored-cookie-secret"}},
	{"an anchored sequence at the key's own indentation", "cookies: &jar\n- name: session\n  value: flush-anchored-secret\nnext: ok\n", []string{"flush-anchored-secret"}},
	{"a double-quoted scalar over two lines", "password: \"head\n  quoted-multiline-secret\"\nnext: ok\n", []string{"quoted-multiline-secret", "head"}},
	{"a single-quoted scalar over three lines", "password: 'first-single-secret\n  two-single-secret\n  three''s-secret'\nnext: ok\n", []string{"two-single-secret", "s-secret", "first-single-secret"}},
	{"a double-quoted scalar with an escaped quote, over lines", "password: \"a \\\" escaped-quote-secret\n  more-secret\"\nnext: ok\n", []string{"escaped-quote-secret", "more-secret"}},
	{"a scalar with a comment after it", "password: commented-secret # mine\nnext: ok\n", []string{"commented-secret"}},
	{"a flow sequence over lines", "cookies: [flow-one-secret,\n  flow-two-secret]\nnext: ok\n", []string{"flow-one-secret", "flow-two-secret"}},
	// Round 6: a value ends where YAML says it ends, not where its
	// indentation, a comment or an anchor might suggest.
	{"a double-quoted scalar closed on an unindented line", "password: \"head\nquoted-unindented-secret\"\nnext: ok\n", []string{"quoted-unindented-secret", "head"}},
	{"a single-quoted scalar closed on an unindented line", "password: 'head\nsingle-unindented-secret'\nnext: ok\n", []string{"single-unindented-secret", "head"}},
	{"a quoted scalar over a blank line", "password: 'head\n  one-secret\n\n  two-secret'\nnext: ok\n", []string{"one-secret", "two-secret", "head"}},
	{"an anchored flow sequence over lines", "password: &pw [head,\nanchored-flow-secret]\nnext: ok\n", []string{"anchored-flow-secret", "head"}},
	{"a tagged flow sequence over lines", "password: !!seq [head,\n  tagged-flow-secret]\nnext: ok\n", []string{"tagged-flow-secret", "head"}},
	{"a flow sequence closed on an unindented line", "password: [head,\nflow-unindented-secret]\nnext: ok\n", []string{"flow-unindented-secret", "head"}},
	{"an anchored quoted scalar over lines", "password: &pw \"head\nanchored-quoted-secret\"\nnext: ok\n", []string{"anchored-quoted-secret", "head"}},
	{"a block value after a comment line", "password:\n# a comment\n  block-after-comment-secret\nnext: ok\n", []string{"block-after-comment-secret"}},
	{"a sequence after a comment line, under an anchor", "cookies: &jar\n# a comment\n- collection-after-comment-secret\nnext: ok\n", []string{"collection-after-comment-secret"}},
	{"a mapping after two comment lines and a blank", "token:\n  # one\n\n# two\n  access: mapping-after-comments-secret\nnext: ok\n", []string{"mapping-after-comments-secret"}},
	{"a block scalar with a comment-like line in its body", "password: |\n  body-one-secret\n  # body-comment-secret\n  body-three-secret\nnext: ok\n", []string{"body-one-secret", "body-comment-secret", "body-three-secret"}},
	// Round 7: a bracket, brace or quote inside a comment is not syntax.
	{"a flow sequence with a bracket in a comment", "cookies: [\n  # ] end of an example list\n  {name: sp_dc, value: comment-cookie-secret}\n]\nnext: ok\n", []string{"comment-cookie-secret"}},
	{"a flow mapping with a brace and a quote in a comment", "token: {\n  # } not the end, and a \" too\n  access: comment-brace-secret\n}\nnext: ok\n", []string{"comment-brace-secret"}},
	{"a flow sequence with a comment after an item", "cookies: [first-item-secret, # ] ' \"\n  second-item-secret]\nnext: ok\n", []string{"first-item-secret", "second-item-secret"}},
	{"an anchored flow sequence with a bracket in a comment", "cookies: &jar [\n# ]\nanchored-comment-secret]\nnext: ok\n", []string{"anchored-comment-secret"}},
	{"a flow sequence holding a hash that is no comment", "cookies: [a#b-hash-secret, 'c # d-quoted-secret']\nnext: ok\n", []string{"b-hash-secret", "d-quoted-secret"}},
	{"a flow sequence with a single-quoted backslash", "cookies: ['C:\\\\', after-backslash-secret]\nnext: ok\n", []string{"after-backslash-secret"}},
	{"a nested secret key closed on an unindented line", "auth:\n  password: \"head\nnested-unindented-secret\"\n  next: ok\n", []string{"nested-unindented-secret", "head"}},
}

// Round 5, H5: complete YAML values under secret keys, through every sink:
// redacted directly, inside a structured result, in the summary, and in
// sealed log, JSONL and JSON files.
func TestRedactionOfCompleteYAMLValuesInEverySink(t *testing.T) {
	r := NewRedactor()
	for _, tt := range yamlSecrets {
		t.Run(tt.name, func(t *testing.T) {
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(tt.in), &parsed); err != nil {
				t.Fatalf("the fixture is not valid YAML: %v\n%s", err, tt.in)
			}
			check := func(where, text string, wantNext bool) {
				t.Helper()
				for _, secret := range tt.secrets {
					if strings.Contains(text, secret) {
						t.Errorf("%s: %q survived:\n%s", where, secret, text)
					}
				}
				if wantNext && !strings.Contains(text, "next: ok") {
					t.Errorf("%s: the unrelated field after the secret was lost:\n%s", where, text)
				}
			}
			direct := r.RedactString(tt.in)
			check("redacted directly", direct, true)
			if strings.Count(direct, "\n") != strings.Count(tt.in, "\n") {
				t.Errorf("redaction changed the number of lines:\n%s", direct)
			}

			// In a structured result, and so in results.json and the summary.
			c := &Catalog{Cases: []Case{{ID: "AA-001", Title: "Blocked", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired}}}
			results := []Result{{CaseID: "AA-001", Execution: Blocked, EnvironmentLimitation: tt.in, Detail: "the config read:\n" + tt.in}}
			v := Evaluate(c, nil, results, nil, testTarget)
			check("the summary", Summary(Identity{Mode: ModeRelease}, c, nil, v, Sealed{}, r), false)

			b, err := NewBundle(t.TempDir(), "run-1")
			if err != nil {
				t.Fatal(err)
			}
			WriteJSON(filepath.Join(b.Dir, "results.json"), results)
			os.WriteFile(filepath.Join(b.Dir, "player.log"), []byte("starting\n"+tt.in+"done\n"), 0o600)
			var jsonl strings.Builder
			for line := range strings.SplitAfterSeq(tt.in, "\n") {
				if line == "" {
					continue
				}
				ev, _ := json.Marshal(map[string]string{"Action": "output", "Package": "p", "Test": "TestX", "Output": line})
				jsonl.WriteString(string(ev) + "\n")
			}
			os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), []byte(jsonl.String()), 0o600)
			sealed, err := b.Seal(r)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"results.json", "player.log", "go-test-root.jsonl"} {
				data, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/"+name).Output()
				if err != nil {
					t.Skipf("no tar to read the archive back with: %v", err)
				}
				check("sealed "+name, string(data), name == "player.log")
				if name != "player.log" {
					for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
						if name == "go-test-root.jsonl" && !json.Valid([]byte(line)) {
							t.Errorf("sealed %s: a line is no longer JSON: %q", name, line)
						}
					}
				}
			}
			if data, _ := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output(); !strings.Contains(string(data), `next: ok`) {
				t.Errorf("sealed go-test-root.jsonl lost the unrelated field:\n%s", data)
			}
			var back []Result
			if data, _ := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/results.json").Output(); json.Unmarshal(data, &back) != nil || len(back) != 1 || back[0].Execution != Blocked {
				t.Errorf("sealed results.json is no longer the result:\n%s", data)
			}
		})
	}
}

// Round 5, H5: a real test printing a quoted YAML credential over lines.
func TestRedactionOfRealGoTestQuotedMultilineYAML(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	module := goModule(t, `package repro
import ("fmt"; "testing")
func TestPrintedYAML(t *testing.T) {
	fmt.Print("before: visible-before\npassword: \"head\n  streamed-quoted-secret\n  last-streamed-secret\"\nnext: visible-after\npassword: plain words-secret\n  continued-plain-secret\ncookies: &jar\n  - name: s\n    value: streamed-cookie-secret\nlast: visible-last\n")
}
`)
	run, err := RunGoTest(module, ".", "TestPrintedYAML", nil)
	if err != nil || run.Outcome.Status != "pass" {
		t.Fatalf("%v, %q\n%s", err, run.Outcome.Status, run.Raw)
	}
	if strings.Count(string(run.Raw), `"Output":"  streamed-quoted-secret\n"`) != 1 {
		t.Fatalf("go test did not emit the continuation as an event of its own:\n%s", run.Raw)
	}
	secrets := []string{"streamed-quoted-secret", "last-streamed-secret", "words-secret", "continued-plain-secret", "streamed-cookie-secret"}
	check := func(where string, data []byte) {
		t.Helper()
		for _, secret := range secrets {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s: %q survived", where, secret)
			}
		}
		for _, keep := range []string{"visible-before", "visible-after", "visible-last"} {
			if !strings.Contains(string(data), keep) {
				t.Errorf("%s: unrelated output %q was lost", where, keep)
			}
		}
		passed := false
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ev struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &ev) != nil {
				t.Errorf("%s: a line is no longer JSON: %q", where, line)
			}
			passed = passed || ev.Action == "pass" && ev.Test == "TestPrintedYAML"
		}
		if !passed {
			t.Errorf("%s: the test's verdict is no longer readable", where)
		}
	}
	r := NewRedactor()
	check("redacted directly", r.Redact(run.Raw))
	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), run.Raw, 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	if err != nil {
		t.Skipf("no tar: %v", err)
	}
	check("in the sealed bundle", stored)
}

// Round 6, H5: a real test printing a quoted YAML credential whose last
// line is not indented, and a block value after a comment.
func TestRedactionOfRealGoTestUnindentedQuotedYAML(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	module := goModule(t, `package repro
import ("fmt"; "testing")
func TestPrintedYAML(t *testing.T) {
	fmt.Print("before: visible-before\npassword: \"head\nstream-unindented-secret\"\nnext: visible-after\npassword:\n# a comment\n  stream-after-comment-secret\ncookies: &jar [a,\nstream-flow-secret]\nlast: visible-last\n")
}
`)
	run, err := RunGoTest(module, ".", "TestPrintedYAML", nil)
	if err != nil || run.Outcome.Status != "pass" {
		t.Fatalf("%v, %q\n%s", err, run.Outcome.Status, run.Raw)
	}
	if strings.Count(string(run.Raw), `"Output":"stream-unindented-secret\"\n"`) != 1 {
		t.Fatalf("go test did not emit the unindented last line as an event of its own:\n%s", run.Raw)
	}
	check := func(where string, data []byte) {
		t.Helper()
		for _, secret := range []string{"stream-unindented-secret", "stream-after-comment-secret", "stream-flow-secret"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s: %q survived", where, secret)
			}
		}
		for _, keep := range []string{"visible-before", "visible-after", "visible-last"} {
			if !strings.Contains(string(data), keep) {
				t.Errorf("%s: unrelated output %q was lost", where, keep)
			}
		}
		passed := false
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ev struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &ev) != nil {
				t.Errorf("%s: a line is no longer JSON: %q", where, line)
			}
			passed = passed || ev.Action == "pass" && ev.Test == "TestPrintedYAML"
		}
		if !passed {
			t.Errorf("%s: the test's verdict is no longer readable", where)
		}
	}
	r := NewRedactor()
	check("redacted directly", r.Redact(run.Raw))
	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), run.Raw, 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	if err != nil {
		t.Skipf("no tar: %v", err)
	}
	check("in the sealed bundle", stored)
}

// What is not a credential, and not under a secret key, stays: the policy
// did not become "remove everything".
func TestRedactionKeepsWhatIsNotUnderASecretKey(t *testing.T) {
	r := NewRedactor()
	for _, kept := range []string{
		"title: \"First\nSong\"\nnext: ok\n",
		"notes:\n# a comment\n  visible-block\nnext: ok\n",
		"list: &a [one,\ntwo]\nnext: ok\n",
		"password: \"closed-secret\"\ntitle: \"visible title\"\nnext: ok\n",
	} {
		out := r.RedactString(kept)
		for _, want := range []string{"next: ok"} {
			if !strings.Contains(out, want) {
				t.Errorf("%q lost %q: %q", kept, want, out)
			}
		}
		if strings.Contains(kept, "visible") && !strings.Contains(out, "visible") {
			t.Errorf("unrelated text was removed from %q: %q", kept, out)
		}
		if !strings.Contains(kept, "secret") && out != kept {
			t.Errorf("text with no secret key was changed: %q became %q", kept, out)
		}
	}
	// A quote that never closes anywhere removes only what could belong to it.
	out := r.RedactString("password: \"never closed\n  continued-secret\nnext: ok\nlater: visible-later\n")
	if strings.Contains(out, "continued-secret") || !strings.Contains(out, "next: ok") || !strings.Contains(out, "visible-later") {
		t.Errorf("an unclosed quote: %q", out)
	}
}

// sinksOf puts text holding a credential through every place evidence goes:
// redacted directly, in the summary, and read back from a sealed bundle as
// a structured result, a mixed log and stored "go test -json" output.
func sinksOf(t *testing.T, in string) map[string]string {
	t.Helper()
	r := NewRedactor()
	sinks := map[string]string{"redacted directly": r.RedactString(in)}
	c := &Catalog{Cases: []Case{{ID: "AA-001", Title: "Blocked", Lane: LaneBinary, Driver: DriverScripted, Implemented: true, Gate: GateRequired}}}
	results := []Result{{CaseID: "AA-001", Execution: Blocked, EnvironmentLimitation: in, Detail: "the config read:\n" + in}}
	sinks["the summary"] = Summary(Identity{Mode: ModeRelease}, c, nil, Evaluate(c, nil, results, nil, testTarget), Sealed{}, r)
	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	WriteJSON(filepath.Join(b.Dir, "results.json"), results)
	os.WriteFile(filepath.Join(b.Dir, "player.log"), []byte("starting up\n"+in+"shut down\n"), 0o600)
	var jsonl strings.Builder
	for line := range strings.SplitAfterSeq(in, "\n") {
		if line != "" {
			ev, _ := json.Marshal(map[string]string{"Action": "output", "Package": "p", "Test": "TestX", "Output": line})
			jsonl.WriteString(string(ev) + "\n")
		}
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), []byte(jsonl.String()), 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"results.json", "player.log", "go-test-root.jsonl"} {
		data, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/"+name).Output()
		if err != nil {
			t.Skipf("no tar to read the archive back with: %v", err)
		}
		sinks["sealed "+name] = string(data)
	}
	for _, line := range strings.Split(strings.TrimSpace(sinks["sealed go-test-root.jsonl"]), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("sealed go-test-root.jsonl: a line is no longer JSON: %q", line)
		}
	}
	return sinks
}

// Round 7, H5: TOML arrays and inline tables, with comments that hold
// delimiters, through every sink.
func TestRedactionOfTOMLCollectionsWithCommentsInEverySink(t *testing.T) {
	for _, tt := range []struct {
		name    string
		in      string
		secrets []string
	}{
		{"an array with a bracket in a comment", "cookies = [\n  # ] comment\n  \"toml-cookie-secret\",\n]\nnext = \"ok\"\n", []string{"toml-cookie-secret"}},
		{"an array with a comment after an item", "cookies = [\"first-toml-secret\", # ] } ' \"\n  \"second-toml-secret\"]\nnext = \"ok\"\n", []string{"first-toml-secret", "second-toml-secret"}},
		{"an array of inline tables under a comment", "cookies = [\n# { not a table ]\n  { name = \"sp_dc\", value = \"toml-table-secret\" },\n]\nnext = \"ok\"\n", []string{"toml-table-secret"}},
		{"an array holding a multi-line string with a bracket", "cookies = [\n  \"\"\"\n] multi-line-item-secret\n\"\"\",\n  \"after-multiline-secret\",\n]\nnext = \"ok\"\n", []string{"multi-line-item-secret", "after-multiline-secret"}},
		{"an array holding a literal string with a backslash", "cookies = ['C:\\', 'literal-after-secret']\nnext = \"ok\"\n", []string{"literal-after-secret"}},
		// Preflight after round 7: in TOML a "#" ends a bare value and starts a
		// comment with no space before it.
		{"a comment straight after a number", "cookies = [\n  1# ] comment\n  , \"bare-number-comment-secret\",\n]\nnext = \"ok\"\n", []string{"bare-number-comment-secret"}},
		{"a comment straight after a boolean, a float and a date", "cookies = [true# ]\n  , 1.5# } \"\n  , 2026-10-09# ] '\n  , \"bare-mixed-comment-secret\"]\nnext = \"ok\"\n", []string{"bare-mixed-comment-secret"}},
		{"a comment straight after a number in an inline table's array", "token = { ids = [1# ]\n  , 2], access = \"inline-bare-comment-secret\" }\nnext = \"ok\"\n", []string{"inline-bare-comment-secret"}},
		// Round 8: a multi-line string in a collection may close on four or
		// five quotes, as it may anywhere; what follows it is a new item.
		{"a four-quote basic string, then a string holding a bracket", "cookies = [\"\"\"head\"\"\"\", \"one]quote-run-secret\"]\nnext = \"ok\"\n", []string{"quote-run-secret", "head"}},
		{"a four-quote literal string, then a string holding a bracket", "cookies = ['''head'''', 'one]literal-run-secret']\nnext = \"ok\"\n", []string{"literal-run-secret", "head"}},
		{"a five-quote basic string, then a string holding a bracket", "cookies = [\"\"\"head\"\"\"\"\", \"one]five-run-secret\"]\nnext = \"ok\"\n", []string{"five-run-secret"}},
		{"a five-quote literal string, then a string holding a bracket", "cookies = ['''head''''', 'one]five-literal-secret']\nnext = \"ok\"\n", []string{"five-literal-secret"}},
		{"a three-quote string, then a string holding a bracket", "cookies = [\"\"\"head\"\"\", \"one]three-run-secret\"]\nnext = \"ok\"\n", []string{"three-run-secret"}},
		{"a four-quote string over lines, in an inline table", "token = { parts = [\"\"\"\nhead\"\"\"\", \"a]table-run-secret\"], kind = \"x\" }\nnext = \"ok\"\n", []string{"table-run-secret"}},
		{"an escaped quote before the closing run", "cookies = [\"\"\"head\\\"\"\"\"\", \"one]escaped-run-secret\"]\nnext = \"ok\"\n", []string{"escaped-run-secret"}},
		{"an array holding a hash inside a string", "cookies = [\"a # ] b-string-secret\", \"c-secret\"]\nnext = \"ok\"\n", []string{"b-string-secret", "c-secret"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var parsed map[string]any
			if _, err := toml.Decode(tt.in, &parsed); err != nil {
				t.Fatalf("the fixture is not valid TOML: %v\n%s", err, tt.in)
			}
			for where, text := range sinksOf(t, tt.in) {
				for _, secret := range tt.secrets {
					if strings.Contains(text, secret) {
						t.Errorf("%s: %q survived:\n%s", where, secret, text)
					}
				}
				if (where == "redacted directly" || where == "sealed player.log") && !strings.Contains(text, `next = "ok"`) {
					t.Errorf("%s: the unrelated field after the secret was lost:\n%s", where, text)
				}
			}
			if direct := NewRedactor().RedactString(tt.in); strings.Count(direct, "\n") != strings.Count(tt.in, "\n") {
				t.Errorf("redaction changed the number of lines:\n%s", direct)
			}
		})
	}
}

// Round 7, H5: a real test printing collections whose comments hold
// delimiters, in YAML and in TOML.
func TestRedactionOfRealGoTestCollectionsWithComments(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	module := goModule(t, "package repro\nimport (\"fmt\"; \"testing\")\nfunc TestPrinted(t *testing.T) {\n"+
		"\tfmt.Print(\"before: visible-before\\ncookies: [\\n  # ] end of an example list\\n  {name: sp_dc, value: stream-yaml-comment-secret}\\n]\\nnext: visible-after\\n\")\n"+
		"\tfmt.Print(\"cookies = [\\n  # ] comment\\n  \\\"stream-toml-comment-secret\\\",\\n]\\nlast = \\\"visible-last\\\"\\n\")\n}\n")
	run, err := RunGoTest(module, ".", "TestPrinted", nil)
	if err != nil || run.Outcome.Status != "pass" {
		t.Fatalf("%v, %q\n%s", err, run.Outcome.Status, run.Raw)
	}
	check := func(where string, data []byte) {
		t.Helper()
		for _, secret := range []string{"stream-yaml-comment-secret", "stream-toml-comment-secret"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s: %q survived", where, secret)
			}
		}
		for _, keep := range []string{"visible-before", "visible-after", "visible-last"} {
			if !strings.Contains(string(data), keep) {
				t.Errorf("%s: unrelated output %q was lost", where, keep)
			}
		}
		passed := false
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ev struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &ev) != nil {
				t.Errorf("%s: a line is no longer JSON: %q", where, line)
			}
			passed = passed || ev.Action == "pass" && ev.Test == "TestPrinted"
		}
		if !passed {
			t.Errorf("%s: the test's verdict is no longer readable", where)
		}
	}
	r := NewRedactor()
	check("redacted directly", r.Redact(run.Raw))
	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), run.Raw, 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	if err != nil {
		t.Skipf("no tar: %v", err)
	}
	check("in the sealed bundle", stored)
}

// The collection reader, by itself: where a bracketed value ends, under
// YAML's rules (after "key:") and under TOML's (after "key =").
func TestContainerEnd(t *testing.T) {
	const both = "both"
	for _, tt := range []struct{ name, syntax, in, rest string }{
		{"plain", both, `[1, 2] tail`, " tail"},
		{"nested", both, `[[1], {a: [2]}] tail`, " tail"},
		{"a bracket in a string", both, `["]", ']'] tail`, " tail"},
		{"an escaped quote in a double-quoted string", both, `["a\"]", 1] tail`, " tail"},
		{"a backslash ending a single-quoted string", both, `['C:\', 1] tail`, " tail"},
		{"a doubled apostrophe", "yaml", `['it''s ]', 1] tail`, " tail"},
		{"a comment holding a bracket", both, "[\n # ] no\n 1] tail", " tail"},
		{"a comment holding a quote", both, "[1, # it's \" odd\n 2] tail", " tail"},
		{"a comment right after a bracket", both, "[# ]\n1] tail", " tail"},
		{"a hash inside a string is no comment", both, `["a # ]", 1] tail`, " tail"},
		{"a multi-line string holding a bracket", both, "[\"\"\"\n] x\n\"\"\", 1] tail", " tail"},
		{"a literal multi-line string", both, "['''\n] ' x\n''', 1] tail", " tail"},
		{"never closed", both, `[1, 2`, ""},
		// A multi-line string closing on a run of quotes, then a string
		// that holds a bracket.
		{"three quotes close a basic string", "toml", "[\"\"\"head\"\"\", \"a]b\"] tail", " tail"},
		{"four quotes close a basic string", "toml", "[\"\"\"head\"\"\"\", \"a]b\"] tail", " tail"},
		{"five quotes close a basic string", "toml", "[\"\"\"head\"\"\"\"\", \"a]b\"] tail", " tail"},
		{"four quotes close a literal string", "toml", "['''head'''', 'a]b'] tail", " tail"},
		{"five quotes close a literal string", "toml", "['''head''''', 'a]b'] tail", " tail"},
		{"four quotes over lines", "toml", "[\"\"\"\nhead\"\"\"\", \"a]b\"] tail", " tail"},
		{"an escaped quote, then the closing run", "toml", "[\"\"\"head\\\"\"\"\"\", \"a]b\"] tail", " tail"},
		// Where the two formats differ: a "#" straight after a bare value.
		{"YAML: a hash inside a word is part of the word", "yaml", `[a#b] tail`, " tail"},
		{"YAML: so a bracket after it on the line is syntax", "yaml", "[a#b]\n tail", "\n tail"},
		{"TOML: a hash after a number starts a comment", "toml", "[1# ]\n, 2] tail", " tail"},
		{"TOML: after a boolean, a float and a date too", "toml", "[true# ]\n, 1.5# }\n, 2026-10-09# \"\n, 3] tail", " tail"},
		{"TOML: a comment to the end means it never closed", "toml", "[1# ] the rest", ""},
	} {
		for _, syntax := range []string{"yaml", "toml"} {
			if tt.syntax != both && tt.syntax != syntax {
				continue
			}
			if got := tt.in[containerEnd(tt.in, syntax == "toml"):]; got != tt.rest {
				t.Errorf("%s (%s rules): after %q comes %q, want %q", tt.name, syntax, tt.in, got, tt.rest)
			}
		}
	}
}

// YAML's own rule is kept: a "#" inside a word is the word, and the value
// is removed with it, to its real end.
func TestRedactionKeepsYAMLCommentRules(t *testing.T) {
	in := "cookies: [a#b-hash-secret]\nnext: ok\nlist: [c#d, visible-item]\n"
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(in), &parsed); err != nil {
		t.Fatalf("the fixture is not valid YAML: %v", err)
	}
	for where, text := range sinksOf(t, in) {
		if strings.Contains(text, "b-hash-secret") {
			t.Errorf("%s: the value survived:\n%s", where, text)
		}
		if (where == "redacted directly" || where == "sealed player.log") && (!strings.Contains(text, "next: ok") || !strings.Contains(text, "list: [c#d, visible-item]")) {
			t.Errorf("%s: what follows the value, or an unrelated list, was changed:\n%s", where, text)
		}
	}
}

// Round 8, H5: a real test printing arrays whose first item is a multi-line
// string that closes on four quotes, followed by a credential that holds a
// bracket.
func TestRedactionOfRealGoTestQuoteRunsInCollections(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go command")
	}
	module := goModule(t, "package repro\nimport (\"fmt\"; \"testing\")\nfunc TestPrinted(t *testing.T) {\n"+
		"\tfmt.Print(\"before = \\\"visible-before\\\"\\ncookies = [\\\"\\\"\\\"head\\\"\\\"\\\"\\\", \\\"one]stream-quote-run-secret\\\"]\\nafter = \\\"visible-after-run\\\"\\n\")\n"+
		"\tfmt.Print(\"cookies = ['''\\nhead'''', 'one]stream-literal-run-secret']\\nfinal = \\\"visible-final\\\"\\n\")\n}\n")
	run, err := RunGoTest(module, ".", "TestPrinted", nil)
	if err != nil || run.Outcome.Status != "pass" {
		t.Fatalf("%v, %q\n%s", err, run.Outcome.Status, run.Raw)
	}
	if !strings.Contains(string(run.Raw), "stream-quote-run-secret") || !strings.Contains(string(run.Raw), "stream-literal-run-secret") {
		t.Fatalf("the test did not print the fixtures:\n%s", run.Raw)
	}
	check := func(where string, data []byte) {
		t.Helper()
		for _, secret := range []string{"stream-quote-run-secret", "stream-literal-run-secret"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s: %q survived", where, secret)
			}
		}
		for _, keep := range []string{"visible-before", "visible-after-run", "visible-final"} {
			if !strings.Contains(string(data), keep) {
				t.Errorf("%s: unrelated output %q was lost", where, keep)
			}
		}
		passed := false
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ev struct{ Action, Test string }
			if json.Unmarshal([]byte(line), &ev) != nil {
				t.Errorf("%s: a line is no longer JSON: %q", where, line)
			}
			passed = passed || ev.Action == "pass" && ev.Test == "TestPrinted"
		}
		if !passed {
			t.Errorf("%s: the test's verdict is no longer readable", where)
		}
	}
	r := NewRedactor()
	check("redacted directly", r.Redact(run.Raw))
	b, err := NewBundle(t.TempDir(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(b.Dir, "go-test-root.jsonl"), run.Raw, 0o600)
	sealed, err := b.Seal(r)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := exec.Command("tar", "-xzOf", sealed.Archive, "run-1/go-test-root.jsonl").Output()
	if err != nil {
		t.Skipf("no tar: %v", err)
	}
	check("in the sealed bundle", stored)
}
