// Package acceptance is ddsonic's release-acceptance tooling: the case
// catalog and its validation, the release gate, the summary for a release
// record, the identity of the artifact under test and the evidence bundle.
// It is not part of the player; nothing in the ddsonic binary imports it.
// See docs/ddsonic/acceptance.md.
package acceptance

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// CatalogPath is the case catalog, relative to the repository root.
const CatalogPath = "docs/ddsonic/acceptance/cases.yaml"

// The values each catalog field may take.
const (
	TierDeterministic = "deterministic"
	TierLive          = "live"

	DriverScripted = "scripted"
	DriverManual   = "manual"

	// A lane is where a case executes. LaneGo runs in-process against the
	// source tree; LaneBinary drives a built executable; LaneManual is a
	// person or an agent following a written procedure.
	LaneGo     = "go"
	LaneBinary = "binary"
	LaneManual = "manual"

	ReviewerAssertions = "assertions"
	ReviewerAgent      = "agent"
	ReviewerHuman      = "human"

	GateRequired = "required"
	GateAdvisory = "advisory"

	KindAuthoritative = "authoritative"
	KindDerived       = "derived"
	KindLive          = "live"
	KindLocal         = "local"
	KindGeneral       = "general"
)

var (
	tiers     = []string{TierDeterministic, TierLive}
	drivers   = []string{DriverScripted, DriverManual}
	lanes     = []string{LaneGo, LaneBinary, LaneManual}
	reviewers = []string{ReviewerAssertions, ReviewerAgent, ReviewerHuman}
	gates     = []string{GateRequired, GateAdvisory}
	kinds     = []string{KindAuthoritative, KindDerived, KindLive, KindLocal, KindGeneral}

	caseIDPattern      = regexp.MustCompile(`^[A-Z]{2,6}(-[A-Z]{2,6})?-[0-9]{3}$`)
	semanticsIDPattern = regexp.MustCompile(`^[a-z]+(/[a-z-]+)?$`)
	testFuncPattern    = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)
)

// Catalog is the case catalog: the semantics the cases are written against,
// and the cases.
type Catalog struct {
	Semantics []Semantics `yaml:"semantics"`
	Cases     []Case      `yaml:"cases"`
}

// Semantics is one row of the product-semantics oracle: what a visible list
// promises. docs/ddsonic/acceptance/semantics.md explains each in prose.
type Semantics struct {
	ID        string `yaml:"id"`
	Provider  string `yaml:"provider"`
	List      string `yaml:"list"`
	Kind      string `yaml:"kind"`
	Statement string `yaml:"statement"`
}

// Case is one acceptance case. It names what is checked and where its
// procedure lives; it holds no steps.
type Case struct {
	ID        string `yaml:"id"`
	Area      string `yaml:"area"`
	Title     string `yaml:"title"`
	Semantics string `yaml:"semantics"`
	Tier      string `yaml:"tier"`
	Driver    string `yaml:"driver"`
	Lane      string `yaml:"lane"`
	// Runner names a scripted case's Go test as "<package dir>:<TestName>",
	// the directory relative to the repository root ("." for the root).
	Runner string `yaml:"runner"`
	// Procedure names a manual case's written steps as "<doc>#<anchor>",
	// the doc relative to the catalog's directory.
	Procedure     string `yaml:"procedure"`
	Reviewer      string `yaml:"reviewer"`
	Applicability string `yaml:"applicability"`
	Gate          string `yaml:"gate"`
	// Implemented is false for a scripted case whose runner does not exist
	// yet. Such a case is reported as unimplemented and never passes.
	Implemented  bool   `yaml:"implemented"`
	Since        string `yaml:"since"`
	RegressionOf string `yaml:"regression_of"`
	// Inapplicable lists the only circumstances in which this case may be
	// recorded "not applicable". Without an entry that names the candidate's
	// release or platform, the case always applies: a result cannot declare
	// itself inapplicable.
	Inapplicable []Inapplicability `yaml:"inapplicable"`
}

// Inapplicability says for which candidates a case does not apply, why, and
// which other required cases supply the coverage it would have given.
type Inapplicability struct {
	// Releases and Platforms name the candidates: exact versions
	// ("v1.0.0-rc.4") and platforms ("darwin/arm64"). An empty list does not
	// restrict; at least one of the two must be given.
	Releases  []string `yaml:"releases"`
	Platforms []string `yaml:"platforms"`
	Reason    string   `yaml:"reason"`
	// CoveredBy names the required cases that supply the coverage instead.
	CoveredBy []string `yaml:"covered_by"`
}

// AppliesTo reports whether the entry names this candidate.
func (in Inapplicability) AppliesTo(t Target) bool {
	if len(in.Releases) == 0 && len(in.Platforms) == 0 {
		return false
	}
	return (len(in.Releases) == 0 || slices.Contains(in.Releases, t.Version)) &&
		(len(in.Platforms) == 0 || slices.Contains(in.Platforms, t.Platform))
}

