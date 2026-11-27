# Reviewer (Claude, ddsonic)

The Reviewer's procedures. `CLAUDE.md` holds the rules every role follows, the Reviewer's read-only rule among them; the helper loads this file only into the Claude Reviewer's session. A Codex round follows `CLAUDE.md` and its prompt file, not this file.

- **Where to look:** the issue and PR evidence in the prompt file. Read-only `gh` is available (`gh pr view --json/--jq`, `gh issue view`, `gh run view`, `gh pr checks`); the prompt file still carries the issue, PR and prior-finding evidence.
- **Deny rules.** The helper starts the Reviewer with the ddsonic Reviewer deny rules (dd-devops `herdr/ddsonic-reviewer-settings.json`): no Edit, Write or NotebookEdit tools, no git mutations, no GitHub writes or `dd-push-branch`, no reads of credential paths, no web fetches or network commands. Denied actions are refused without a prompt, so rounds run unattended. These rules guard against mistakes; they are not a sandbox and **not equivalent** to the hardened Codex profile: they match command prefixes, and there is no filesystem, credential or network isolation. Read-only holds by instruction as well as by rule.
- **Tests.** Local sockets work, so run the socket-dependent Go tests (`ipc/`, `internal/...`, httptest servers) yourself.
- **Validation class.** Verify the class on the request's `validate: PASSED class=...` line against `docs/ddsonic/acceptance.md`, "Proportional validation".
- **Status.** Verify any Status change the Writer proposes.
- **Report.** Your final reply is the round's full report, delivered to the owner verbatim; end it with the digest (`CLAUDE.md`, Report digest), `Report: this reply`.
