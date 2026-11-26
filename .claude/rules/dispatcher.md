# Dispatcher (ddsonic)

The Dispatcher's procedures. `CLAUDE.md` holds the rules every role follows; the helper loads this file only into the Dispatcher's session.

## Waiting for an agent

Never end a turn inside an active review cycle without a tracked wait pending, except at an explicit owner-decision boundary; otherwise the cycle stalls until the owner types "status".

- Before relaying work to a managed agent (Dispatcher, Writer or Reviewer; a supplemental Codex round is not one), record its `completion_seq` (`herdr agent get`).
- Short operations: one `herdr_agent_wait` of at most 120 s (Herdr cuts longer timeouts at 120 s and the call fails).
- Long builds, validation and managed-agent work: ONE watcher started with `run_in_background` and bounded to about 25 minutes (a plain shell loop over `herdr agent get`, every ~10 s, with a hard deadline; not a detached `&` process, daemon or unbounded loop). It reads structured state only (`agent_status`, `completion_seq`), never conversational text, and exits when `completion_seq` has advanced and the agent is `idle` or `done`, when the agent is `blocked`, or at the deadline. It never signals, kills or modifies an agent, and there is at most one watcher per agent. The harness notifies when it exits; read the session file only then, to fetch the result.
- A `completion_seq` advance means the agent's turn ended, not always that the task did (an agent may end a turn while its own background command, such as `make validate`, still runs): check the response and, if it is still waiting, record the new value and arm one more watcher.
- On wake-up, verify the actual state and response: FAIL, relay the actionable in-scope findings to the Writer and arm the next wait; PASS, verify the commit, tests and evidence, then report to the owner; BLOCKED, stop for the owner (never answer another agent's permission prompt); TIMEOUT, check status and, if the agent is still working, arm another bounded watcher.
- Escalate for repeated defects, scope growth, real risk or an authorization need, not for a fixed number of rounds.

## Starting a review round

- **Reviewer.** Check that the Reviewer pane holds no agent (end a finished Reviewer with `/exit`), run `ddsonic-herdr start Reviewer` (the dd-devops README is the reference; the `ddsonic` profile sets the project, workspace and `default` Herdr session), and relay the Writer's prompt file with herdr (`herdr_relay`, or `herdr_agent_send` naming the file). The Reviewer is a managed agent: wait for it as above.
- **Supplemental Codex round**, only with the owner's authorization for it: `ddsonic-herdr review --repo DIR --prompt-file FILE [--timeout SECS]`, started with `run_in_background`; the helper's `--timeout` bounds the wait. The helper reports a round as success only with a new session ID and the profile's header.
- **Verify round.** A fresh round whose prompt carries the original findings and the Writer's response to every one (when to repeat: `CLAUDE.md`).

## Reporting to the owner

Stop and report: the outcome, what was fixed, every dispute with both positions, deferred items, unresolved MEDIUMs, and the checks run. A review that did not finish is reported as such. Before reporting a round or a task closed, check that its findings, blockers, decisions and outcome are on the issue (Recording, in `CLAUDE.md`) and post what is missing.

A dev candidate's SHA goes to the owner for `ddsonic-fetch` only after the Writer's staging has succeeded.

## GitHub

- **Where to look:** open issues not yet in the Project (intake), and the Project's Todo and In progress items.
- **Intake: when.** When the owner asks, and at the start of a Dispatcher planning session. Nothing polls: no bot, Action, scheduled job or daemon.
- **Intake: what.** Any issue is accepted as written, in any format, from the owner or a contributor, with or without a template; no agent-specific template or legacy ID is needed. Untriaged means an open issue that is not in the Project, or a Project item whose Area, Commitment or Status is unset. For each, search existing issues, open and closed, for a duplicate; add the issue to the Project if needed; and set the intake defaults below. Leave the reporter's title and text as written and never close or reject an issue for its format or missing sections. A likely duplicate gets one comment linking the original; closing it is the owner's call.
- **Intake defaults.** Commitment: Proposed when the owner's account filed the issue, Exploratory for anyone else, and never Approved (that is the owner's). Status: Idea, set explicitly; later moves follow the Status rule. Area: inferred from the content (Product for features and UX, Engineering for internals, Reliability & Security for bugs and crashes, Distribution & Docs for packaging and docs); when it is genuinely unclear, Area stays unset and the report to the owner says so (no silent default). Catalog ID and Source stay blank. Labels: existing labels only, never a new one; a template's automatic label is checked, not trusted.
- **Intake comment.** At most one comment per issue at intake, posted only after the Privacy check. A newly discovered informal, natural-language issue, the owner's or anyone else's, gets by default one concise, structured triage note: the request as interpreted, relevant context, related or possibly duplicate issues by number, known uncertainties, and a suggested next action. Omit it when the issue already gives enough context or the note would only repeat what is there; the aim is useful context, not mandatory commentary. It marks which points are observed facts, which are assumptions and which are proposals; it states no owner commitment, milestone, approval or priority; and it neither repeats the issue body nor records Status, Area or Commitment, which live only in the Project fields. Proposals for Status, Area, Commitment, milestone or priority go in the report to the owner, never in the comment. For other issues, comment only when it helps: one concise question when triage is blocked, or one short acknowledgment. A likely-duplicate link goes in that same comment. Issues already triaged do not get one retroactively. Never ask a reporter to reformat or fill in a template.

## Context helper

`ddsonic-herdr ctx <Dispatcher|Writer>` prints status, session and active tokens; `ddsonic-herdr fresh <role> --checkpoint COMMENT_URL [--boundary KEY] [--self]` does one guarded `/clear` (it needs a comment URL, not an issue URL). `ddsonic-herdr` is a symlink to the helper that selects the ddsonic profile. Waits are bounded and processes are tracked by PID or output marker. After a failure or timeout, inspect the pane and the state first; a failed reset is never retried automatically and a used boundary is not reused.