// Target is the candidate a run judges: its release version and platform.
type Target struct {
	Version  string
	Platform string // GOOS/GOARCH
}

// Required reports whether the case gates a release.
func (c Case) Required() bool { return c.Gate != GateAdvisory }

// RunnerParts splits Runner into its package directory and test name.
func (c Case) RunnerParts() (dir, test string, ok bool) {
	dir, test, ok = strings.Cut(c.Runner, ":")
	return dir, test, ok && dir != "" && strings.HasPrefix(test, "Test")
}

// Problem is one thing wrong with the catalog. Gating is true when it
// concerns a release-gating case or the catalog as a whole: such a problem
// fails the gate, whatever the results say.
type Problem struct {
	CaseID string
	Gating bool
	Text   string
}

func (p Problem) String() string {
	if p.CaseID == "" {
		return "catalog: " + p.Text
	}
	return p.CaseID + ": " + p.Text
}

// LoadCatalog reads the catalog under the repository at root. A file that
// cannot be read or parsed, or that holds a field the schema does not know,
// is an error: an unreadable catalog validates nothing.
func LoadCatalog(root string) (*Catalog, error) {
	data, err := os.ReadFile(filepath.Join(root, CatalogPath))
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	return ParseCatalog(data)
}

// ParseCatalog parses catalog YAML, refusing unknown fields and anything
// after the first document.
func ParseCatalog(data []byte) (*Catalog, error) {
	var c Catalog
	if err := decodeYAML(data, &c); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	return &c, nil
}

// decodeYAML decodes the one YAML document in data into v, strictly: a
// field v does not have is an error, and so is a second document, which a
// plain decode would leave unread and unvalidated.
func decodeYAML(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("after the first document: %w", err)
		}
		return errors.New("more than one YAML document: only one is read, so a second is refused")
	}
	return nil
}

// Validate checks the catalog against the repository at root: fields and
// their values, that every scripted case's runner is a test that exists and
// is named by one case only, that every manual case's procedure is a heading
// that exists, and that no test in a scenario directory is left without a
// case. With root empty, the checks that read the repository are skipped.
func (c *Catalog) Validate(root string, scenarioDirs ...string) []Problem {
	var problems []Problem
	add := func(cs *Case, format string, args ...any) {
		p := Problem{Gating: true, Text: fmt.Sprintf(format, args...)}
		if cs != nil {
			// A case whose gate is unreadable is treated as gating.
			p.CaseID, p.Gating = cs.ID, cs.Gate != GateAdvisory
		}
		problems = append(problems, p)
	}

	if len(c.Cases) == 0 {
		add(nil, "no cases")
	}
	semantics := map[string]bool{}
	for _, s := range c.Semantics {
		switch {
		case !semanticsIDPattern.MatchString(s.ID):
			add(nil, "semantics id %q is not of the form provider/list", s.ID)
		case semantics[s.ID]:
			add(nil, "semantics id %q is defined twice", s.ID)
		}
		semantics[s.ID] = true
		if !slices.Contains(kinds, s.Kind) {
			add(nil, "semantics %q: kind %q is not one of %s", s.ID, s.Kind, strings.Join(kinds, ", "))
		}
		if strings.TrimSpace(s.Statement) == "" {
			add(nil, "semantics %q: no statement", s.ID)
		}
	}

	ids, runners := map[string]bool{}, map[string]string{}
	for i := range c.Cases {
		cs := &c.Cases[i]
		if !caseIDPattern.MatchString(cs.ID) {
			add(nil, "case %d: id %q is not of the form AREA-000 or AREA-PART-000", i+1, cs.ID)
		} else if ids[cs.ID] {
			add(nil, "id %s is used twice", cs.ID)
		}
		ids[cs.ID] = true

		for name, value := range map[string]string{
			"area": cs.Area, "title": cs.Title, "semantics": cs.Semantics,
			"applicability": cs.Applicability, "since": cs.Since,
		} {
			if strings.TrimSpace(value) == "" {
				add(cs, "%s is missing", name)
			}
		}
		for _, f := range []struct {
			name, value string
			allowed     []string
		}{
			{"tier", cs.Tier, tiers}, {"driver", cs.Driver, drivers}, {"lane", cs.Lane, lanes},
			{"reviewer", cs.Reviewer, reviewers}, {"gate", cs.Gate, gates},
		} {
			if !slices.Contains(f.allowed, f.value) {
				add(cs, "%s %q is not one of %s", f.name, f.value, strings.Join(f.allowed, ", "))
			}
		}
		if cs.Semantics != "" && !semantics[cs.Semantics] {
			add(cs, "semantics %q is not a row of the catalog's semantics", cs.Semantics)
		}

		switch cs.Driver {
		case DriverScripted:
			if cs.Lane == LaneManual {
				add(cs, "a scripted case needs lane go or binary")
			}
			if cs.Tier == TierLive {
				add(cs, "a live case is manual: scripted cases never use real accounts")
			}
			if cs.Procedure != "" {
				add(cs, "a scripted case has a runner, not a procedure")
			}
			problems = append(problems, c.checkRunner(cs, root, runners)...)
		case DriverManual:
			if cs.Lane != LaneManual {
				add(cs, "a manual case needs lane manual")
			}
			if cs.Runner != "" {
				add(cs, "a manual case has a procedure, not a runner")
			}
			if cs.Implemented {
				add(cs, "implemented is for scripted cases: a manual case is executed, not implemented")
			}
			if msg := checkProcedure(cs.Procedure, root); msg != "" {
				add(cs, "%s", msg)
			}
		}
	}

	// Inapplicability is the catalog's to define, and never without another
	// required case that supplies the coverage.
	byID := map[string]*Case{}
	for i := range c.Cases {
		byID[c.Cases[i].ID] = &c.Cases[i]
	}
	for i := range c.Cases {
		cs := &c.Cases[i]
		for n, in := range cs.Inapplicable {
			if len(in.Releases) == 0 && len(in.Platforms) == 0 {
				add(cs, "inapplicable entry %d names no release and no platform", n+1)
			}
			if strings.TrimSpace(in.Reason) == "" {
				add(cs, "inapplicable entry %d gives no reason", n+1)
			}
			if len(in.CoveredBy) == 0 {
				add(cs, "inapplicable entry %d names no case that supplies the coverage instead", n+1)
			}
			for _, id := range in.CoveredBy {
				other := byID[id]
				switch {
				case other == nil:
					add(cs, "inapplicable entry %d: covering case %q is not in the catalog", n+1, id)
				case other == cs:
					add(cs, "inapplicable entry %d: a case cannot cover itself", n+1)
				case !other.Required():
					add(cs, "inapplicable entry %d: covering case %s is advisory; the coverage must be required", n+1, id)
				case len(other.Inapplicable) > 0:
					add(cs, "inapplicable entry %d: covering case %s can itself be inapplicable", n+1, id)
				}
			}
		}
	}

	// A scenario test nobody names would run in no case and report nowhere.
	for _, dir := range scenarioDirs {
		if root == "" {
			break
		}
		tests, err := testsIn(filepath.Join(root, dir))
		if err != nil {
			add(nil, "scenario directory %s: %v", dir, err)
			continue
		}
		for _, name := range tests {
			if _, named := runners[dir+":"+name]; !named {
				add(nil, "test %s in %s is named by no case", name, dir)
			}
		}
	}
	return problems
}

