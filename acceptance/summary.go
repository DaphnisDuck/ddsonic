package acceptance

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Summary renders the concise acceptance summary that goes into a release
// record. It reports execution, severity and disposition in separate
// columns, and lists what was not done as plainly as what was.
//
// The text passes through r before it is returned, the same redaction as
// the evidence bundle's files: a failure's detail quotes what the player and
// the tools printed, and a summary is persisted and shown.
//
// Every field is redacted whole, as it was recorded, before it is folded
// onto one line, escaped or cut to a cell's length: cutting first would take
// the closing quote off a long credential and leave the redactor nothing to
// recognise. The finished text is redacted once more for good measure.
func Summary(id Identity, c *Catalog, problems []Problem, v Verdict, sealed Sealed, r *Redactor) string {
	return r.RedactString(summary(id, c, problems, v, sealed, r))
}

func summary(id Identity, c *Catalog, problems []Problem, v Verdict, sealed Sealed, r *Redactor) string {
	// cell and pairs shadow the package's formatters with ones that redact
	// their source first; nothing below reaches the text unredacted.
	cell := func(s string) string { return foldCell(r.RedactString(s)) }
	pairs := func(m map[string]string) string { return foldPairs(m, cell) }
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("## Acceptance")
	line("")
	if id.Mode != ModeRelease {
		line("**A development run. It is not release evidence:** it ran a local build, not a release archive's binary.")
		line("")
	}
	line("| What | Value |")
	line("|---|---|")
	line("| Mode | %s |", id.Mode)
	if id.ExpectedVersion != "" {
		line("| Expected version | `%s` |", id.ExpectedVersion)
	}
	line("| Version the binary reports | `%s` |", id.ReportedVersion)
	line("| Source commit | `%s` (%s) |", id.SourceSHA, id.SourceCheck)
	if id.Mode == ModeRelease {
		line("| Commit in the archive's manifest | `%s` |", id.ManifestSourceSHA)
		line("| Commit the binary reports | `%s` |", id.BinarySourceSHA)
		line("| Built with | %s (the manifest and the binary's build information agree) |", cell(id.ReleaseGoVersion))
	}
	if id.ArchiveName != "" {
		line("| Archive | `%s`, SHA-256 `%s`, as SHA256SUMS lists it |", id.ArchiveName, id.ArchiveSHA256)
	}
	line("| Binary under test | SHA-256 `%s` |", id.BinarySHA256)
	line("| Harness revision | `%s` |", id.HarnessRevision)
	line("| Case catalog | SHA-256 `%s` |", id.CatalogSHA256)
	line("| Network isolation | %s |", cell(id.Environment["network isolation"]))
	environment := map[string]string{}
	for k, v := range id.Environment {
		if k != "network isolation" { // it has its own row
			environment[k] = v
		}
	}
	line("| Environment | %s |", pairs(environment))
	line("| Tools | %s |", pairs(id.Tools))
	line("")

	// Totals, by execution result, over every case.
	counts := map[Execution]int{}
	for _, row := range v.Rows {
		counts[row.Result.Execution]++
	}
	var totals []string
	for _, e := range executions {
		totals = append(totals, fmt.Sprintf("%d %s", counts[e], e))
	}
	line("**%d cases:** %s.", len(v.Rows), strings.Join(totals, ", "))
	line("")

	// What ran, against what is only cataloged.
	line("| Lane | Cases | Passed | Not passed |")
	line("|---|---|---|---|")
	for _, lane := range []struct{ name, label string }{
		{LaneBinary, "Scripted, on the binary under test"},
		{LaneGo, "Scripted, in-process on the checkout's source"},
		{LaneManual + "/" + TierDeterministic, "Manual, deterministic (cataloged; executed by hand)"},
		{LaneManual + "/" + TierLive, "Manual, live providers (cataloged; executed by hand)"},
	} {
		total, passed := 0, 0
		for _, row := range v.Rows {
			key := row.Case.Lane
			if key == LaneManual {
				key += "/" + row.Case.Tier
			}
			if key != lane.name {
				continue
			}
			total++
			if row.Result.Execution == Pass {
				passed++
			}
		}
		line("| %s | %d | %d | %d |", lane.label, total, passed, total-passed)
	}
	line("")

	var notPassed, notApplicable, unimplemented, waivers []Row
	for _, row := range v.Rows {
		switch {
		case row.Result.Execution == NotApplicable && row.Blocks == "":
			// Inapplicable as the catalog allows. One the catalog does not
			// allow is not a pass and is listed with the rest below.
			notApplicable = append(notApplicable, row)
		case row.Result.Execution != Pass:
			notPassed = append(notPassed, row)
		}
		if row.Case.Driver == DriverScripted && !row.Case.Implemented {
			unimplemented = append(unimplemented, row)
		}
		if row.Triage != nil && row.Triage.Waiver != nil {
			waivers = append(waivers, row)
		}
	}

	if len(notPassed) > 0 {
		line("**Not passed** (execution, severity and disposition are separate judgements):")
		line("")
		line("| Case | Gate | Execution | Severity | Disposition | Blocks publication | Why |")
		line("|---|---|---|---|---|---|---|")
		for _, row := range notPassed {
			severity, disposition := "—", "—"
			if row.Triage != nil {
				severity, disposition = row.Triage.Severity, row.Triage.Disposition
			}
			blocks := "no"
			if row.Blocks != "" {
				blocks = "yes"
				if !row.Case.Required() {
					blocks = "no (advisory)"
				}
			}
			line("| %s %s | %s | %s | %s | %s | %s | %s |", row.Case.ID, cell(row.Case.Title), row.Case.Gate, row.Result.Execution, severity, disposition, blocks, cell(row.Blocks))
		}
		line("")
	}
	// Why the blocked cases could not reach a verdict, each reason once.
	var reasons []string
	blockedBy := map[string][]string{}
	for _, row := range notPassed {
		if reason := row.Result.EnvironmentLimitation; row.Result.Execution == Blocked && reason != "" {
			if blockedBy[reason] == nil {
				reasons = append(reasons, reason)
			}
			blockedBy[reason] = append(blockedBy[reason], row.Case.ID)
		}
	}
	if len(reasons) > 0 {
		line("**Blocked, and why** (a blocked case is not a pass):")
		for _, reason := range reasons {
			line("- %s: %s", strings.Join(blockedBy[reason], ", "), cell(reason))
		}
		line("")
	}
	if len(notApplicable) > 0 {
		line("**Not applicable:**")
		for _, row := range notApplicable {
			line("- %s: %s", row.Case.ID, cell(row.Result.Detail))
		}
		line("")
	}
	if len(unimplemented) > 0 {
		ids := make([]string, len(unimplemented))
		for i, row := range unimplemented {
			ids[i] = row.Case.ID
		}
		line("**Scripted cases with no runner yet** (they cannot pass): %s.", strings.Join(ids, ", "))
		line("")
	}
	if len(waivers) > 0 {
		line("**Waivers:**")
		for _, row := range waivers {
			w := row.Triage.Waiver
			line("- %s (%s): approved by %s on %s; %s", row.Case.ID, row.Triage.Severity, w.ApprovedBy, w.Date, cell(w.Reference))
		}
		line("")
	}
	if len(problems) > 0 {
		line("**Catalog problems:**")
		for _, p := range problems {
			line("- %s", cell(p.String()))
		}
		line("")
	}

	if v.Publishable() {
		line("**Gate: nothing in this acceptance evidence blocks publication.**")
	} else {
		line("**Gate: publication is blocked.**")
		if rows := len(v.Blocking) - len(v.General); rows > 0 {
			line("- %d release-gating cases block it: the rows marked \"yes\" above, each for the reason its columns give.", rows)
		}
		for _, reason := range v.General {
			line("- %s", cell(reason))
		}
	}
	line("")
	if sealed.Archive != "" {
		line("Evidence bundle (redacted, outside git): `%s`, SHA-256 `%s`.", filepath.Base(sealed.Archive), sealed.SHA256)
	} else {
		line("No evidence bundle was sealed for this summary.")
	}
	return b.String()
}

// foldPairs renders a map as "key: value; key: value", in key order.
func foldPairs(m map[string]string, cell func(string) string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + cell(m[k])
	}
	return strings.Join(parts, "; ")
}

// foldCell makes text that is already redacted fit one table cell or list
// item: one line, no pipes, no longer than a cell.
func foldCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}
