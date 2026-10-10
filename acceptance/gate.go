package acceptance

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Execution is what happened when a case was run. It says nothing about how
// much a failure matters or what will be done about it: those are Triage.
type Execution string

const (
	Pass          Execution = "pass"
	Fail          Execution = "fail"
	Blocked       Execution = "blocked" // could not be run to a verdict; never a pass
	NotRun        Execution = "not run"
	NotApplicable Execution = "not applicable"
)

var executions = []Execution{Pass, Fail, Blocked, NotRun, NotApplicable}

// Severities and dispositions, as the release plan defines them.
const (
	SeverityBlocker = "BLOCKER"
	SeverityHigh    = "HIGH"
	SeverityMedium  = "MEDIUM"
	SeverityLow     = "LOW"
	SeverityNit     = "NIT"

	DispositionFix         = "fix"
	DispositionWaiver      = "accepted waiver"
	DispositionDefer       = "defer"
	DispositionInvestigate = "investigate"
)

var (
	severities   = []string{SeverityBlocker, SeverityHigh, SeverityMedium, SeverityLow, SeverityNit}
	dispositions = []string{DispositionFix, DispositionWaiver, DispositionDefer, DispositionInvestigate}
)

// WaiverApprover is the only person who approves a waiver.
const WaiverApprover = "Steve"

// Result is one case's execution result in one run.
type Result struct {
	CaseID    string    `json:"case_id" yaml:"case_id"`
	Execution Execution `json:"execution" yaml:"execution"`
	// Detail says what was observed: a failure's message, or the reason a
	// case is not applicable.
	Detail string `json:"detail,omitempty" yaml:"detail"`
	// EnvironmentLimitation explains a blocked result. It never changes it.
	EnvironmentLimitation string   `json:"environment_limitation,omitempty" yaml:"environment_limitation"`
	ExecutedBy            string   `json:"executed_by,omitempty" yaml:"executed_by"`
	ExecutedAt            string   `json:"executed_at,omitempty" yaml:"executed_at"`
	DurationSeconds       float64  `json:"duration_seconds,omitempty" yaml:"duration_seconds"`
	Evidence              []string `json:"evidence,omitempty" yaml:"evidence"`
}

// Triage is the judgement on a case that did not pass: how much it matters
// and what happens next. It is kept apart from the Result, which it can
// never turn into a pass.
type Triage struct {
	CaseID      string  `json:"case_id" yaml:"case_id"`
	Severity    string  `json:"severity" yaml:"severity"`
	Disposition string  `json:"disposition" yaml:"disposition"`
	Rationale   string  `json:"rationale,omitempty" yaml:"rationale"`
	Waiver      *Waiver `json:"waiver,omitempty" yaml:"waiver"`
}

// Waiver records the owner's written approval to publish with a finding open.
type Waiver struct {
	ApprovedBy string `json:"approved_by" yaml:"approved_by"`
	Date       string `json:"date" yaml:"date"`
	// Reference says where the written approval is: it must be findable.
	Reference string `json:"reference" yaml:"reference"`
}

// Candidate is what a hand-recorded result says it was observed on. It must
// be the candidate the run identified, exactly: a result recorded against
// another build, or against nothing in particular, proves nothing here.
type Candidate struct {
	Version       string `yaml:"version"`
	SourceSHA     string `yaml:"source_sha"`
	ArchiveSHA256 string `yaml:"archive_sha256"`
	BinarySHA256  string `yaml:"binary_sha256"`
}

// Manual is a file of results a person or an agent recorded by hand.
type Manual struct {
	Candidate Candidate `yaml:"candidate"`
	Results   []Result  `yaml:"results"`
}

// LoadManual reads hand-recorded results (YAML).
func LoadManual(path string) (Manual, error) {
	var m Manual
	return m, loadYAML(path, &m)
}

