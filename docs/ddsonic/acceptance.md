# ddsonic Release Acceptance

Tests say the code does what its author meant. Acceptance asks a different question: does ddsonic, as built for release, do what it promises a user? The rc.3 report that albums saved in YouTube Music never appeared was found by using ddsonic, not by a test. This is the system for finding such things before a candidate is published, and for showing afterwards what was checked.

This page says how it works. The cases are in [acceptance/cases.yaml](acceptance/cases.yaml), what each list promises in [acceptance/semantics.md](acceptance/semantics.md), and the manual procedures in [acceptance/human.md](acceptance/human.md) and [acceptance/live.md](acceptance/live.md).

## Three layers, in this order

1. **Automated code validation.** `make ci`: format, vet, staticcheck, govulncheck, the tests with the race detector, the contract gate. In CI on every pull request commit, every commit on `main` and every release tag (a branch's pushes are covered by its pull request), always in full, as two parallel jobs: `go` (`make ci-core`, everything but the gate) and `sdk-contract`; locally, `make tools` once, then `make ci`, or before a review `make validate`, which runs the local profile of the change's class ([Proportional validation](#proportional-validation)) and is full for anything sensitive or unclear.
2. **Agent and workflow acceptance.** The scripted cases below, then the manual and live cases an agent can review, against the candidate.
3. **Human exploratory acceptance.** A person, on the same binary, **after** the agent pass is green.

A later layer does not start on a red earlier one.

## What exists today, and what does not

This is the first slice. Read the three columns as three different things.

| | State |
|---|---|
| **Executed by the tooling** | The scripted cases: 11 on the binary under test, 13 in-process. `make acceptance` runs them on a local build |
| **Tooling built, not yet used on a release** | Release mode: identifying a draft release's archive and running the cases on the binary extracted from it. It is covered by tests on synthetic archives. **No release run has been made**; the first is rc.4's |
| **Cataloged, executed by hand** | 2 manual deterministic cases, 10 live-provider cases, 4 human cases. Runbooks exist. **None has been executed** as part of building this |

A development run (`make acceptance`) proves the cases and the harness work. It is never release evidence, and its summary says so in its first line.

## Lanes

A case's `lane` says where it executes.

| Lane | What runs | What it can and cannot show |
|---|---|---|
| `binary` | A ddsonic executable in an isolated home, driven through a terminal (tmux) | The built program, end to end. YouTube Music is a fake `yt-dlp`, so not the real service |
| `go` | Go tests in the checkout, in-process | Real sync, catalog and library code over a fake Spotify client that the test can change, fail and block. Not the release binary, and not Spotify's real client or session |
| `manual` | A person or an agent following a runbook | Whatever needs judgement, ears or a real account |

**The `go` lane tests the checkout's source, not the release binary.** In a release run it is allowed to count only when the checkout's production code is the release's source commit; otherwise those cases are `blocked`.

### The binary harness

Each case gets its own environment (`acceptance/harness`):

- a fresh base directory with a **short path** under `/tmp`, holding its own home, config, data, cache and music folders. Runtime files are short-lived and the IPC socket's path has a limit ([files.md](files.md)); evidence is kept elsewhere, below;
- an environment with nothing inherited: no real home, no `DDSONIC_CONFIG_DIR` or `CLIAMP_CONFIG_DIR`, a `PATH` that holds only the harness's own `bin`;
- **no session bus**, so the desktop's media keys and MPRIS name are untouched;
- **ALSA's null device**, so everything plays and nothing is heard;
- **no network, enforced by the kernel** (next section). The status line of a deterministic run therefore shows the radio directory failing to load; that is expected there;
- its own tmux server. Keys go in with `send-keys`, the screen comes back with `capture-pane`, and the player's own `remote state` answers what the screen does not show.

Waits poll for a condition with a time limit and check that the player is still alive; nothing sleeps and hopes. A screen is identified by its breadcrumb before anything on it is read. Where membership matters it is checked twice: on the screen, and in a copy of the catalog file. "This row is absent" is asserted only after a completion point: the sync's recorded finish, and the fake's request log showing the reads were made.

### Network isolation

Every scripted case is offline and fake-backed, and **runs only where the kernel isolates it from the network.** Linux is the platform the rc.4 automated run is made on.

- The runner starts each lane's test process in a new, empty network namespace (`unshare --user --map-current-user --net`): a loopback interface that is down, and nothing else. The player, the fake `yt-dlp` and everything else the case starts are its children and inherit it.
- **It is verified, not assumed.** The process that runs the cases writes down the namespace it is in and the interfaces it can see before it does anything else, and the runner checks that record for every run: not the host's namespace, loopback only. Each binary case checks again from the inside (its namespace, its interfaces, and that reaching an outside address fails with "network is unreachable"), and once more that the running player's process is in that same namespace.
- **Where it cannot be established, the cases are `blocked`.** No `unshare`, user namespaces switched off, another operating system: the scripted cases are not run, and are never reported passed or not applicable.
- Proxy variables that point nowhere are also set, as a second line. They are not the isolation, and neither are DNS settings or environment variables.
- **Live Spotify and YouTube Music cases run outside it,** on purpose: they need the real services.
- **macOS** has no equivalent here. Runs there are supplemental: they cannot claim isolation, their scripted cases are blocked, and they do not stand in for the Linux run.

### The fake `yt-dlp`

`acceptance/fakeytdlp` is the only `yt-dlp` on the player's `PATH`. It **fails closed**:

- it answers only invocations whose arguments are, exactly and in order, ones the case scripted;
- anything else exits with an error and is recorded as rejected;
- **every invocation is recorded as one of three things:** answered, rejected, or a failure of the fake itself (its script missing or unreadable). It is recorded before anything else happens, so a broken fake leaves a trace too;
- **the harness accounts for all of it** once the player has stopped, for every case that installs the fake, whatever the case checked and even if the screen looked right. A rejection fails the case. So does a failure of the fake, a request log that cannot be read, and a script that is gone or malformed at the end: a broken fake is never taken for the provider outage a case scripted. A failure the case did script was answered, and is none of these;
- **the harness hears of every invocation directly.** Before it does anything, and again before it answers, the fake reports to a supervisor the harness runs, over an abstract Unix socket: not a file, in no directory, so nothing done to the file system can stop a report or remove one. If the fake cannot report, it answers nothing. An invocation that ended in a failure of the fake's own, one that began and never reported its end, and one the supervisor heard of that the log does not hold each fail the case, whatever the files look like by then;
- the files are kept beside that, as evidence: a marker left when an invocation starts and removed once it is in the log, and a note of each failure of the fake's own in a file nothing rewrites;
- the log goes into the evidence;
- it never runs the real `yt-dlp` and never uses the network.

What it does not show: that the real `yt-dlp`, YouTube or a signed-in account behave as the fixtures say. [Live acceptance](acceptance/live.md) covers that.

## The case catalog

[acceptance/cases.yaml](acceptance/cases.yaml) is the one list of what is checked. A case names what it checks and where its procedure lives; it holds no steps.

| Field | Values |
|---|---|
| `id` | `AREA-000` or `AREA-PART-000`, unique |
| `area`, `title` | Free text |
| `semantics` | A row of the catalog's `semantics:` section: the promise this case tests |
| `tier` | `deterministic` or `live` (real accounts; always manual) |
| `driver` | `scripted` or `manual` |
| `lane` | `binary`, `go` or `manual` |
| `runner` | A scripted case's Go test: `<package dir>:<TestName>` |
| `procedure` | A manual case's steps: `<runbook>#<heading anchor>` |
| `reviewer` | `assertions`, `agent` or `human` |
| `applicability` | When the case applies |
| `gate` | `required` (gates a release) or `advisory` |
| `implemented` | `false` for a scripted case whose runner does not exist yet; it cannot pass |
| `since`, `regression_of` | The candidate that introduced it; the report it guards against |
| `inapplicable` | The only circumstances in which the case may be recorded `not applicable`: the `releases` and `platforms` it names, a `reason`, and `covered_by`, the other required cases that supply the coverage instead |

`make acceptance-validate` checks, and fails on any of: a missing or unknown field; a value outside the table; a duplicate ID; a `semantics` that is not a row; a runner that is not an existing test, or that two cases name; a procedure whose heading does not exist; a test in `acceptance/binarycases` that no case names; an `inapplicable` entry with no release or platform, no reason, or no covering case that is required and always applies; anything after the file's first YAML document (a second document would otherwise go unread).

**An invalid catalog fails closed.** Nothing runs on one, and a problem with a release-gating case blocks the gate whatever the results say.

## Running it

```sh
make acceptance-validate   # check the catalog
make acceptance            # run the scripted cases on a local build (never release evidence)
```

A release run is given the candidate's files. It downloads nothing and does not create, edit or publish a release:

```sh
go run ./acceptance/cmd/acceptance run \
    --archive ddsonic-1.0.0-rc.4-linux-amd64.tar.gz \
    --sums SHA256SUMS \
    --version v1.0.0-rc.4 \
    --source-sha <the 40-character commit the tag points to> \
    --manual manual-results.yaml \
    --triage triage.yaml
```

It prints the summary for the release record and writes the evidence bundle. It exits non-zero while the gate is not met. `--manual` takes the hand-recorded results, which must name this exact candidate ([acceptance/human.md](acceptance/human.md#recording-what-you-find)).

### The go-librespot contract gate

```sh
make sdk-contract
```

This is a separate gate, and it is mandatory: for the full validation of a change to the Spotify session code, for every release candidate, and for any change that touches the boundary with go-librespot or the version of it that `go.mod` selects. It is not part of `make check`, `make test` or `go test ./...` (it is behind the build tag `sdkcontract`); `make ci` runs it.

It runs eighty-one tests inside go-librespot's own packages, in the tracked replacement of the pinned version (v0.9.1 with patches 0001, 0002 and 0003,[upstream.md](upstream.md#the-local-go-librespot-replacement)), that pin what ddsonic's session code relies on and the exported API does not say: that the access point's `Done` closes before `Close` can block, that a stalled audio key request is released by it, and before it is committed by its own context, that the first audio chunk request ends with its HTTP client's context, that an identity getter waits for a lock a reconnect holds, and that an MP3 decoder is released by `Close` and nothing else. Seven of them (added 2026-10-13, for the aged first play) run the real access point and key provider against a synthetic access point on loopback TCP, whose connection the test closes from the peer's side: a first key request on a connection nobody closed is answered, serviced or not (two controls); an access point nobody services answers no ping and does not notice the close, and the first key request written to the closed connection, which v0.9.1 lost until its 15-second deadline, now ends at once with `ConnectionReplacedError{1, 2}` when the reconnect publishes, and is not sent again; one serviced the way ddsonic services it answers pings, re-authenticates by itself when the peer closes, and answers the first key request made afterwards; a key request in flight across a reconnect is not sent again by the SDK and ends by itself with `ConnectionReplacedError{1, 2}` as the access point's generation moves from 1 to 2, and a request made again is answered; an access point whose reconnect fails for good closes itself, after which a key request fails at once; and closing an access point ends its key writer and receiver, receive loop and ticker, with no answer needed from the server, while a late answer for a replaced request is dropped by the old provider and reaches nobody else (two rounds leave nothing of the old access point running). Forty-three (patch 0002, 2026-10-14) pin the patched access point and key provider, one or more rows each of the design's governing test table, with hooks in the patched code as barriers (none is set in production): connection generations and their announced publication (T-I1, T-A1-T-A3), the hook's domain and one owner per access point (T-H1-T-H4, T-S3-T-S6, S41, S42), the entry refusals (T-E1, T-E2), the receiver's `Done` and closed-subscription fences (T-R2-T-R4, including twenty bounded real-AP trials and a deterministic run of each branch through a subscription the test owns), the outcome that stands (T-R5a-T-R7, S19′), ordering and progress (S20″, S21′, S27a, S27b), replacement against expiry (S25, S26, S26b, S32-S38, S44, T-D1, and the unchanged 15-second window), no resurrection (S35, T-R1), a failed write on an unchanged connection and `Done` before that result is decided (S3, S4, L4), a queued request cancelled before it is committed (S6), callers cancelling while the service's first subscription waits behind a reconnect (S13), and the last sequence number issued once (S17). Four more are base-compatible mechanism probes for S2, S5, S7 and S13: they use only what v0.9.1 already has (what the peer receives, when callers return, goroutine stacks, the access point's subscriptions, and, for S7, whether an answer found no live request, read through a per-stage adapter because v0.9.1 keeps its requests in a map local to its pump goroutine), pass against the active tree and fail against the base. Thirteen (patch 0003, 2026-10-15) pin the reader's single-flight prefetch admission: C1-C10 and three controls of C9 (a bounded join, a late result, the deadline boundary). The runner starts from the toolchain that built it, checked against the repository's pin (Go 1.27.4); uses private, empty caches and an offline module source verified against `go.sum`; authenticates the upstream base against the provenance's go.sum hashes, applies the recorded patches to it and requires the result to be the tracked tree, byte for byte and by its recorded dirhash, and requires the tracked tree with its patches reversed to hash to the upstream base (so the source archive alone can check its SDK bytes); stages a byte-identical copy of the tracked tree outside the module cache; builds a scratch module that is the repository's `go.mod` and `go.sum` plus one replacement, and checks its graph against the repository's; and requires all eighty-one tests to be shown to have run and passed under the race detector. It then runs the symbols gate (`tools/sdkgate`, [upstream.md](upstream.md#the-local-go-librespot-replacement): T-S1 with its source controls, and T-S2), the behavioural mutations B1 and B2 of the staged tree, each of which must turn its contract test red with the failure it is for, and patch 0003's negative controls M1-M3 (M2 is the authentic unpatched reader), each of which must turn exactly its declared assertions red; a timeout, a build failure, a panic or a data race is no red. Then it binds the guard bypass of its composed stage and runs that stage: sixty-one tests that drive ddsonic's real Spotify session code with the patched SDK against loopback fixtures (episodes and Vorbis are not covered there), and six child controls, each of which must end exactly as declared. Last, the stock stage stages rc.4's Spotify code, from an authenticated archive in the repository, against authenticated upstream v0.9.1, where two probes must find rc.4's two known failures (no same-session reopen after a replaced connection; a cancelled key request's late answer taken). Three of them (Slice B, 2026-10-14) drive the real CDN quarantine map of one `Player` from two goroutines released together (concurrent failures; concurrent successful deletes on a populated map) and probe, with nothing else using the player, that its lock is free inside every CDN round trip; `DDSONIC_SDKCONTRACT_STAGE=base` stages the unpatched base instead, where the two race tests fail under the race detector, and each of the four mechanism probes fails on its marked assertion (S2: an in-flight request is still pending after its connection was replaced; S5: a request whose context had already ended is written, registers the key service, and waits for a busy one; S7: a request cancelled during a held write keeps its record; S13: cancelled callers do not return while the service's first subscription waits behind a reconnect). The runner reports, for each probe, whether it was red with its marker, and whether any package failed to build. It is diagnostic red only, never a pass, and the patch-0002 tests and the lock probe are not compiled there; patch 0003's contract is, and what it reports there is diagnostic too. It fails, and never skips, when a prerequisite is missing: the pinned Go, cgo with the codec libraries (mpg123, FLAC, Vorbis), the race detector, or a module in the offline module source. That source is a module download cache holding the archives (`.zip`, `.mod`, `.info`) of every module the repository selects, of upstream go-librespot v0.9.1, and of `golang.org/x/tools` v0.50.0, `golang.org/x/mod` v0.41.0 and `golang.org/x/sync` v0.23.0, which the symbols gate's module builds from (fetched once with `go mod download` in `tools/sdkgate`). A build or a test does not put the upstream SDK archive there, since `go.mod` replaces it with the tracked tree: it is fetched once with `go mod download github.com/devgianlu/go-librespot@v0.9.1` in a scratch module, or carried in the release cache. No extracted copy of the SDK outside the runner's private cache is needed, by the gate or by its own self-tests.

It took about 32 seconds on the development machine (32 cores) on 2026-10-13 with eighteen tests, and about 50 seconds on 2026-10-14 with sixty, the symbols gate and the two mutations (and about the same with sixty-eight): much of it is compiling the SDK from an empty build cache, and one test waits out the SDK's own 15-second key window. With eighty-one tests, the three negative controls and the composed and stock stages, one run on 2026-10-16 took about 5 minutes 21 seconds on the same machine. It is unverified on macOS: the macOS CI workflow does not run it.

When go-librespot is bumped, see [upstream.md](upstream.md#bumping-go-librespot).

### Owed by the next candidate's live and platform acceptance

Recorded here so that none of it is waived by silence. Each is a check on the real candidate, by hand or on the real service, that no scripted case covers:

- The browser opens by itself for the Spotify sign-in, and the sign-in address's link works in Kitty, in Ghostty and through tmux (owner, 2026-10-12).
- LIVE-SPOT-001 again, in a fresh scratch home.
- Real librespot behaviour that the tests reach only through stand-ins: playback recovery after a stream is refused (the silent playback rebuild), a long track that keeps playing across a sign-in, and the teardown of a replaced session.
- **MP3 memory growth (owner, 2026-10-13; a release decision depends on it).** ddsonic does not close a track's decoder when the track ends, by a policy older than this work, and go-librespot's MP3 decoder is released only by being closed, so each MP3 track played is expected to leave its decoder in memory. During long-playback acceptance, on each required platform where practical, measure: the process's memory and native resources across many MP3 tracks played one after another; whether they grow with every track without coming back; roughly what one track costs; and whether a long session degrades noticeably. The result goes to the owner for an explicit decision: a small cost is documented as a 1.0 limitation with a fix after 1.0; a material or unbounded one is fixed before 1.0. This is tracked separately from the sign-in work and is not waived by it.
- **Vorbis decoder retention (owner, 2026-10-13; a release decision depends on it).** When go-librespot has already returned a Vorbis stream but ddsonic never accepts it, because the open is cancelled or superseded or its session is replaced, the pinned SDK cannot safely complete the intended decoder cleanup, so that decoder's native memory and resources may remain retained. The cause is a defect in go-librespot v0.9.1's `vorbis.Decoder.Close`, which can abort the process, so ddsonic does not call it ([upstream.md](upstream.md)). The owner accepted this as a 1.0 limitation on condition that it is measured; passing functional tests say nothing about its cost, and acceptability is not inferred from them. Measure it together with the MP3 measurement above, on each required platform where practical: the growth of the process's memory and native resources across many repeated affected Vorbis opens (opens cancelled or superseded after the stream was returned, and opens whose session a sign-in replaced); whether the retention grows monotonically; roughly what one such event costs; whether repeated cancellation or session replacement degrades the program meaningfully; and whether the platforms differ materially. The result goes to the owner. Release rule: if the cost is small enough for realistic 1.0 use, it is documented as a known 1.0 limitation and a remediation item stays for after 1.0; if it is material or operationally significant, **release progression stops** and the matter goes back to the owner for a remediation decision before 1.0. Not a security waiver.
- The release decision does not wait for an answer.

## Artifact identity

A release run tests **the binary extracted from the draft release's archive**, and refuses to start otherwise. It computes the archive's and the binary's checksums itself, and requires all of this to agree exactly:

- the archive's SHA-256 is the one `SHA256SUMS` lists for it, and the archive is the one named for that version and this platform;
- the archive holds a **release manifest**, `RELEASE-MANIFEST.json`, beside the binary. The release build writes it (`packaging/release.sh`): the product's name, the version, the exact source commit, the platform (`goos`, `goarch`), the Go version and the binary's SHA-256. **Every field is required.** The manifest must name ddsonic, the expected version, the expected commit, the platform the archive is named for, and the checksum of the binary that is actually in the archive; and its platform and Go version must be the ones the binary's own build information gives. The Go version is the release's: it need not be the Go that runs the harness, and it is recorded;
- **the binary itself** reports the version and the source commit it was built from: `ddsonic --version` prints `ddsonic version v1.0.0-rc.4 (commit <40 hex digits>)` in a release build. Both must be the expected ones.

So **the expected release commit, the manifest's commit and the binary's own commit are one and the same,** each read from a different place. The release build makes that mean something: it resolves the source to a commit ID once, before it copies anything, and archives, compiles, embeds and records that same ID, so a branch that moves during the build cannot put one commit's ID on another commit's code. A local tag of that version is one more witness: if the checkout has one it must not contradict them, and if it has none, nothing is concluded from that.

If the manifest is missing or unreadable, or anything disagrees, the run does not start and **nothing is tested in its place.** There is no fallback to a local build "of the same source".

Recorded for every run: the three commits, the archive's checksum, the binary's checksum, the expected and the reported version, the harness revision (the checkout's commit, marked when it has uncommitted changes), the case catalog's checksum, whether network isolation was established, the OS, kernel and terminal, and the versions of Go, tmux, ffmpeg and the host's `yt-dlp`.

The lifecycle around it is in [releasing.md](releasing.md): the tag and the workflow fix the candidate, acceptance runs on the draft's archive, and the summary is committed to the release record afterwards. A tag is never moved and a candidate never rebuilt; a failed candidate is abandoned and the fix ships as the next number.

## The gate

Three separate judgements. None can stand in for another.

| | Values | Who decides |
|---|---|---|
| **Execution** | `pass`, `fail`, `blocked`, `not run`, `not applicable` | What happened when the case was run |
| **Severity** | `BLOCKER`, `HIGH`, `MEDIUM`, `LOW`, `NIT` | Triage, for a case that did not pass |
| **Disposition** | `fix`, `accepted waiver`, `defer`, `investigate` | Triage |

Rules, for a release-gating case:

- Only `pass` is a pass. A result is never edited or triaged into one.
- **An environment limitation never passes.** A case that could not be run is `blocked`, with the reason beside it; a skipped scripted case is reported as `blocked`. A limitation is a note beside a result that did not pass. A result recorded as `pass` *with* an environment limitation is not a pass anywhere: it becomes `blocked` in the results that are stored, in the totals and in the summary, with what was recorded kept as its detail, and the invalid record blocks publication by itself, whatever the case is then triaged as.
- **Each scripted case runs in a test process of its own,** so the process's exit is a signal about that case and nothing else. A reported failure is a failure. A reported pass is a pass only if its process also ended successfully; if it did not (a `TestMain`, a crash, a cleanup or harness that exits), the case is `blocked`, with what the test reported kept beside it. Another case's failure can neither hide such a failure nor be laid at a passing case's door.
- **`not applicable` is the catalog's to grant, never a result's.** A case may be recorded `not applicable` only where the catalog says so for the candidate's release or platform (`inapplicable`), with a reason, and only with another required case named as supplying the coverage. **That case must have been executed to a verdict** for this candidate: a pass, or a failure, which the gate then judges like any other. One that was not run, was blocked, or does not apply itself supplies no coverage, whatever its triage. Today no case has such an entry: every case applies, and in particular **no Spotify or YouTube Music case can be set aside as not applicable for rc.4.**
- **Hand-recorded results are bound to the candidate, exactly.** The file names the candidate it was recorded on: its version, source commit, archive checksum and binary checksum, each to the character (a version in another case, a checksum in capitals or an abbreviated commit is not the candidate). If any is missing or differs, the whole file is refused. Each result says who executed it and when; a time that cannot be read, or is later than the moment of the run by any margin, is refused. How old a result may be is not judged: that policy is not configured.
- Every case that did not pass needs a severity and a disposition.
- **BLOCKER and HIGH block publication.**
- **An unfixed MEDIUM needs Steve's written waiver.** Only Steve approves a waiver. An agent may propose a severity and write the rationale; it does not approve.
- LOW and NIT do not block.
- **`investigate` cannot remain at publication,** at any severity.
- A case with no result is `not run`.

Triage is its own file, given with `--triage`:

```yaml
triage:
  - case_id: UI-001
    severity: MEDIUM
    disposition: accepted waiver
    rationale: The note is cut off only below 60 columns.
    waiver:
      approved_by: Steve
      date: 2026-10-12
      reference: where Steve's written approval can be read
```

A waiver without `approved_by: Steve`, a date and a reference is not a waiver. *How* the approval is given and recorded (Steve's own line in the release record, or a quoted message) has not been decided; until it is, the reference must lead to Steve's own words.

## Evidence

Two things come out of a run.

**The summary** is short and goes into the release record (`docs/ddsonic/releases/<tag>.md`): what was tested, totals per execution result, each lane's cases, every case that did not pass with its severity and disposition in separate columns, what was not applicable, waivers, the gate's answer with its reasons, and the evidence bundle's name and checksum.

**The evidence bundle** is the raw material, and it stays out of git:

- the structured results, the identity, the gate's reasons, the catalog as it was;
- every scripted case's captures: the screen, standard error, the player's log, the remote state, the fake `yt-dlp`'s request log;
- the full test output of each lane;
- the manual results and the triage file, if given.

Before it is sealed, every file is **redacted**: the home directory, the user and host names, and credentials in the forms they ordinarily take: a password, token, secret or cookie as a JSON, TOML or YAML value, under a bare or a quoted key, whether the value is a quoted string, a TOML string in triple quotes over several lines, a YAML block scalar, a list or an object (a list of cookies, each a name and a value); `Authorization`, `Cookie` and API-key headers; bearer and basic schemes; `user:password@` in a URL; tokens among a URL's query parameters; session cookies by name. Each form is read to its real end, by that format's rules: an escaped quote does not close a string, a TOML multi-line string may close on four or five quotes, YAML doubles an apostrophe inside single quotes, a block scalar's header may carry a comment, and a list or mapping written under a secret key belongs to it. A YAML value is read whole: a plain scalar with all its words and the indented lines that continue it; an anchor or tag before a value of any form; a quoted scalar or a flow list to its real closing quote or bracket, on whatever line and at whatever indentation that is; and a value that starts under a comment line. Inside a list or object, a comment is a comment: a bracket, brace or quote in it is not counted, and strings are skipped by their own rules, a TOML multi-line string to the end of its closing run of three, four or five quotes. Where a comment may start is the format's own rule, and the key's separator says which format it is: after `key =` (TOML) a `#` always starts one, straight after a value too; after `key:` (YAML) it needs a space, a line start or a delimiter before it, and inside a word it is part of the word. Text that is itself a YAML mapping is redacted by the YAML parser's own account of where each value lies, before the rules for text are applied to it. Where a value's end cannot be told for certain (a quote that never closes), everything that could still belong to it is removed. JSON is redacted as JSON: every string in it is redacted in its own right, so a credential inside a JSON string is found, and the file stays valid JSON. And `go test -json` gives every printed line an event of its own, so a value over several lines arrives in pieces: each test's output is put back together, redacted as the one text it was, and dealt back to its events, line for line. Where that cannot be done safely, the first event carries all of the redacted text and the rest are emptied. Then it is packed into one archive, and the archive's SHA-256 is written beside it and into the summary, so the evidence a decision rested on can be shown unaltered.

**One redaction policy covers everything a run persists or prints.** The summary quotes what failed, so every field it shows is redacted whole, as it was recorded, before it is shortened to fit a line (shortening first would cut the end off a long credential and leave nothing to recognise), and the finished text once more, before it is written beside the archive or printed. Redaction is by pattern: it is a safeguard, and no reason to paste a secret into a result.

It is kept in `~/.local/share/ddsonic/acceptance/`, a private folder in ddsonic's data directory: durable, and not `/tmp`. `--evidence-dir` names another place; a temporary directory or a path inside the repository is refused, and so is a symbolic link that leads into either, in the directory or anywhere above it.

## Open decisions

These are the owner's, and the tooling does not make them. Where one is needed and missing, the tooling takes the strictest reading.

| Decision | Until it is made |
|---|---|
| How old live evidence may be and still be reused | No reuse |
| Test accounts, and whether any are created | None are created; see [acceptance/live.md](acceptance/live.md) |
| Which changes to a real account a test may make | None; LIVE-SPOT-005 is not to be run |
| How Steve's waiver approval is recorded | The reference must lead to his own words |

## Known gaps

Areas with no case at all. Listed so that their absence is a decision and not an oversight.

- Setup (`ddsonic setup`): fresh, rerun, interrupted.
- Upgrade from 0.10: an existing config and catalog.
- Degraded states beyond a failed sync: unwritable config, data or cache; a corrupt catalog.
- Spotify playlists (order, repeats, a change that keeps the count), Liked Songs and Artists as scripted transitions. Only saved albums are scripted.
- YouTube Music playlists and OAuth mode in the scripted lane. Only Liked Music in cookie mode is scripted.
- Local Artists and Genres; only Albums is scripted.
- Artwork, lyrics, the queue's own keys, the EQ.
- Terminal sizes other than 150×30, 80×30 and 80×14.
- macOS. The scripted cases need Linux's network namespaces and are blocked elsewhere.
- CI. The binary lane runs locally; it is not in the workflows.

## Decoder items: corrections (2026-11-10)

The two decoder items under [Owed by the next candidate's live and platform acceptance](#owed-by-the-next-candidates-live-and-platform-acceptance) stay as the owner set them on 2026-10-13, and what they ask to be measured is still owed; the Vorbis measurement is extended below (owner, 2026-11-10). Two of their statements of fact are corrected here:

- **MP3.** "ddsonic does not close a track's decoder when the track ends" is out of date for Spotify's MP3 and FLAC decoders. In v1.0.0 and on `main` they are closed on every path: a stream that was never accepted when it is disposed of (`releaseDecoder` in `external/spotify/playback_ddsonic.go`), an accepted one when its streamer closes (`spotifyStreamer.Close` in `external/spotify/streamer.go`). The measurement shows whether that holds on a real candidate.
- **MP3 on Local.** Local MP3 files are decoded in pure Go (`github.com/gopxl/beep/v2/mp3`, through `player/decode.go`), not by go-librespot's MP3 decoder, which uses mpg123. A Local MP3 run is a different path and cannot stand in for the Spotify MP3 measurement; that one needs Spotify tracks delivered as MP3.
- **Vorbis.** The retention is not limited to streams that were never accepted. ddsonic closes no Spotify Vorbis decoder, accepted or not (`releaseDecoder`, and `spotifyStreamer.Close`, which closes only MP3 and FLAC), so under the current cleanup policy every opened Vorbis decoder stays retained until the program exits. v1.0.0 behaves the same. About 141 KiB per open was observed with a test fixture and the host's libvorbis; that is not an established cost for every platform, and it was not measured on a release binary.
- **Vorbis measurement, extended (owner, 2026-11-10). OWED, not run.** The owed Vorbis measurement above now also covers ordinary played and manually skipped Spotify Vorbis tracks. Its original obligation (cancelled, superseded and replaced opens) and the release rule stay as written. The owner runs it on the candidate:
  - Arms, each in a fresh process, with Spotify tracks served as Ogg Vorbis: (a) opens cancelled, superseded or replaced: rapid `n`/`p`, and sign-out and sign-in during playback (rapid skips usually finish their opens, so the sign-ins carry most of this arm; this arm meets the original obligation only if the run establishes that such opens actually occurred, and otherwise that obligation stays OWED); (b) tracks played through; (c) tracks skipped by hand (`ddsonic next` a few seconds into each).
  - Counts: sample each arm at N = 0, 20, 40 and 80 tracks started, taken from the commands issued and the queue position. That is not a count of decoder opens: there is no decoder counter in the tree, and preloads and discarded opens are not counted.
  - Each sample: pause playback for about 5 minutes (at least two GC cycles in the trace), then read `grep -E 'VmRSS|RssAnon|VmHWM' /proc/<pid>/status`. Start ddsonic as `GODEBUG=gctrace=1 ddsonic 2> gctrace.log`; the trace helps interpret RSS growth alongside changes in the Go live heap (read the last GC before each sample).
  - Report, per arm, RssAnon, VmHWM, VmRSS and the Go live heap at each N, and the slope of RssAnon in KiB per track started (not per decoder open): a trend, not a pass or fail.
  - About 141 KiB per open (a test fixture, the host's libvorbis) is investigative context only, not an acceptance threshold for the release binary. Material or unbounded growth stays subject to the existing release gate (the release rule in the Vorbis item above) and a separate owner decision.
  - Spotify MP3 must be tested through Spotify, with tracks Spotify serves as MP3, never with Local files.
  - **BLOCKED until the codec a track opened can be verified.** The candidate exposes no supported way to verify the opened codec: go-librespot picks the file whose bitrate is nearest the preferred one, not by codec, and on a tie the first file listed wins (at the default 320 kbps, an Ogg Vorbis 320 and an MP3 320 file tie) (`selectBestMediaFormat` in `third_party/go-librespot/player/format.go`); ddsonic gives the SDK a `NullLogger`, so the choice is not logged; and the release binary links libvorbis and mpg123 statically and is stripped (`-s -w`), so neither `/proc/<pid>/maps` nor a breakpoint by symbol shows which decoder ran. A track's identity does not settle it. Until a verification method exists, the three arms and the Spotify MP3 measurement are not decoder measurements: a run made anyway is recorded as a process memory trend with the codec unknown.

## Changing the basis of the evidence

A case, its runner, a fixture, a semantics row and the harness are what a result means. Changing any of them changes what was proved:

- it is reviewed like any other change;
- it shows in the summary as a different catalog checksum and harness revision;
- a change that turns a failing case green is a change of expectation. It is reported as one, with its reason, and is not the candidate passing.

## Proportional validation

This is a local aid for the time before a review. It does not change CI: GitHub runs both jobs, `go` and `sdk-contract`, in full on every pull request commit, every commit on `main` and every release tag.

`make validate` gives the working tree's change one of four classes, with `tools/classify-change.sh` and the rules in `tools/validation-classes.txt`, against `main` fetched at that moment from the project's repository, and runs that class's profile:

| Class | What it covers | Profile |
|---|---|---|
| docs | documentation no test reads | preflight, gofmt, `git diff --check` against the base, the root package's tests with the citation check strict (`make validate-docs`) |
| test | tests only | preflight, vet, staticcheck, the whole suite under the race detector, shellcheck (`make validate-test`) |
| code | Go source and what tests read or embed | `make ci-core`, everything but the contract gate |
| full | everything else | `make ci`, the contract gate included |

The class of a change is the highest class of its paths. A path no rule names is full, and so is anything the classifier cannot decide safely: a symlink, a submodule, a mode or type change, a binary, a deleted or renamed test, a rename across classes, a name that is not plain UTF-8, an empty diff, a shallow clone, a malformed rule and any error. The build, CI, tooling and the classifier with its rules, dependencies, the Spotify boundary, sign-in and the other security-sensitive code, and this page are full by rule, so they always need the full `make ci`. The rules come from the fetched base, not from the change.

`PROFILE=test`, `code` or `full` raises the profile; a lower or unknown one is an error, and nothing runs. After the profile, `make validate` classifies again and fails if the class rose meanwhile. Its last line, `validate: PASSED class=... profile=... base=... head=...`, goes into the review request, and the Reviewer checks the class independently.

**What it is not.** `make validate` runs the Makefile and scripts of the working tree, which the change itself can edit, so its class line is evidence for the Reviewer, not a control. Nothing here, locally or in CI, is tamperproof against a change that edits its own Makefile, scripts or workflow: such edits are full by rule, and independent review and the owner's approval of the merge remain necessary.

**Residual risk.** A lower local profile can miss what only the skipped steps would find, for example a regression across components that the contract gate would have caught; CI's full run of the pull request, and of `main` after the merge, is where that shows.

**If `main` is red after a merge:** no further merges until it is green; find the pull request that caused it; the owner decides between a revert and a fix forward; record it as a defect, with the run; report it. No silent re-run: a re-run is part of that record.