// checkRunner validates a scripted case's runner and records it in runners.
func (c *Catalog) checkRunner(cs *Case, root string, runners map[string]string) []Problem {
	var problems []Problem
	add := func(format string, args ...any) {
		problems = append(problems, Problem{CaseID: cs.ID, Gating: cs.Gate != GateAdvisory, Text: fmt.Sprintf(format, args...)})
	}
	if !cs.Implemented {
		if cs.Runner != "" {
			add("an unimplemented case names no runner yet")
		}
		return problems
	}
	dir, test, ok := cs.RunnerParts()
	if !ok {
		add("runner %q is not of the form <package dir>:<TestName>", cs.Runner)
		return problems
	}
	if other, taken := runners[cs.Runner]; taken {
		add("runner %s is already named by %s", cs.Runner, other)
	}
	runners[cs.Runner] = cs.ID
	if root == "" {
		return problems
	}
	tests, err := testsIn(filepath.Join(root, dir))
	if err != nil {
		add("runner %s: %v", cs.Runner, err)
	} else if !slices.Contains(tests, test) {
		add("runner %s: no such test in %s", cs.Runner, dir)
	}
	return problems
}

// checkProcedure returns what is wrong with a manual case's procedure, or "".
func checkProcedure(procedure, root string) string {
	doc, anchor, ok := strings.Cut(procedure, "#")
	if !ok || doc == "" || anchor == "" {
		return fmt.Sprintf("procedure %q is not of the form <doc>#<anchor>", procedure)
	}
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.Dir(CatalogPath), doc))
	if err != nil {
		return fmt.Sprintf("procedure %s: %v", procedure, err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "#") && headingAnchor(line) == anchor {
			return ""
		}
	}
	return fmt.Sprintf("procedure %s: %s has no heading with that anchor", procedure, doc)
}

// headingAnchor is the anchor a Markdown heading gets: lower case, spaces
// as hyphens, punctuation dropped.
func headingAnchor(heading string) string {
	text := strings.ToLower(strings.TrimSpace(strings.TrimLeft(heading, "#")))
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// testsIn lists the test functions declared in dir's test files.
func testsIn(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no test files in %s", dir)
	}
	var tests []string
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range testFuncPattern.FindAllSubmatch(data, -1) {
			tests = append(tests, string(m[1]))
		}
	}
	return tests, nil
}
