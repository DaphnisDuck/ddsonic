# Writer (ddsonic)

The Writer's procedures. `CLAUDE.md` holds the rules every role follows; the helper loads this file only into the Writer's session.

**Where to look:** the issue named in the task.

## Requesting a review

Run `make validate` as in the Validation quickstart (or `make validate PROFILE=` a higher class) and paste its `validate: PASSED class=...` line. Write the request into a prompt file (a private file outside the repo; it is data, and holds the issue/PR evidence). It carries:
- the owner's request verbatim, plus the confirmed acceptance criteria and constraints;
- the base commit and the files in the change;
- notes written as claims to check, never as "please verify my correct implementation": the entry points affected and the invariants the change preserves; the tests added or changed, by name, and the exact commands run with their results; for a cross-cutting fix, a caller matrix (every path that reaches the changed behavior and how each is handled); for performance questions, the benchmark command and its output; and what you are unsure of.

Ask only at coherent slice boundaries, not after every patch. Leave the working tree unchanged while the Reviewer is reviewing it.

## Triage

Give every finding exactly one disposition, meeting its criteria in `CLAUDE.md` (the loop). Dispute on the merits, never just to disagree. The verify round's prompt carries the original findings and a response covering every one.

## Staging a dev candidate

When a reviewed Linux change needs owner testing, stage the exact reviewed commit in `~/ddsonic-candidates/<label>-<sha7>/` as the earlier candidates are (`README.txt`, `SHA256SUMS`, the binary `ddsonic-<label>-<sha7>`, its `git archive` source) and verify it (the archive's commit, `--version`, `sha256sum -c`, permissions, `ldd`).