// Accept returns the results of m that may be taken for the candidate id,
// and what is wrong with the rest. The whole file is refused unless it names
// exactly the candidate under test: its version, source commit, archive
// checksum and binary checksum. A result is refused when it is for a case
// that is not manual, does not say who executed it, or carries a time that
// cannot be read or is later than now, by any margin. How old a result may
// be is not judged: no such policy is configured.
func (m Manual) Accept(id Identity, c *Catalog, now time.Time) (accepted []Result, problems []string) {
	if id.Mode != ModeRelease {
		return nil, []string{"hand-recorded results were given to a development run: they are bound to a release candidate, and this is not one"}
	}
	for _, f := range []struct{ name, got, want string }{
		{"version", m.Candidate.Version, id.ExpectedVersion},
		{"source_sha", m.Candidate.SourceSHA, id.SourceSHA},
		{"archive_sha256", m.Candidate.ArchiveSHA256, id.ArchiveSHA256},
		{"binary_sha256", m.Candidate.BinarySHA256, id.BinarySHA256},
	} {
		switch {
		case strings.TrimSpace(f.got) == "":
			problems = append(problems, fmt.Sprintf("hand-recorded results do not say which candidate they are for: candidate.%s is missing", f.name))
		case f.want == "" || f.got != f.want:
			// Exact, to the character: a version in another case, a checksum
			// in capitals or an abbreviated commit is another string, and
			// what it was recorded on is then not established.
			problems = append(problems, fmt.Sprintf("hand-recorded results are for another candidate: candidate.%s is %s, and the candidate under test has %s", f.name, f.got, f.want))
		}
	}
	if len(problems) > 0 {
		return nil, problems
	}
	cases := map[string]Case{}
	for _, cs := range c.Cases {
		cases[cs.ID] = cs
	}
	for _, r := range m.Results {
		cs, known := cases[r.CaseID]
		at, timeErr := time.Parse(time.RFC3339, r.ExecutedAt)
		switch {
		case !known:
			problems = append(problems, fmt.Sprintf("a result was recorded by hand for %q, which is not a catalog case", r.CaseID))
		case cs.Driver != DriverManual:
			problems = append(problems, fmt.Sprintf("%s: a result was recorded by hand for a scripted case", r.CaseID))
		case strings.TrimSpace(r.ExecutedBy) == "":
			problems = append(problems, fmt.Sprintf("%s: a hand-recorded result does not say who executed it", r.CaseID))
		case timeErr != nil:
			problems = append(problems, fmt.Sprintf("%s: executed_at %q is not a time (RFC 3339, such as 2026-10-12T15:00:00Z)", r.CaseID, r.ExecutedAt))
		case at.After(now):
			problems = append(problems, fmt.Sprintf("%s: executed_at %s has not happened yet", r.CaseID, r.ExecutedAt))
		default:
			accepted = append(accepted, r)
		}
	}
	return accepted, problems
}

// LoadTriage reads the triage file (YAML).
func LoadTriage(path string) ([]Triage, error) {
	var file struct {
		Triage []Triage `yaml:"triage"`
	}
	if err := loadYAML(path, &file); err != nil {
		return nil, err
	}
	return file.Triage, nil
}

