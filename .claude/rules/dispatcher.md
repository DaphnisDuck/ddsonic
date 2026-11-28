# Dispatcher (ddsonic)

The Dispatcher's procedures. `CLAUDE.md` holds the rules every role follows; the helper loads this file only into the Dispatcher's session.

## Waiting for an agent

Never end a turn inside an active review cycle without a tracked wait pending, except at an explicit owner-decision boundary; otherwise the cycle stalls until the owner types "status".

- Before relaying work to a managed agent (Dispatcher, Writer or Reviewer; a supplemental Codex round is not one), record its `completion_seq` (`herdr agent get`) and its `ctx` tokens (`ddsonic-herdr ctx <role>`).
- Short operations: one `herdr_agent_wait` of at most 120 s (Herdr cuts longer timeouts at 120 s and the call fails).
- Long builds, validation and managed-agent work: the watcher is ONE `ddsonic-herdr await <role> --since SEQ`, started with `run_in_background` (default and maximum 1500 s; the dd-devops README is the reference). It reads structured Herdr state only and never signals an agent; keep at most one per agent, and no status polls or separate `ctx` calls beside it (its `ctx` field is the wake-up check for the awaited role; on a Reviewer wake-up, also run one `ctx Writer`). The harness notifies when it exits; its one JSON line is the result.
- A `done` means the agent's turn ended, not always that the task did (an agent may end a turn while its own background command, such as `make validate`, still runs): if it is still waiting, arm one more `await` from the new `completion_seq`.
- On wake-up, read the digest from the end of the `reply` file (`sed -n '/^DIGEST/,$p'`) when `reply_final` is `true`; when it is `false` or absent (a helper before dd-devops#16), read the agent's pane once instead (`herdr_agent_read`) and take the digest only if the pane shows it, otherwise report to the owner. Check `ctx` against the thresholds. `done`: FAIL, relay the actionable in-scope findings to the Writer and arm the next wait; PASS, verify the commit, tests and evidence, then report to the owner. `blocked` (exit 3): stop and report the stall to the owner; never answer another agent's permission prompt. `timeout` (exit 4): re-arm only when the status is `working` and `ctx.tokens` is above the count recorded at the relay or the last wake-up, then record the new count; every other case (tokens unchanged, lower or null, `ctx` `pending` or `unknown`, another status, a prompt) goes to the owner. Any other exit (a Herdr failure, no JSON line) goes to the owner with its error.
- Read the full report only on FAIL, BLOCKED, a dispute, an open MEDIUM or higher, an `Owner` line other than `None`, or `DIGEST over-limit`. Targeted expansion: when a material decision needs more evidence than the digest gives, read just the sections that decide it (`grep -n`, `sed -n` ranges) and say so in the owner report. A missing digest line is a defect to send back.
- Review rounds: at most four per bounded task, stopping on PASS; a fifth needs the owner. Before that limit, escalate for repeated defects, scope growth, real risk or an authorization need.

## Starting a review round

- **Reviewer.** Check that the Reviewer pane holds no agent (end a finished Reviewer with `/exit`), run `ddsonic-herdr start Reviewer` (the dd-devops README is the reference; the `ddsonic` profile sets the project, workspace and `default` Herdr session), and relay the Writer's prompt file with herdr (`herdr_relay`, or `herdr_agent_send` naming the file). The Reviewer is a managed agent: wait for it as above.
- **Supplemental Codex round**, only with the owner's authorization for it: `ddsonic-herdr review --repo DIR --prompt-file FILE [--timeout SECS]`, started with `run_in_background`; the helper's `--timeout` bounds the wait. The helper reports a round as success only with a new session ID and the profile's header.
- **Verify round.** A fresh round whose prompt carries the original findings and the Writer's response to every one (when to repeat: `CLAUDE.md`).

## Reports

Every substantive report (`CLAUDE.md`, Report digest) reaches the owner verbatim through `ddsonic-herdr deliver FILE...`, run with the report's path without reading it; the stream is shown in the ddsonic workspace's Reports tab, and Terminal stays the owner's shell. Your commentary may accompany a report, never replace it.
- Each delivery notification to the owner names every report by the `id` and `title` from `deliver`'s result line.
- `deliver` exits 1 (not persisted) or 2 (persisted, not presented): tell the owner `delivery failed: <reason>; report at <path>`. Every failure is stated.
- A revised report is delivered again as a new entry.
- A Codex round's report is the log whose path `review` prints; deliver that file as it is.
- At the start of a new Writer assignment, run `ddsonic-herdr new-view <issue>-<assignment>`; resets and review corrections keep the current view.

## Reporting to the owner

Stop and report: the outcome, what was fixed, every dispute with both positions, deferred items, unresolved MEDIUMs, and the checks run, carrying over every Owner, Deferred, Disputes, Caveats, Not-executed and open-finding line of the digests. A review that did not finish is reported as such. Before reporting a round or a task closed, check that its findings, blockers, decisions and outcome are on the issue (Recording, in `CLAUDE.md`) and post what is missing.

While #206 is open, check each Writer assignment's context readings (its `CTX` lines, or the manual `ctx` fallback), resets and skills at closeout against #206's qualification criteria, and record the result there. While #217 is in qualification, record each assignment's measurements on #217 at closeout, drafted by the Writer.

A dev candidate's SHA goes to the owner for `ddsonic-fetch` only after the Writer's staging has succeeded.

## GitHub

- **Your share.** The Writer and Reviewer draft records; you post them. Code reading and diffs go to them. Wait for CI with one background `gh run watch <id> --exit-status`. Read and set Project fields with `ddsonic-herdr project <issue> [--status S] [--area A] [--commitment C]`. Read GitHub with narrow `--jq` queries, once each.
- **Where to look:** open issues not yet in the Project (intake), and the Project's Todo and In progress items.
- **Intake: when.** When the owner asks, and at the start of a Dispatcher planning session. Nothing polls: no bot, Action, scheduled job or daemon.
- **Intake: what.** Any issue is accepted as written, in any format, from the owner or a contributor, with or without a template; no agent-specific template or legacy ID is needed. Untriaged means an open issue that is not in the Project, or a Project item whose Area, Commitment or Status is unset. For each, search existing issues, open and closed, for a duplicate; add the issue to the Project if needed; and set the intake defaults below. Leave the reporter's title and text as written and never close or reject an issue for its format or missing sections. A likely duplicate gets one comment linking the original; closing it is the owner's call.
- **Intake defaults.** Commitment: Proposed when the owner's account filed the issue, Exploratory for anyone else, and never Approved (that is the owner's). Status: Idea, set explicitly; later moves follow the Status rule. Area: inferred from the content (Product for features and UX, Engineering for internals, Reliability & Security for bugs and crashes, Distribution & Docs for packaging and docs); when it is genuinely unclear, Area stays unset and the report to the owner says so (no silent default). Catalog ID and Source stay blank. Labels: existing labels only, never a new one; a template's automatic label is checked, not trusted.
- **Intake comment.** At most one comment per issue at intake, posted only after the Privacy check. A newly discovered informal, natural-language issue, the owner's or anyone else's, gets by default one concise, structured triage note: the request as interpreted, relevant context, related or possibly duplicate issues by number, known uncertainties, and a suggested next action. Omit it when the issue already gives enough context or the note would only repeat what is there; the aim is useful context, not mandatory commentary. It marks which points are observed facts, which are assumptions and which are proposals; it states no owner commitment, milestone, approval or priority; and it neither repeats the issue body nor records Status, Area or Commitment, which live only in the Project fields. Proposals for Status, Area, Commitment, milestone or priority go in the report to the owner, never in the comment. For other issues, comment only when it helps: one concise question when triage is blocked, or one short acknowledgment. A likely-duplicate link goes in that same comment. Issues already triaged do not get one retroactively. Never ask a reporter to reformat or fill in a template.

## Context helper

`ddsonic-herdr ctx <Dispatcher|Writer>` prints active tokens and state (Thresholds and Checks, `CLAUDE.md`); `ddsonic-herdr fresh <role> --checkpoint COMMENT_URL [--boundary KEY] [--self]` does one guarded `/clear` (the dd-devops README); `--resume FILE` seeds the reset with the role's resume checkpoint (`CLAUDE.md`, Context and handoffs). A planned reset (any reset at a safe boundary) passes `--boundary <issue>-<step>`; the 600 s cooldown applies only without one. A refusal is not a failure: repeat it once its cause is gone. After a failure or timeout, inspect the pane and the state first; a failed reset is never retried (stop for the owner) and a used boundary is not reused.