func loadYAML(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := decodeYAML(data, into); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// WriteJSON writes v to path, indented.
func WriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

// Row is one case with its result and triage, as the gate judged it.
type Row struct {
	Case   Case
	Result Result
	Triage *Triage
	// Blocks says why this row blocks publication; empty when it does not.
	Blocks string
}

// Verdict is the gate's answer for one run.
type Verdict struct {
	Rows []Row
	// Blocking lists every reason publication is blocked. General holds
	// those among them that belong to no one case's row: an invalid
	// catalog, a result for no case, the caller's own findings.
	Blocking []string
	General  []string
}

// Publishable reports whether nothing in the acceptance evidence blocks
// publication. It is necessary, not sufficient: a release has other gates.
func (v Verdict) Publishable() bool { return len(v.Blocking) == 0 }

// Evaluate applies the release gate. Every case gets exactly one row: a
// case with no result is "not run". Problems with the catalog, the results
// or the triage that concern a gating case block publication, as does every
// entry of blockers (the caller's own findings, such as an artifact that
// could not be identified).
//
// The rules, for a release-gating case that did not pass:
//   - it must be triaged (a severity and a disposition);
//   - BLOCKER and HIGH block;
//   - MEDIUM blocks unless the owner's written waiver is recorded;
//   - LOW and NIT do not block;
//   - "investigate" blocks at any severity: it is not a state to publish in.
//
// "blocked" and "not run" are not passes, whatever the reason.
func Evaluate(c *Catalog, problems []Problem, results []Result, triage []Triage, target Target, blockers ...string) Verdict {
	var v Verdict
	v.Blocking = append(v.Blocking, blockers...)
	for _, p := range problems {
		if p.Gating {
			v.Blocking = append(v.Blocking, "catalog invalid: "+p.String())
		}
	}

	cases := map[string]*Case{}
	for i := range c.Cases {
		cases[c.Cases[i].ID] = &c.Cases[i]
	}
	byCase := map[string]Result{}
	for _, r := range results {
		switch {
		case cases[r.CaseID] == nil:
			v.Blocking = append(v.Blocking, fmt.Sprintf("result for %q, which is not a catalog case", r.CaseID))
		case byCase[r.CaseID].CaseID != "":
			v.Blocking = append(v.Blocking, fmt.Sprintf("%s has more than one result", r.CaseID))
		default:
			if invalid := invalidatedPass(&r); invalid != "" && cases[r.CaseID].Required() {
				// The record itself was invalid. That stands beside the
				// row: triaging the now-blocked case does not remove it.
				v.Blocking = append(v.Blocking, r.CaseID+": "+invalid)
			}
			byCase[r.CaseID] = r
		}
	}
	triaged := map[string]*Triage{}
	for i := range triage {
		t := &triage[i]
		switch {
		case cases[t.CaseID] == nil:
			v.Blocking = append(v.Blocking, fmt.Sprintf("triage for %q, which is not a catalog case", t.CaseID))
		case triaged[t.CaseID] != nil:
			v.Blocking = append(v.Blocking, fmt.Sprintf("%s is triaged more than once", t.CaseID))
		default:
			triaged[t.CaseID] = t
		}
	}

	v.General = slices.Clone(v.Blocking)
	for _, cs := range c.Cases {
		row := Row{Case: cs, Result: byCase[cs.ID], Triage: triaged[cs.ID]}
		if row.Result.CaseID == "" {
			row.Result = Result{CaseID: cs.ID, Execution: NotRun, Detail: "no result recorded"}
		}
		row.Blocks = judge(row, target, byCase)
		if row.Blocks != "" && cs.Required() {
			v.Blocking = append(v.Blocking, cs.ID+": "+row.Blocks)
		}
		v.Rows = append(v.Rows, row)
	}
	return v
}

// judge returns why a row blocks publication, or "".
func judge(row Row, target Target, results map[string]Result) string {
	r, t := row.Result, row.Triage
	if !slices.Contains(executions, r.Execution) {
		return fmt.Sprintf("execution %q is not one of pass, fail, blocked, not run, not applicable", r.Execution)
	}
	switch r.Execution {
	case Pass:
		if t != nil {
			return "a passing case carries no triage: a result is never triaged into a pass"
		}
		return ""
	case NotApplicable:
		if msg := inapplicable(row.Case, target, results); msg != "" {
			return msg
		}
		if strings.TrimSpace(r.Detail) == "" {
			return "not applicable, with no reason given"
		}
		if t != nil {
			return "a case that is not applicable carries no triage"
		}
		return ""
	}

	if t == nil {
		return fmt.Sprintf("%s, and not triaged (no severity or disposition)", r.Execution)
	}
	if !slices.Contains(severities, t.Severity) {
		return fmt.Sprintf("severity %q is not one of %s", t.Severity, strings.Join(severities, ", "))
	}
	if !slices.Contains(dispositions, t.Disposition) {
		return fmt.Sprintf("disposition %q is not one of %s", t.Disposition, strings.Join(dispositions, ", "))
	}
	if t.Waiver != nil && t.Disposition != DispositionWaiver {
		return fmt.Sprintf("a waiver is recorded but the disposition is %q", t.Disposition)
	}
	if t.Disposition == DispositionWaiver {
		if msg := checkWaiver(t.Waiver); msg != "" {
			return msg
		}
	}
	if t.Disposition == DispositionInvestigate {
		return fmt.Sprintf("%s at %s, still under investigation", r.Execution, t.Severity)
	}
	switch t.Severity {
	case SeverityBlocker, SeverityHigh:
		return fmt.Sprintf("%s at %s: it blocks publication and cannot be waived", r.Execution, t.Severity)
	case SeverityMedium:
		if t.Disposition != DispositionWaiver {
			return fmt.Sprintf("%s at MEDIUM with disposition %q: an unfixed MEDIUM needs %s's written waiver", r.Execution, t.Disposition, WaiverApprover)
		}
	}
	return ""
}

func checkWaiver(w *Waiver) string {
	switch {
	case w == nil:
		return "disposition is accepted waiver, with no waiver recorded"
	case w.ApprovedBy != WaiverApprover:
		return fmt.Sprintf("waiver approved by %q: only %s approves a waiver", w.ApprovedBy, WaiverApprover)
	case strings.TrimSpace(w.Reference) == "" || strings.TrimSpace(w.Date) == "":
		return "waiver lacks its date or the reference to the written approval"
	}
	return ""
}

// inapplicable returns why a case may not be recorded "not applicable" for
// the candidate, or "". Only the catalog makes a case inapplicable: it must
// name this release or platform, and the cases it names as supplying the
// coverage instead must have been executed for this candidate themselves.
func inapplicable(cs Case, target Target, results map[string]Result) string {
	for _, in := range cs.Inapplicable {
		if !in.AppliesTo(target) {
			continue
		}
		for _, id := range in.CoveredBy {
			// Coverage is supplied by an execution that reached a verdict:
			// a pass, or a failure (which the gate then judges like any
			// other). A case that was not run, was blocked, does not apply
			// or has no valid result was not executed, and no triage of it
			// changes that.
			if covering := results[id]; covering.Execution != Pass && covering.Execution != Fail {
				state := string(covering.Execution)
				if state == "" {
					state = "no result"
				}
				return fmt.Sprintf("not applicable, but %s, which the catalog names as covering it, was not executed (%s): it supplies no coverage", id, state)
			}
		}
		return ""
	}
	return fmt.Sprintf("recorded as not applicable, but the catalog does not make this case inapplicable for %s on %s: it applies", target.Version, target.Platform)
}

// invalidatedPass turns a result recorded as a pass with an environment
// limitation into the blocked result it is, keeping what was recorded as
// its detail, and returns why; "" for any other result. A limitation is a
// note beside an execution that did not reach a verdict: an execution the
// environment undermined is blocked or failed, and is never counted,
// persisted or shown as a pass.
func invalidatedPass(r *Result) string {
	if r.Execution != Pass || strings.TrimSpace(r.EnvironmentLimitation) == "" {
		return ""
	}
	r.Execution = Blocked
	recorded := "recorded as a pass, and invalidated: it carries an environment limitation"
	if r.Detail != "" {
		recorded += "; its own detail: " + r.Detail
	}
	r.Detail = recorded
	return "a result recorded as a pass carried an environment limitation (" + r.EnvironmentLimitation + ") and was invalidated: the case is blocked"
}
