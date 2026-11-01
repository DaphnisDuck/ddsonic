# Spotify token coordinator: design specification

Status: revision 10, with one correction of fact made on 2026-10-13 after
review round 9 and marked where it stands (section 4.3, "CORRECTION"): the
claim that closing a never-accepted Vorbis decoder is safe was wrong. The
owner's decision on that (2026-10-13) is recorded there too.
The design is implemented on branch `fix/spotify-signin`
(see `plan.md`); the implementation is in review and is not accepted.
Revision 10 differs from revision 9, which was the reviewed design, in two
things only, both in 14.5: the contract runner is a separate mandatory gate
(`make sdk-contract`) and not part of `make check` (owner decision,
2026-10-13), and a failing tool's exit is read before its output. Everything
else is revision 9's text, including its "is to be" tense. Where the
implementation deviates from this document, `plan.md` says so.
The next paragraph describes revision 9, whose first sentence then read
"DESIGN ONLY, revision 9. Nothing in this document is implemented."
Revision 9 follows an execution of the 14.5 runner sequence in a scratch
directory (recorded at the end of 14.5). The sequence ran as written; three
details the run exposed are added to the steps (a writable private module
cache so the temporary directory can be removed, the C toolchain in the
child's environment, and what a well-formed changed archive is). The next
paragraph describes revision 8.
Revision 8 answers the designated reviewer's finding on revision 7
(`/tmp/dsdesign7.5k1x_mb5/FINAL-DESIGN-REVIEW.txt`): Go refuses an overlay
that replaces files beneath the active module cache, so the contract-test
runner stages a verified copy of the SDK outside it and overlays that
(14.5). The next paragraph describes revision 7.
Revision 7 answers the designated reviewer's finding on revision 6
(`/tmp/dsdesign6.vm_d0m1c/FINAL-DESIGN-REVIEW.txt`): the contract-test runner
extracts the pinned module into its private cache before it asks for the
module's directory (14.5), and two sentences in 4.3 are made exact. The next
paragraph describes revision 6.
Revision 6 answers the designated reviewer's finding on revision 5
(`/tmp/dsdesign5.j4m5m1wg/FINAL-DESIGN-REVIEW.txt`): a track open whose
caller has already given up is never accepted and never fed to recovery
(4.3, step 5). It records the owner's decisions of 2026-10-12 on O4 and on a
build that succeeds after its deadline (15.4), and the pinned-toolchain check
of the contract-test runner (14.5). The next paragraph describes revision 5.
Revision 5 answers the designated reviewer's findings on revision 4
(`/tmp/dsdesign4.y5wfwi92/FINAL-DESIGN-REVIEW.txt`): every unaccepted track
open has one cancel handle for its HTTP lifetime; a never-accepted stream's
decoder is closed; an error from a retired bundle is not fed to recovery; and
a build completes on its own deadline whether or not the SDK returns (4.2,
4.3). O4 was pending in revision 5; it is decided now (15.4).
The next paragraph describes revision 4.
Revision 4 answers the designated reviewer's five findings on revision 3
(`/tmp/dsdesign3._ab85b1x/FINAL-DESIGN-REVIEW.txt`): no caller ever calls the
SDK's blocking close (4.3); a complete ownership protocol for a stream being
opened (4.3); an explicit build phase so a stored build cannot start during a
browser sign-in, and a starter that waits like a joiner (4.2); and corrected
wording. It also records the owner's decisions O1 to O3 of 2026-10-12 (15.4).
The paragraph below describes revision 3.
Revision 3 answers the designated reviewer's six findings on revision 2
(`/tmp/dsdesign2.15gyfx3y/FINAL-DESIGN-REVIEW.txt`): one commit boundary for
acceptance, file write and close (3, 8); playback teardown that does not wait
on the SDK (4.3); an owner-qualified dirty flag in place of sequence numbers
(8); one stored-credential build shared by startup and Authenticate (4.2);
one canonical request loop with separate bounds for recovery renewals and
token flights (5, 16(d)); and no offered exception to the lock rule. The
ordering matrix states the state space it covers and no longer claims more.
Revision 2 records the owner's decisions of 2026-10-12 on revision 1 (quit-time
durability, owed-sync scope), removes every lock held across network I/O on a
read path (section 4), and adds section 16, reviewer hotspots. It is written
against branch `fix/spotify-signin` at
`9b3bae3e9639a71109bd827246efd8800d6bedd7` (tree
`7c1e4441aa8e23787df7b1407071f06439cf0af3`), after review round 8 rejected
that tree with findings R8-1 to R8-4
(`/tmp/dsr8.lfdlqj99/FINAL-REVIEW.txt`). Sections are numbered as the
dispatcher's request numbered them. File and line references are to that
commit.

## 0. Summary of the design

Today two things refresh a Web API token: the long-lived `oauth2` token source
(automatic, on expiry, inside the library, under the library's own mutex) and
`Session.renewToken` (forced, after a 401 or for a session without a token).
They are tied together afterwards by a sign-in generation, a token revision
that counts only forced replacements, a source identity and a retirement flag.
Rounds 5 to 8 each found an ordering those checks did not cover.

The design replaces both refreshers with one object, the **token
coordinator**. Four decisions carry the argument:

- D1. **One coordinator per sign-in.** A coordinator is created with the
  session a builder produces and dies when that sign-in is replaced or its
  session is closed. It holds no reference to its `Session`, so it never
  "changes hands".
- D2. **No long-lived oauth2 source.** The coordinator caches the access token
  and its expiry itself. Every refresh, automatic or forced, is the same
  routine, and at most one runs per coordinator at a time (single flight).
  Callers that need a newer token while one is in flight wait for that one.
- D3. **Every published token is a new lease revision.** Refusals and 401s
  carry the lease they are about and apply only while that lease is current.
- D4. **One logical request is one object.** The 401 recovery state and the
  429 state are fields of the same request value, driven by one loop.

- D5. **No lock a read path takes is held across network I/O.** Session
  construction is a single flight on a channel, and a session's librespot
  player is torn down outside every lock (section 4).

Two routine corrections ride along: an explicit refresh that arrives while an
older run still owns the provider is owed and runs after it (R8-3), and a
cancelled OAuth flow offers nothing (R8-4).

New file: `external/spotify/token_coordinator_ddsonic.go`. Names below are
proposals; the invariants are the specification.

```go
type tokenCoordinator struct {
    clientID string
    ctx      context.Context    // ends at close; parent of every flight
    cancel   context.CancelFunc
    now      func() time.Time   // clock, replaceable in tests

    mu           sync.Mutex     // L5; never held across I/O of any kind
    closed       bool
    creds        storedCreds    // this sign-in's credential, in memory
    dirty        bool           // creds differ from what this coordinator last wrote; true at construction
    rev          uint64         // revision of the current lease; 0 = none yet
    access       string
    expiry       time.Time      // zero = does not expire (as oauth2)
    refused      bool           // the lease refusedRev was refused
    refusedRev   uint64
    flight       *refreshFlight // the one refresh in the air, or nil
}

type lease struct {             // which token a request was sent with
    c   *tokenCoordinator
    rev uint64
}

type refreshFlight struct {
    base  uint64                // c.rev when the flight began
    done  chan struct{}         // closed after the durable section (settle and write)
    lease lease; access string; err error // the result, read after done
}

// credStore is the credential file (process-wide; one file).
var credStore struct {
    mu    sync.Mutex            // L4; the durable-commit guard (section 8)
    owner *tokenCoordinator     // whose credential the file holds, or nil
}
```

Operations (all of them; there are no others that touch token state):

- `newTokenCoordinator(clientID, creds, seed *oauth2.Token)`: `seed` nil
  gives a coordinator with no lease (`rev == 0`).
- `token(ctx) (lease, access, error)`: the current lease if usable; otherwise
  `renew` from the current revision.
- `renew(ctx, basis lease) (lease, access, error)`: a lease newer than
  `basis`, by joining or starting the single flight.
- `settle(flight, tok, err)`: the only place a lease is published or a
  refresh refusal recorded.
- `refuse(l lease) bool`: records that the Web API refused lease `l` after a
  renewal; applies only if `l` is current.
- `setIdentity(username, data, deviceID)`: the librespot half of the
  credential, at a silent playback rebuild.
- `needsSignIn() bool`, `snapshot() storedCreds`, `close()`.
- `credStore.claim(c)`, and the durable section every credential-changing
  operation and `close()` run in (section 8). There is no separate flush.

The token usable test is `access != "" && (expiry.IsZero() ||
now().Before(expiry.Add(-10*time.Second)))`, the same margin `oauth2` uses, so
real expiry behaves as it does today.

## 1. Ownership

The coordinator is the sole authority for: acquiring a token for a live
sign-in, caching the usable token, automatic refresh, forced renewal,
publishing a new token, versioning token state, persisting a rotated
credential, classifying a token refusal, and marking a token auth-dead.

What happens to each existing production site:

| Site today | Where | Fate |
|---|---|---|
| `persistingTokenSource` | `session.go:337-363` | Removed. Rotation is detected in `settle` by comparing the answer's refresh token with `creds.RefreshToken`. |
| automatic `oauth2` source (`conf.TokenSource` kept in `Session.tokenSource`) | `session.go:82`, `session_auth_ddsonic.go:162-171` | Removed. `Session.tokenSource` is deleted; the coordinator's cache and `renew` replace it. No long-lived `oauth2.TokenSource` exists in production. |
| forced `Session.renewToken` | `session_auth_ddsonic.go:471-528` | Removed. `renew(ctx, basis)` is the same routine as automatic refresh. |
| `Session.restoreWebAPIToken`, `ensureWebAPI` | `session_errors.go:75-103` | `restoreWebAPIToken` removed: a coordinator without a usable lease refreshes in `token()`. `ensureWebAPI` becomes `ensureSession` plus nothing. |
| `restoreMu` | `session_errors.go:69` | Removed. Single flight replaces it. |
| `SpotifyProvider.sessionMu` | `provider.go:70,108` | Removed. The stored build is a single flight on a channel (`storedCall`, section 4.2); no mutex is held while it runs. |
| `webAPITokenSource` | `session.go:365-376` | Removed (section 13). It persists with an unguarded closure. |
| `Session.webApiWithBody` | `session.go:664-667` | Removed (section 13). No production caller. |
| `Session.webAPIStamped`, `Session.accessToken` | `session.go:673-697`, `session_auth_ddsonic.go:349-383` | Replaced by the request driver's send step (section 5) and `tokenCoordinator.token`. Lyrics (`lyrics.go:57`) calls `token(ctx)` and nothing else, as now. |
| `Session.refuseToken` | `session_auth_ddsonic.go:314-327` | Replaced by `tokenCoordinator.refuse(lease)`. |
| `Session.refuseSignIn` (playback) | `session_auth_ddsonic.go:301-309`, `provider.go:764,779` | Kept, unchanged in meaning: it is a refusal of a session build (generation), raised by playback, and is not token state. It moves no token and writes no credential. It is read together with the coordinator through one accessor (section 2). This is the one "auth-dead" mark that is not inside the coordinator; see 16(c). |
| `sourceID`, `adopt`, `rotate` gating | `session_auth_ddsonic.go:176-218` | Removed. A coordinator does not reference its session, so there is nothing to adopt; "is this source still the session's" becomes "is this coordinator open". |
| `credential` (`committed`, `retired`), `credMu` | `session_auth_ddsonic.go:134-267` | `credential` removed; its state is the coordinator's `creds`/`dirty`. `credMu` becomes `credStore.mu` with an explicit owner. `committed` is "is `credStore.owner`"; `retired` is "`closed`, or no longer owner". |
| `tokenStamp{gen, rev}` | `session_auth_ddsonic.go:338` | Replaced by `lease{c, rev}`. |
| `signInState.tokenRev`, `refused*`, `cred`, `source` | `session_auth_ddsonic.go:108-125` | Removed from `Session`, except `gen`, `closed` and the playback refusal (`refusedGen`). |
| `WithSignIn` pin, `signInPin`, `authEpoch` | `session_auth_ddsonic.go:385-459`, `provider.go:80` | Kept exactly (section 6). It is action identity, not token state. |
| `authorized`, `unauthorized`, `webAPIWithBody`, `webAPIOnce` | `session_auth_ddsonic.go:671-717,841-882`, `provider.go:791-862`, `catalog_sync.go:284-306` | Merged into one request driver with a rate policy parameter (section 5). The two public helper names remain as thin callers. |
| silent session rebuild's own refresh (`Session.Reconnect` -> `NewSessionSilent` -> `silentTokenRefresh` and a second credential) | `session.go:713-715,186-196` | Removed from the reconnect path. A silent reconnect rebuilds playback only and keeps the live coordinator (section 7.5). Today it is a second refresher of the same refresh token that runs outside `restoreMu`. |
| setup refresh in the stored builder | `session.go:190` | Kept as **seed acquisition**, and serialized: every stored-credential build of a provider, whether startup or `Authenticate` asked for it, is the one `storedCall` in flight (section 4.2). It runs only while the provider has no session, so at most one refresher of the stored grant exists at any time: the one stored build, or the accepted session's coordinator. |
| interactive exchange | `session.go:523` | Kept as seed acquisition of a **new** grant (a browser authorization code). It never refreshes the stored refresh token. |
| `NewSession` (stored first, then browser, in one builder) | `session.go:137-147`, `session_auth_ddsonic.go:80` | No longer the provider's sign-in builder: `signIn` runs the stored phase through `storedCall` and the browser phase through `newInteractiveSession` (4.2). `newSessionFromStored`'s non-silent branch (`doWebAPIAuth`, `session.go:231`) loses its production caller. |

After the change, `saveCreds` has exactly one production caller
(the write step of the durable section), and `silentTokenRefresh` exactly two (the flight, and the
stored-session builder's seed).

## 2. Identities

| Identity | Representation | Created by | Invalidated by | A stale result carrying it |
|---|---|---|---|---|
| A. Sign-in (token authority lifetime) | the `*tokenCoordinator` pointer | a builder (`newSessionFromStored`, `newInteractiveSession`) | `close()`: interactive publication replacing it, `Session.Close`, or disposal of a build nobody took | MAY be returned to the caller that waited for it, as "superseded". MAY NOT publish a lease, record a refusal, change `creds`, or write the file. |
| B. Session build | `Session.gen` (unchanged) | every publication into a session, interactive or silent | the next publication | Used by playback (`refuseSignIn(gen)` applies only if still current) and by pins. MAY NOT affect another generation. |
| C. Token lease | `lease{c, rev}` | `settle` on success (`rev+1`), or the seed (`rev 1`) | the next successful `settle` on `c`, or `c.close()` | A 401 or refusal about it MAY mark the sign-in refused only while `l.rev == c.rev` and `c` is open. MAY NOT refuse, replace or outlive a newer lease. |
| D. Refresh flight | `*refreshFlight` with `base` | `renew`, when none is in flight | `settle` | Commits only if `c` is open (then `c.rev == base` holds by construction, and is asserted). |
| E. User action | `signInPin{epoch, sess, gen}` (unchanged) | `WithSignIn` at action start | any interactive acceptance (`authEpoch`), or any publication into its session | Its remaining writes are not sent: `errSignInChanged` (`provider.ErrNotApplied`). Never resumed. |
| F. Logical request | `*apiCall` (section 5) | one call of `webAPIWithBody` or `webAPIOnce` | return of that call | Holds its own renewal and rate state; shared with nobody. |

Readers get A and B together and without a lock: `Session` gains
`view atomic.Pointer[sessionView]`, an immutable
`{tokens *tokenCoordinator; gen uint64; closed bool; playbackRefused bool}`,
stored only while `Session.mu` is held (publication, `Close`,
`refuseSignIn`). `Session.generation()`, `needsSignIn()` and the request
driver read it. `needsSignIn` is `playbackRefused || tokens.needsSignIn()`,
and `tokens.needsSignIn()` is `(refused && refusedRev == rev) || rev == 0`,
which is today's `refusedLocked() || tokenSource == nil`.

## 3. Publication invariants

- **I1 (single flight).** For one coordinator at most one flight exists at a
  time: `renew` starts one only when `c.flight == nil`, under `c.mu`, and
  `settle` clears it under `c.mu`.
- **I2 (one publication point).** `c.rev`, `c.access`, `c.expiry` change only
  in `settle` on success. `settle` requires `!c.closed`. Given I1, every
  flight that settles on an open coordinator has `base == c.rev`: no other
  flight ran between its start and its settle. Hence: *an older refresh result
  never overwrites a newer published token*, because two refresh results for
  one coordinator are never concurrent, and a result for a closed coordinator
  is dropped.
- **I3 (refusals are about a lease).** `c.refused` is set only by (a) `settle`
  with `invalid_grant`, for the current `rev` (the flight's `base`), and (b)
  `refuse(l)` when `l.c == c && l.rev == c.rev && !c.closed`. Hence: *an older
  refusal never invalidates a newer accepted token.*
- **I4 (success supersedes).** The coordinator is refused iff
  `refused && refusedRev == rev`. A successful `settle` advances `rev`, so *a
  later successful renewal supersedes the refusal of the older revision*
  without any clearing step. While refused, `token` and `renew` return
  `errSignInRefused` without starting a flight; the only success that can
  follow a refusal is a flight already in the air when it was recorded (matrix
  row 4). Otherwise the refusal ends with the sign-in.
- **I5 (one commit boundary).** Every transition that changes `c.creds`,
  publishes a lease from a flight, changes `credStore.owner`, or sets
  `c.closed` runs inside a **durable section**: `credStore.mu` (L4) is held
  from before the transition is decided until after the file write it calls
  for has returned. Inside it, `c.mu` (L5) is taken for the memory step only
  and released before any file I/O. So `closed` is written only with L4 and
  L5 both held, and read under either.
- **I6 (acceptance and durability are one step relative to close).** Because
  `close()` needs L4, it runs either wholly before a durable section (the
  result then finds `closed` and is dropped: it was never accepted) or wholly
  after it (the result was accepted and its write has returned). There is no
  ordering in which a result is accepted and then skipped by close, and none
  in which a file is renamed after its coordinator closed. A held durable
  section postpones the closed transition; it is never the other way round.
- **I7 (ownership of the file).** `credStore.owner` is set only by
  `claim(c)`, called at exactly three acceptance points: `publish`
  (interactive), `signIn` first install, and the stored build's commit.
  `close()` clears it if it is `c`. A replacement closes the old coordinator
  and claims the new one in **one** durable section.
- **I8 (the file is written only for the open owner).** A write happens only
  inside a durable section, only for `c == credStore.owner`, only while
  `!c.closed`, and only when `c.dirty`. `dirty` is per coordinator: true from
  construction (a seed is never on disk in that form), set by every change of
  `creds`, cleared only after a write of that coordinator's `creds` returned
  without error. Nothing is compared across coordinators.
- **I9 (unaccepted work is inert).** A build that failed, was cancelled or was
  not taken is never claimed; a coordinator that was replaced or closed is not
  the owner and is closed. By I8 neither can change the file. By I2, I3 and I5
  neither can change another coordinator's memory.
- **I10 (atomic for readers).** A reader observes token state only through
  `c.mu` (one critical section returns lease, access and refusal together) and
  sign-in identity only through `Session.view` (one atomic load). A settle
  changes lease, expiry and refresh token in one L5 section. Publication of a
  sign-in is: `authEpoch+1`, then `view.Store(new)`, then the old coordinator
  closed. A reader that sees the new view sees the new epoch.
- **I11 (a live answer is not dropped for a caller's sake).** A flight runs on
  `c.ctx`, not on a caller's context. A caller that stops waiting does not
  cancel it; its answer is settled and written with no waiter.
- **I12 (one refresher per grant, before acceptance too).** The stored
  refresh token is refreshed only by the provider's single stored build while
  the provider has no session, and only by the accepted coordinator
  afterwards (4.2). An interactive build refreshes nothing: it exchanges a new
  authorization code.

## 4. Locking and I/O

**Rule.** Network I/O is never performed while holding a lock that a normal
read path takes. There is no exception for pre-existing or non-token state.
Revision 1 listed two exceptions (`sessionMu` across the startup build, and
`Session.mu` across librespot stream setup and teardown). Both are removed
here, by the same shape each time: establish ownership of the work, snapshot
what it needs, release the locks, do the work, retake a narrow commit lock,
revalidate, commit only if still current, otherwise discard.

### 4.1 Guards

In lock order (a lock is taken only while holding locks earlier in this list):

| # | Guard | Read under it | Published under it | Never under it |
|---|---|---|---|---|
| L2 | `SpotifyProvider.mu` | `session`, `closed`, `storedRefused`, `signingIn`, `stored` | the same; first-install and startup `claim` | builds, browser flow, Web API or token requests, librespot calls |
| L3 | `Session.mu` (plain mutex after this change) | `closed`, the current playback bundle (to take a reference) | `view`, the playback bundle pointer; the durable sections of interactive publication, silent `setIdentity` and `Close` | session builds, stream setup, player or session teardown, Web API or token requests |
| L4 | `credStore.mu` | `owner` | `owner`, every coordinator's `closed` and `dirty`, the file (temp + rename, 0600) | any network I/O |
| L5 | `tokenCoordinator.mu` | lease, expiry, refusal, `creds`, `flight`, `closed` | the same | any I/O at all |
| L6 | `signInPin.mu`, `rateGate.mu` (leaves) | pin binding; rate block | the same | any I/O |

Order: L2 -> L3 -> L4 -> L5; L6 locks are leaves. The coordinator takes
nothing while holding L5 and has no pointer to a session or provider, so no
edge runs upward. Existing order was `Provider.mu -> Session.mu ->
sourceID.mu/credMu` and `restoreMu -> Session.mu -> credMu`. `sessionMu`,
`restoreMu` and `sourceID.mu` cease to exist; `credMu` is L4; L5 is new.

Read paths and what they take: a Web API request takes L2 (one pointer read),
loads `Session.view` without a lock, and takes L5 (one short section). Opening
a stream takes L3 for one short section (4.3). Nothing else.

### 4.2 Session construction: one build phase, one stored build, no mutex across it

`sessionMu` is deleted. A provider without a healthy session is in exactly
one **build phase**, a field guarded by L2:

```go
type buildPhase int // idle | storedBuilding | browserSignIn

type storedCall struct {
    done chan struct{}   // closed by the call's controller after its completion step
    err  error           // written by the controller before done; read after
}
// SpotifyProvider gains: phase buildPhase; stored *storedCall;
// storedCancel context.CancelFunc. signingIn *signInCall exists already.
```

**`p.storedBuild(wait context.Context) error`** is what `ensureSession` is
(with a context that is never cancelled) and what `signIn` calls first.

Entry, under L2, in this order:

1. a session exists -> nil.
2. `closed` -> `errProviderClosed`; `storedRefused` ->
   `errStoredSignInRefused`.
3. `phase == browserSignIn` -> **no build is started.** Return at once
   "sign-in in progress", which is `playlist.ErrNeedsAuth`, and change
   nothing (`storedRefused` is not set). The caller does not wait for the
   browser: a background sync must not hang for the length of a sign-in. The
   sync a successful sign-in requires is the owed run of section 11.
4. `phase == storedBuilding` -> take the existing call.
5. `phase == idle` -> create the call and its own cancellable context
   (network setup bounded by `sessionSetupTimeout`), set
   `phase = storedBuilding`, store `stored` and `storedCancel`, and **start
   the call's controller and worker** (below).

Then release L2. **Every caller, the one that started the build included,**
waits the same way, holding nothing:
`select { <-call.done: return call.err; <-wait.Done(): return wait.Err() }`.
The starter has no special role after step 5: if it stops waiting, the build
goes on.

**A build ends on its own deadline, whether or not the SDK returns.** The
SDK can keep a builder past its context: after constructing the session the
stored builder reads its identity (`session.go:181`), and the SDK's getters
take a lock with no context (`ap/ap.go:623-642`) that a background reconnect
can hold; a failed builder's own cleanup calls the SDK's `Close`
synchronously. So no call, phase or waiter may depend on the builder
returning. Every session build in the provider (stored, browser, silent
playback rebuild) therefore runs as two goroutines:

- The **worker** runs the raw builder on the call's context and publishes its
  result with the same ownership rule as a track open (4.3):
  `CAS(pending -> ready)` then a send that cannot block; if the CAS fails the
  worker owns its late result and retires it (a built session is retired as
  in 4.3; its coordinator was never claimed, so nothing is written). Nobody
  waits for the worker.
- The **controller** selects on the result and on the call's context (its
  deadline, `Provider.Close`'s cancellation). When the context ends first it
  does `CAS(pending -> abandoned)`; if that fails the worker has published,
  and the controller receives the result. Either way the controller then
  runs the **completion step** below and closes `done`. The controller makes
  no SDK call and waits for nothing but those two events, so **a call
  completes no later than its own deadline plus the completion step's local
  work.**

The completion step for a stored build is one L2 section. It, and only it,
clears `stored` and `storedCancel` and decides the next phase:

- **Ownership check first:** the call must still be `p.stored`, the provider
  open, and **the call's context not ended**. A result that arrives with the
  context already ended is rejected even if it reports success: the build is
  treated as expired.
- `p.closed`: `err = errProviderClosed`; `phase = idle`.
- expired or cancelled (with or without a late result): `err` is the
  deadline or cancellation error, through `silentSessionError`;
  `storedRefused` is **not** set; next phase as for "any other failure".
- build succeeded, in time: `p.session = sess`; if `p.signingIn != nil`,
  `authEpoch+1`; `credStore.claim`; reset the session-scoped caches.
  `phase = browserSignIn` if a sign-in is registered **and** the new session
  needs sign-in (it has no Web API token), else `idle`.
- build refused for the stored credential, in time: `storedRefused = true`;
  `phase = browserSignIn` if a sign-in is registered, else `idle`.
- any other failure (timeout, network): `phase = browserSignIn` if a sign-in
  is registered, else `idle`.

After releasing L2 the controller retires a result it received and did not
install (without waiting, 4.3), sets `call.err` and closes `call.done`.

What follows from this:

- **Retry is possible.** After an expired call the phase is released, so the
  next `ensureSession` starts a new call, even while the old worker is still
  inside the SDK.
- **A late worker cannot refresh the stored token.** The setup refresh is
  sent on the call's context and comes after the identity read; a call that
  has completed has a dead context, so a worker released later fails that
  request before it is sent. I12 holds across retries.
- **A late success is never claimed or written.** Its coordinator is never
  the owner. If Spotify had rotated the token for it, that result did not
  reach acceptance: the limitation of 15.4(1), not a new one.
- **Only a waiter stopping is different:** the call's context is not the
  waiter's. A cancelled sign-in or caller leaves the build running, as
  before.
- **What a stuck worker retains** is one goroutine and whatever SDK objects
  it had built, until the SDK lets go. That is the same retention as a stuck
  tail and is counted in the O4 limitation (4.3).

The browser build (S4) and the silent playback rebuild are wrapped the same
way (approved by the reviewer), each keeping its own timeout and
cancellation as the call's context: a sign-in ends at `signInTimeout` or its
cancellation even if the SDK holds its builder, so `signingIn` and
`browserSignIn` cannot be left set by a hung build. A silent rebuild captures
the generation it set out to renew when it starts, and its completion
revalidates that generation as well as its context: a rebuild that returns
late, or after the session moved on, is retired and published nowhere.

So when a sign-in is waiting on the stored build, the phase passes **from
`storedBuilding` to `browserSignIn` in the commit's own L2 section**. No
caller can observe `idle` in between, and entry rule 3 keeps every later
caller out while the browser is open.

**`signIn` (Authenticate):**

- S1. Under L2: closed -> error; a healthy session -> nil; a sign-in in
  flight -> join it (wait on its `done`). Otherwise register:
  `p.signingIn = call`, with the sign-in's own cancellable context. Still in
  that section: if a session exists and needs sign-in, or `storedRefused`,
  set `phase = browserSignIn` now and go to S3.
- S2. Stored phase: `p.storedBuild(signInCtx)`. The sign-in is a caller like
  any other: it starts the build or joins the one in flight. It cannot join
  itself, because what it joins is a `storedCall`, never a `signInCall`, and
  entry is decided by `phase`, not by `signingIn`.
- S3. After waking **for any reason**, retake L2 and decide from the state,
  not from which channel fired:
  - a healthy session exists: success. (If the build installed it while this
    sign-in was registered, the epoch moved in that commit.)
  - the sign-in's context has ended (cancelled, timed out, provider closed):
    **deregister** in this same section: `p.signingIn = nil`; if `phase ==
    browserSignIn`, `phase = idle`. Complete the `signInCall` with the
    cancellation. From here the stored build, if still running, is an
    ordinary background build: its commit sees no registered sign-in, moves
    no epoch, and hands no phase to anyone.
  - `phase == browserSignIn` (handed over by the commit, or set at S1): go on
    to the browser.
- S4. Browser, no lock held: no session -> `newInteractiveSession`,
  installed as today; a session that needs sign-in -> the rebuild in place.
  Both are a new grant; neither reads or refreshes the stored refresh token.
- S5. End, one L2 section, by the sign-in goroutine and only it: install or
  not (as today), `p.signingIn = nil`, `phase = idle`, `storedRefused =
  false` on success; then complete `signInCall.err` and `done`.

Who writes what:

| State | Written by |
|---|---|
| `stored`, `storedCancel`, `storedCall.err/done` | the call's controller (created at entry rule 5); never the worker |
| `signingIn`, `signInCall.err/done` | the sign-in that registered it (S1, S3, S5) |
| `phase` | entry rule 5 (`idle -> storedBuilding`); the commit (`storedBuilding -> idle or browserSignIn`); S1 (`idle -> browserSignIn`); S3 and S5 (`browserSignIn -> idle`) |
| `authEpoch` for a stored install | the commit, only while a sign-in is registered |

Consequences, each a statement the tests check:

- **One authority for the stored grant (I12).** At most one `storedCall`
  exists; none exists or can start while `phase == browserSignIn`; after a
  commit installs a session the only refresher is its coordinator.
- **A cancelled sign-in leaves nothing behind.** After S3's deregistration no
  later commit moves the epoch for it or reports its build as that sign-in's
  success.
- **The stored build's lifetime is its own.** It does not end because its
  starter, or any waiter, stopped waiting. `Provider.Close` cancels it; its
  result is then discarded unwritten (15.4(1)).
- **No forced browser re-auth.** A browser opens in exactly the cases it
  opens in today.
- **Pinning is unchanged.** Today a stored session installed by
  `Authenticate` moves `authEpoch`, and one installed by `ensureSession` does
  not. The commit keeps both.
- **Waiting, stated honestly.** A sign-in can wait for the stored build
  before its browser opens. `sessionSetupTimeout` bounds that build's network
  setup only; the commit's local file write and lock waits come after it and
  are not covered by that figure. The wait is cancellable (S3).
- **Owner decisions O1 and O2** (15.4) approve the two visible changes in
  this section: a stored session that still needs interactive Web API
  authentication goes through the full existing interactive flow (no separate
  partial flow is kept), and a sign-in may wait, cancellably, for the stored
  build in flight.

`Session.reconnect` keeps the same shape: read `view` (generation, closed),
build with no lock, then `publish` under L3 revalidates `closed` and, for a
silent rebuild, `gen == from`.

### 4.3 Playback without `Session.mu` across librespot; retirement that never calls the SDK from a caller

Today `NewStream` read-holds `Session.mu` across librespot's stream setup
(`session.go:645-653`) and `publish`/`Close` write-hold it across
`closeLibrespot`. Two facts about the pinned SDK (go-librespot v0.9.1), both
found by the reviewer and both confirmed by reading the code, shape this
section:

- **F1. A setup can outlive its context.** `audio.KeyProvider.Request`
  enqueues without selecting on its context (`audio/provider.go:132-138`) and
  the pump sends with `context.Background()` (`:114`). What releases it:
  `Request` selects on `ap.Done()` at both of its waits.
- **F2. `Accesspoint.Close` can block.** It closes `done` first, then calls
  `closeConn`, then cancels the access point's context
  (`ap/ap.go:256-260`). `closeConn` needs `connMu.RLock` (`:469-475`), and
  `recvLoop` holds `connMu.Lock` across reconnect and its backoff
  (`:375-378`), on a context that is cancelled only by the step after the
  blocked one. Revision 3 called `Close` from the caller and said it could
  not block. That was wrong.

What the design uses is the one thing F2 leaves prompt: **`done` is closed by
the first statement of `Close`**, before anything that can wait. No dependency
change is needed.

```go
type playback struct {       // one librespot session and its player
    sess    *session.Session
    player  *librespotPlayer.Player
    devID   string
    ctx     context.Context  // ends when the bundle is retired
    cancel  context.CancelFunc
    users   sync.WaitGroup   // setup WORKERS running on this bundle
    retire  sync.Once
    gone    chan struct{}    // closed when the tail has finished
}
```

**Retiring a bundle** (publication, `Session.Close`):

- R1, under L3, by the caller: the bundle is detached (publication swaps in
  the new one; `Close` leaves none). From here no `users.Add` on it can
  happen, and no stream setup on it can be accepted (below).
- R2, after L3 is released, by the caller:
  `b.retire.Do(func() { b.cancel(); go b.tail() })`. That is all. **The
  caller makes no SDK call.** `publish`, `Session.Close` and
  `Provider.Close` return here.
- The tail, on the bundle's own goroutine, holding no lock:
  - T1. `go b.sess.Accesspoint().Close()` on a separate closer goroutine,
    because that call may block (F2).
  - T2. Wait for `b.sess.Accesspoint().Done()`. It closes as soon as any
    `Close` call has begun, the closer's or the SDK's own.
  - T3. `b.users.Wait()`.
  - T4. `b.player.Close()`.
  - T5. `b.sess.Close()` (events, dealer, then the access point's `Close`
    again, which waits for the closer's call to finish).
  - T6. `close(b.gone)`.

**Lifetime contract, with nothing claimed beyond it:**

| What | When |
|---|---|
| The caller returns | At once. It runs no SDK code and waits for nothing. |
| No new setup starts on the bundle | From R1. |
| Setups in progress are told to stop | From R2 (`b.cancel`) for context-bound waits; from T2 for the key-request waits. T2 does not depend on `connMu`. |
| The player is closed | After every worker has returned (T3, T4). Never under a live setup. |
| The old connection is closed, and the tail ends | **No bound is claimed.** Normally at once. If the SDK is reconnecting, `Close` waits for that attempt: its dials time out (30 s each, up to six), but its handshake reads have no deadline. Until then one closer goroutine, one tail goroutine (at T5), the old session object and possibly its socket stay alive. |

While a tail is stuck it holds no application lock and no reference anyone
waits on, and no other bundle, caller or request depends on it.

**What "retired" means (owner decision O4, approved 2026-10-12, 15.4).** For
1.0, "closed immediately after the swap" means **retired immediately from
ddsonic application use**:

- *Immediate:* the old bundle is excluded from all **new** application work
  (no new track open, no request, no credential write), and no caller waits.
- *Deliberately not ended:* streams accepted before the detachment keep
  playing over their own connections.
- *Not bounded:* the old SDK session's own work. Its access point's `Close`
  can wait on a reconnect (F2). Its dealer has the same shape: `Dealer.Close`
  closes the connection under a lock before it cancels
  (`dealer/dealer.go:121-129, 247-248, 321-326`), so T5 can stay stuck even
  after the access point has closed. Until the SDK's internal close
  completes, an authenticated connection of the old session may remain open.

**Retention while stuck, per retired object:** the closer goroutine, the tail
goroutine, the SDK's own goroutines for that session (its receive loops and
tickers, which are additional to those two), the session and player objects,
and its sockets. The same holds for a build worker stuck in the SDK (4.2).

**There is no aggregate bound.** Every retirement that happens while the SDK
is in that state adds one such set: an interactive sign-in, a silent playback
rebuild (which retires a bundle too, with no user action), a discarded or
late build. Nothing in this design caps their number or their lifetime; they
end when the SDK's attempt ends, or at process exit. Today the same SDK state
blocks `publish` and `Close` themselves, with `Session.mu` held, and
`Provider.Close` with `Provider.mu` held too; the design trades that hang for
this retention. The owner has accepted it as a documented 1.0 fault-time
resource limitation; no dependency fork or pin is added to force a physical
close. That acceptance waives no reviewer finding, no live acceptance, not
the security audit, and not later work on SDK lifetimes.

Once-only: `retire` is a `sync.Once`; `Accesspoint.Close`, `Dealer.Close` and
`Player.Close` are once-only in the SDK. A `Close` during a publication's
retirement, a second `Close`, and the SDK closing its own access point after
a failed reconnect all find the work done or in progress.

**Opening a stream: who owns the result, and its transport, at each moment.**
Two lifetimes are kept apart. The **setup** is the SDK's `NewStream` call; it
is bounded by the caller's setup deadline and ended by `b.ctx`. The
**stream** is what that call returns; it reads its audio over its own HTTP
context, created by the caller from `context.Background()` exactly as today
(`session.go:86-98`, `:640-654`). That context is not derived from the setup
deadline or from `b.ctx`, so an established stream outlives the setup, the
bundle and a sign-in: long-track continuity is unchanged.

That independence has a cost the protocol must pay: the SDK starts using the
stream's HTTP context **during** setup (it requests the first chunk on a
context of its own, through our client, `audio/chunked-reader.go:84-107`), so
ending the setup context does not end that request. Hence:

**The open handle.** Each open has one `openHandle`: the stream's HTTP
`cancel`, wrapped so that it runs once however often it is called. It exists
before the worker starts. **Until the stream is accepted, every way out of
the open calls it, at the point that exit is taken:** the caller's deadline
or cancellation, the bundle's retirement, an error result, a panic, a failed
acceptance, and the disposal of a late result. "At the point the exit is
taken" is exact for the branches that hold no lock (the caller's select, the
worker). In step 5 the exit is taken under L3, and L3 can be held for the
length of a local credential write (4.4); so no strict deadline is claimed
through a stalled local filesystem, only that an ended open is never
accepted once that lock is obtained. Only a successful acceptance transfers it: it is
then returned to the caller as the stream's cancel function, and from that
moment nothing in this protocol calls it; in particular retirement does not.
This keeps what `awaitSpotifyStream` does today on timeout and on error
(`session.go:121-130`).

State, one atomic per open: `pending`, `ready`, `abandoned`. The result
channel has capacity 1.

1. **Register.** Under L3: closed -> fail. Take the current bundle `b`;
   `b.users.Add(1)`. Release L3. Create the HTTP context and the handle.
2. **Worker** (a goroutine). Deferred, in order of execution: recover a panic
   into an error result; **if the result is an error, call the handle**;
   publish (step 3); `b.users.Done()` last. The reference covers the worker's
   whole life and ends only after the result has an owner.
3. **Publish** (worker). `CAS(pending -> ready)`:
   - succeeds: send the result (stream or error) on the channel; the send
     cannot block. **This is publication, not acceptance.**
   - fails (`abandoned`): the worker owns the result and disposes of it.
4. **Wait** (caller): select on the channel, the caller's context, and
   `b.ctx.Done()` (how the caller observes retirement).
   - **The channel fired:** go to step 5.
   - **A context fired:** **call the handle first** (this is what releases a
     worker stalled in the first chunk request), then
     `CAS(pending -> abandoned)`.
     - succeeds: return the cancellation, or "session changed" for a
       retirement. The worker disposes of whatever it later gets.
     - fails (the state is `ready`): receive the result (it is there, or
       arrives with the worker's next statement), dispose of it, return the
       same outcome as above. It is never returned as a stream and never left
       in the channel.
   - If both are ready, the select may take either branch; both are defined
     and both leave exactly one owner.
5. **Validate, for streams and errors alike, under L3, before anything is
   returned.** Two checks, in this order:
   - **(a) The caller is still asking.** If the caller's context has ended
     (its deadline passed or it was cancelled, at any time up to this check,
     including between receiving the result and obtaining L3, and including
     the case where result and context were both ready at the select):
     release L3; call the handle; dispose of a stream, once; and return the
     caller's **context outcome**. That outcome is not an auth error and not
     `ErrNeedsAuth`. Nothing is accepted, and an error result, a key error
     included, is **not** returned: an open that has ended cannot start the
     provider's silent recovery.
   - **(b) The result's origin is current.** The session is open and `b` is
     still its current bundle. If not: release L3; call the handle; dispose
     of a stream; and return **"session changed while the track was
     opening"**, whatever the result was. That outcome is not an auth error
     either: the provider's stream recovery (`provider.go:690-703, 745-765`)
     does not classify it as one, so an error produced by a retired bundle
     cannot start a silent reconnect of the session that replaced it, refuse
     it, or end its pins.
   - **Both hold, error result:** release L3 and return the error as it is.
     A genuine auth error from the live bundle, for a caller still asking,
     keeps today's recovery.
   - **Both hold, stream:** the stream is **accepted**; the handle is
     transferred. Release L3 and return the stream with its cancel function.

   **Acceptance linearizes at this point and only here:** a stream is
   established if and only if, under L3, its caller's context was live and
   its bundle attached. **The observation of a live context while holding L3
   is the acceptance linearization point.** The context is read once, there;
   it is not consulted again. A cancellation that arrives after that
   observation, even before the function has returned to its caller, does not
   retroactively cancel the stream: the handle has been transferred and
   nothing in this protocol calls it. A detachment after it does nothing
   either. This is the O3 boundary.

As a second guard, the provider's recovery is tied to what it tried:
`NewStreamer` asks for a silent reconnect of the generation it opened under,
and a reconnect asked for a generation that is no longer current does
nothing.

**Disposing of a stream that was never accepted.** The disposer is the
result's one owner, the SDK call that produced the stream has returned, and
no reader was ever given it, so the disposer has exclusive ownership. It:

1. calls the handle (idempotent), ending any chunk request;
2. **closes the decoder, once:** `stream.Source` is the concrete SDK decoder
   (`player/player.go:887-958`); if it has `Close() error` (MP3, FLAC) or
   `Close()` (Vorbis) that method is called; an error is logged;
3. drops its references.

Step 2 is needed and revision 4 was wrong to forbid it: the MP3 decoder
registers a `cgo.Handle` to itself (`mp3/decoder.go:94`) that only `Close`
deletes (`:127-139, 223-225`), and there is no finalizer, so dropping
references can never free it. Called once, by an exclusive owner, on a
decoder nobody reads, each `Close` is the decoder's own documented release:
Vorbis takes its lock and is idempotent; MP3's `free` is idempotent; FLAC's
deletes the decoder and its client data and is not idempotent, which is why
"once" matters. **CORRECTION (2026-10-13, review round 9): the next sentence
is false for Vorbis.** go-librespot v0.9.1's `vorbis.Decoder.Close` can abort the
process on a decoder nobody has read. The implementation does not close a never-accepted
Vorbis decoder. **Owner's decision (2026-10-13, `plan.md`, R9-4 and the
Decisions log):** that is accepted as a documented 1.0 resource and
reliability limitation, measured on the next candidate before release, and
the owner handles a report of the SDK defect to upstream, outside this repository;
the SDK is not
patched, forked or re-pinned for it. It is not a correctness or
credential-integrity defect and not a security waiver. MP3 and FLAC are closed
as this section says, and tested with real decoders. The original sentence follows. I found nothing in the pinned code that makes any of the
three unsafe under those conditions; that is a code reading plus the tests of
14.5, not a proof for every platform.

**Not changed, and stated as an existing defect:** an **established**
stream's teardown (`spotifyStreamer.Close`, `streamer.go:134-153`) still only
cancels and clears references, by a deliberate upstream-era policy about
closing decoders during a track handoff. For MP3 tracks that leaves the same
`cgo.Handle` rooted for every track played. Changing it is outside this
design's authorization; it is reported to the dispatcher (completion report)
and listed as risk (d6).

What is claimed about opening:

- **Exactly one owner** of a result at all times.
- **The HTTP lifetime of an unaccepted open always ends**, at the first of:
  caller gives up, bundle retired, error, failed validation.
- **The worker's reference is not held by a slow caller.**
- **A retired bundle's result, stream or error, never reaches the caller as
  a stream or as its own error.** A caller still asking gets "session
  changed"; a caller whose context has also ended gets its context outcome,
  because check (a) comes first. Both are non-auth outcomes.
- **Owner decision O3** (15.4): a track open still in progress when its
  session is replaced or closed fails immediately, and replacement or close
  never waits on it. A track open is an established stream if and only if
  its acceptance in step 5 happened before the bundle's detachment.

`Provider.Close`: under L2, `closed`, cancel the sign-in and the stored
build, take and clear `p.session`; release L2; `sess.Close()`.

### 4.4 Token refresh, requests, and the credential file

- **Token refresh.** `renew` decides under L5 (join, start, or return), then
  releases L5. The flight goroutine calls `silentTokenRefresh(c.ctx, ...)`
  holding no lock. It is bounded by `tokenHTTPClient` (30 s per attempt) and
  ended by `c.cancel`. The answer is settled in a durable section (section
  8), and `flight.done` is closed after that section ends.
- **Waiters** hold no lock: `select { <-flight.done; <-ctx.Done() }`.
- **Web API request.** The driver holds no lock while sending.
- **The credential file write is local disk I/O under locks, stated
  plainly.** It always happens under L4. At the acceptance points, at a
  silent rebuild's `setIdentity` and at `Close` it also happens under L3, and
  at first install and the stored build's commit under L2. L2 is taken by
  every Web API read (to read `p.session`); L3 by every stream open. This is
  not network I/O and is outside the rule above; it is kept because `Close`
  and publication must be ordered against the write (I6).
  **Read-latency cost:** while one of those writes is in progress, a Web API
  read that needs L2 (first install or stored commit only) and a stream open
  that needs L3 wait for one temp-file write and rename of a file under 1 KB.
  A flight's settle writes under L4 only, so by itself it delays no reader.
  But a `Close`, a silent publication or a replacement that arrives during
  that write waits for L4 **while holding L3**, and a first install or stored
  commit while holding L2: for the length of that one file write, stream
  opens (L3) or Web API reads (L2) wait behind them. On a healthy local disk
  this is well under a millisecond; on a stalled filesystem it is as long as
  the stall, for those waiters.

## 5. Retry state: one logical request

```go
type apiCall struct {
    method, path string; query url.Values; body []byte; contentType string
    accept    []int
    waitRate  bool               // webAPIWithBody: true; webAPIOnce: false
    pin       *signInPin         // writes only
    // state, owned by this request alone
    renewedIn *tokenCoordinator  // the sign-in whose one recovery renewal is spent
    authRound int                // backward edges taken for auth; at most 3
    rateTry   int                // backward edges taken for a 429; at most 7
}
```

`webAPIWithBody` and `webAPIOnce` each build one `apiCall` and call
`p.request(ctx, call)`. This section and the table in 16(d) describe the same
machine; where prose and table could differ, the table is the rule.

**The canonical backward edge.** The loop returns to step 1 only through one
of two functions, and each checks its limit **before** it moves:

- `backAuth()`: if `authRound == 3`, fail with "the Spotify session kept
  changing during the request"; else `authRound++` and go to 1.
- `backRate(wait)`: if `rateTry == 7`, fail as rate-limited; else
  `rateTry++`, wait (ended by `ctx`), and go to 1.

There is no other way back to step 1.

1. Rate gate (`p.rateLimited()`), as both helpers do now.
2. `sess := p.session` under L2; none: `playlist.ErrNeedsAuth`.
3. Write: `pin.holds(sess)` or `errSignInChanged`.
4. `v := sess.view.Load()`; closed: `errSessionClosed`; `playbackRefused`:
   `errSignInRefused`.
5. `lease, access, err := v.tokens.token(ctx)`.
   - superseded (the coordinator closed while waiting): a write ends with
     `errSignInChanged`; a read takes `backAuth()`.
   - refused, no refresh token: `playlist.ErrNeedsAuth` (typed as today).
   - anything else: the outage error, as today.
6. Write: `pin.holds(sess)` again, immediately before sending.
7. Send. Not 401 and not 429: return it (the helper applies `accept`).
8. **401:**
   - `call.renewedIn == lease.c` (this request's recovery renewal for this
     sign-in is spent): if `lease.c.refuse(lease)` then `errSignInRefused`.
     Otherwise the lease was superseded: a read takes `backAuth()`, **without
     renewing**. (A write never reaches here twice.)
   - otherwise set `call.renewedIn = lease.c` and `renew(ctx, lease)`:
     refused -> `ErrNeedsAuth`; outage -> `rejectedWrite(method, err)`;
     superseded -> a read takes `backAuth()`, a write ends
     `errSignInChanged`; success -> a read takes `backAuth()`; a write runs
     the existing check (`GET /v1/me` sent directly with `token(ctx)`, not
     through the driver): 401 there -> `refuse(probeLease)` -> `ErrNeedsAuth`
     or `errSignInChanged`; anything else -> the existing "not authorised,
     not sent again; the sign-in has been renewed" `notSent`.
9. **429:** `waitRate` false -> today's `webAPIOnce` rule (`block` on
   `Retry-After`, else `*catalog.RateLimitError`), return. `waitRate` true ->
   today's `webAPIWithBody` rule: a `Retry-After` beyond the inline maximum
   closes the gate and fails; otherwise `backRate(wait)`.

Invariants:

- **Q1.** `renewedIn`, `authRound` and `rateTry` are written only by this
  loop, live as long as the `apiCall`, and are never decreased or reset.
  `backRate` writes `rateTry` only.
- **Q2 (recovery renewals).** A request calls `renew` explicitly, after a
  401, at most once per coordinator. This is the budget R8-2 is about, and it
  is today's rule ("once per sign-in").
- **Q3 (automatic refreshes are not budgeted, and are bounded by visits).**
  Step 5 calls `token()` once per visit, and `token()` starts or joins a
  flight only if the lease is unusable at that moment. That is the existing
  behaviour of the `oauth2` source (it refreshes whenever it is asked while
  expired) and this design sets no new limit on it. It is bounded because
  visits to step 5 are: each visit after the first follows a backward edge.
- **Q4.** A write is sent again only after a 429 (body replayed, pin
  rechecked at steps 3 and 6), never after a 401.
- **Q5.** Independent requests have independent `apiCall`s; nothing about a
  budget is stored on the session or coordinator.

Bounds, derived in 16(d): a read makes at most 11 Web API attempts and a
write at most 9; recovery renewals are at most 4 per read (1 per sign-in) and
1 per write; token flights started or joined are at most 15 per read and 10
per write.

Preserved: `webAPIWithBody` waits out a 429, `webAPIOnce` does not; 403, 5xx
and transport errors are not auth; the write-side `GET /v1/me` check;
`rejectedWrite`, `notAppliedError`, `notSent` and their `ErrNotApplied`
identity; cancellation ends a rate wait and a token wait.

## 6. Mutation semantics

Nothing in action pinning changes: `provider.ActionContext`, `WithSignIn`
called on the first line of `CreatePlaylist` and `AddTrackToPlaylist` and at
request arrival in the GUI, daemon and overlay, `provider.AddTracks`,
`authEpoch`, `signInPin.holds`, `errSignInChanged`, `provider.ErrNotApplied`,
`provider.NotApplied`, `PartialError`.

- Reads may be sent again after a successful renewal, and on the new sign-in
  after an interactive sign-in (step 5 and step 8).
- A write is bound to the sign-in that stood when its action began. After any
  interactive acceptance (`authEpoch` moved), or any publication into its
  session (`gen` moved, which includes a silent playback rebuild, as today),
  it is not sent, and nothing resumes it. An explicit sign-in is not a fresh
  mutation action.
- A token renewal inside one sign-in (a new lease on the same coordinator) is
  not a new sign-in and does not end an action: that is today's behaviour
  (`tokenRev` never broke a pin).
- The epoch moves before the new view is stored (I9), so a writer that saw
  the new sign-in cannot see the old count. A writer that loaded the old view
  and old epoch and then sends is the same window that exists today between
  `accessToken`'s last check and the HTTP send.

## 7. Session and auth interaction

1. **First-run sign-in** (provider has no session, no credential file).
   `signIn`: the stored phase finds no credential (S3 of 4.2), then
   `newInteractiveSession`: the exchange seeds a new coordinator C (`rev 1`),
   credential staged in `C.creds`, `dirty`. Under L2: `p.session = fresh`,
   `authEpoch+1`, `credStore.claim(C)` writes the file.
   A failed or cancelled flow built nothing or closes an unclaimed build: no
   write (I9).
2. **Post-reset sign-in.** Identical: `ddsonic spotify reset` deleted the
   file, the stored phase finds none, the interactive path runs.
2a. **Sign-in while the stored credential still works** (the user asks
   before startup's build has run, or while it runs): the shared stored build
   commits and the sign-in ends without a browser, as today.
3. **Revoked or expired stored credential at startup.** `ensureSession` ->
   stored builder -> setup refresh answers `invalid_grant` ->
   `errStoredSignInRefused`, remembered as `p.storedRefused`; the file is not
   touched. An outage at setup yields a session whose coordinator has no lease
   but keeps the refresh token: the next request's `token()` starts a flight.
4. **Mid-session `invalid_grant`.** A flight settles with `invalid_grant`:
   `refused` for the current revision, `ErrNeedsAuth` to every waiter, memory
   only. `creds` and the file are unchanged (I5, I8). The sign-in that follows
   is 6.
5. **Playback reconnect (silent).** `Session.Reconnect` builds a librespot
   session and player from `tokens.snapshot()` (username, data, device id) and
   does **no** token refresh. `publish` (silent): if `gen != from` or the
   session is closed, the build is closed and nothing changes; otherwise swap
   the bundle, `tokens.setIdentity(...)` in a durable section (which writes
   it), `gen+1` (pins end, as today), clear `playbackRefused`, store the
   view; then retire the old bundle (4.3). The coordinator, its
   lease and its refresh token are untouched. Playback's
   `refuseSignIn(gen)` after a failed reconnect is unchanged.
6. **Session replacement by interactive sign-in.** `reconnect` builds fresh
   with coordinator C2. `publish` under L3: `authEpoch+1`, `view.Store` with
   C2 and `gen+1`, then one durable section that closes C1 (cancelling its
   flight) and claims and writes C2. L3 is released and the old bundle is
   retired at once (4.3): the caller does not wait; its player is closed
   once its setup workers have returned; when its connection closes is not
   bounded. A stream already open
   keeps its own connection, as today.
7. **Provider search, playlist picker, library reads.** All are `apiCall`s.
   A read pending across a sign-in is sent on the new sign-in; the overlay's
   writes are pinned at command construction (unchanged).
8. **`Provider.Close`.** Under L2: `closed = true`, `authCancel()`,
   `storedCancel()`, the session detached. Outside L2, `Session.Close`:
   under L3 the view is stored closed and the coordinator closed in a durable
   section (section 8); outside L3 the bundle is retired (4.3). A flight in the air is cancelled; if its answer still arrives it is
   dropped (I2) and not written (I8). A build finishing afterwards is not
   taken (4.2 step 6, `signIn`) and is closed unclaimed.
9. **Stale completion or cancellation.** A waiter whose context ends returns
   its context error; the flight is unaffected (I11). A sign-in cancelled
   before its build returns publishes nothing; one cancelled after `publish`
   is complete (round 2's committed-outcome rule, unchanged).
10. **Startup build and a sign-in at the same time.** They are the same
    stored build (4.2); the browser phase starts only after it has settled,
    and while the browser is open a startup caller gets "sign-in in
    progress" and starts no build.
11. **Concurrent startup callers** (sync, playback, a library read) share one
    build and wait for it on a channel, holding no lock (4.2).

## 8. Credential handling: one linearization for acceptance, write and close

**The durable section.** Every operation below has this one shape, and there
is no other code that writes the file or sets `closed`:

```
lock L4
  lock L5
    if c.closed: unlock L5, unlock L4, return "dropped"   // never accepted
    memory step                                            // the acceptance
    snapshot c.creds, c.dirty
  unlock L5
  if credStore.owner == c and dirty:
      err := saveCreds(snapshot)        // temp + rename, 0600; no L5 held
      if err == nil: lock L5; c.dirty = false; unlock L5
      else: log (applog.UserError); c.dirty stays true
unlock L4
```

| Operation | Called from | Memory step |
|---|---|---|
| settle (success) | the flight goroutine, no other lock | `rev+1`, lease, expiry; if the refresh token rotated: `creds.RefreshToken`, `dirty = true` |
| settle (`invalid_grant`) | the same | `refused@rev`. The refused answer carries nothing to persist and changes no credential. If the coordinator is the owner and still `dirty` from an earlier failed write, the section's write step retries **that earlier, already-accepted** credential, as every section does; nothing of the refusal is written. |
| `setIdentity` | silent publication, under L3 | librespot half of `creds`, `dirty = true` |
| `claim(c)` | the three acceptance points, under L2 or L3 | `credStore.owner = c` (set under L4, before the L5 step) |
| replacement | interactive publication, under L3 | first, under L5 of the old coordinator: `closed = true`, cancel its context; then `claim(new)` as above, in the same L4 hold |
| `close()` | `Session.Close`, under L3 | none before the write; **after** the write step: lock L5, `closed = true`, cancel; `owner = nil` if it was `c` |

`close()` is the one row whose closed transition comes after the write step:
if `c` is the owner and still `dirty` (an earlier write failed), `close()`
makes one more attempt to write the already-accepted `creds` while the
coordinator is still open, then closes. It accepts nothing new.

What the protocol gives, against the reviewer's two orderings:

- **Ordering A (a file renamed after close)** cannot occur. `closed` is set
  only inside a durable section; a write happens only inside a durable
  section, for a coordinator that section found open; sections do not
  overlap. A `Close` that arrives during a write waits on L4, and the closed
  transition is postponed until the rename has returned.
- **Ordering B (an accepted rotation skipped by close)** cannot occur.
  Acceptance (the memory step) and its write are in the same section; `close`
  is before both or after both.
- **The accepted limitation is not widened.** The loss the owner accepted is
  of a result that did not reach acceptance: a flight whose settle finds
  `closed`, or a build that is never claimed. That is unchanged. An accepted
  result is lost only if the local write itself fails, and then it stays
  `dirty` and is retried at the owner's next durable section and at `Close`.

Other properties:

- **First write.** A coordinator is `dirty` from construction, so the first
  `claim` always writes the seed, whatever any earlier owner wrote.
- **Owner change.** `dirty` belongs to the coordinator. A new owner with any
  history writes its own `creds` at `claim`; nothing is compared with the
  previous owner. A replaced owner that was still `dirty` is closed in the
  replacement section and never writes: its credential is superseded.
- **Monotonicity** is within one owner: all its credential changes are
  serialized by L4 and each write is of the state its section just produced.
- **Older callbacks overwriting newer credentials:** there are no callbacks.
- **A failed interactive flow altering the stored credential:** its
  coordinator is never claimed (I7, I9).
- **Obsolete refresh restoring a superseded refresh token:** within one
  coordinator there is one flight at a time (I1); across coordinators the
  obsolete one is closed; before acceptance there is one stored build (I12).
- **Duplicate persistence:** a section writes only when `dirty`.
- **Write failure** is logged and does not fail the token, as
  `persistingTokenSource` behaves today.
- **Waiters** of a flight are released after its section ends: a caller for
  whom `renew` returned knows the write was attempted.
- **Preserved:** the stored credential is deleted only by
  `ddsonic spotify reset` (`DeleteCreds` keeps its one production caller); a
  staged credential is written only after the player started and the session
  was accepted (`claim` is reached only then); atomic temp + rename, 0600.

## 9. Closure of R8-1 and R8-2

**R8-1** had three shapes. The invariant that makes each impossible:

- (a) *An older forced answer overwrites a newer automatically refreshed
  credential.* Requires two refresh results for one sign-in to be in the air
  together. I1 forbids it: the "automatic" reader that needs a token while the
  forced flight is in the air joins that flight. There is one answer; it is
  the newest. Across sign-ins, I2/I8/I9 drop the old coordinator's answer.
- (b) *An older forced `invalid_grant` refuses a newer automatically
  refreshed token.* Same: I1. The refusal recorded by `settle` is always
  about `base == rev`, the current lease (I3).
- (c) *A stale 401 on the forced token refuses a newer token the same source
  refreshed by itself, including by real expiry.* The automatic refresh is a
  flight, so it published `rev+1` (D3; this is the reviewer's "stamp the
  actual accepted token publication, including automatic refresh"). The late
  401 carries the old lease; `refuse` requires `l.rev == c.rev` (I3) and
  does nothing. The read goes round once more on the current lease without
  renewing (step 8) and succeeds.

Root cause, stated once: `tokenRev` versioned source replacement. `rev` now
versions every publication, because there is no publication that is not a
`settle`.

**R8-2.** By Q1 and Q2 the renewal budget is a field of the logical request
and the 429 branch cannot write it. In the reviewer's sequence (401, renewal,
429 with `Retry-After: 1`, 401) the second 401 finds `renewedIn == lease.c`
and the lease current, so `refuse` applies: one renewal, three requests,
`ErrNeedsAuth`.

## 10. Complementary-ordering matrix

**What this matrix covers, exactly.** It is an enumeration over four state
machines defined in this document, and it is complete for those and for
nothing else:

- **(S-T) token state of one coordinator:**
  `(closed, rev, refused@refusedRev, flight?, creds, dirty)`.
- **(S-F) the file:** `(owner, the owner's dirty)`, changed only in durable
  sections.
- **(S-B) session builds of one provider:** `(session?, phase: idle |
  storedBuilding | browserSignIn, stored call?, registered sign-in?,
  storedRefused, closed)` and, per build call, `(pending | ready | abandoned,
  context ended?)`.
- **(S-P) one playback bundle:** `(attached | detached, retired?, worker
  count)` and, per open, `(pending | ready | abandoned, accepted?, handle
  called?, decoder closed?)`.

For S-B and S-P the claim is narrower than for S-T and S-F: the rows cover
every pair of the transitions listed below, and those transitions are every
place sections 4.2 and 4.3 write the state above; nothing is claimed about
transitions an implementation might add. Revision 3's rows for these two
machines missed the phase, the starter's wait and the ownership of a
published result; they are replaced.

It does **not** cover, and no completeness is claimed for: the internals of
go-librespot beyond the waits named in 4.3; Spotify's server-side behaviour
(for example what it does with two refreshes of one token; the design avoids
sending them rather than reasoning about the answer); the operating system's
file semantics beyond atomic rename; or code outside the Spotify package.
Revision 2's sentence that the matrix was closed over "any ordering of token
events" is withdrawn: its flush was two steps, seed acquisition was outside
it, and worker lifetime was outside it.

Transitions:

- S-T, each one durable section or one L5 section: T1 settle(success), T2
  settle(`invalid_grant`), T3 settle(other error; no change), T4 `refuse(l)`
  (L5 only; guard `!closed && l.rev == rev`), T5 `close`, T6 `setIdentity`.
  T1, T2, T3 are mutually exclusive in time (I1).
- S-F: W write-for-owner (inside T1, T6, claim, close), C claim, X
  replacement (close old + claim new).
- S-B, each one L2 section: SB1 stored build starts (`idle ->
  storedBuilding`); SB2 commit installs; SB3 commit discards or fails; in
  SB2/SB3 the phase goes to `browserSignIn` if a sign-in is registered and
  still needs the browser, else `idle`; SB4 a sign-in registers; SB5 it
  deregisters on cancellation; SB6 the browser build is installed or
  published and the sign-in ends; SB7 `Provider.Close`; SB8 a caller is
  turned away ("sign-in in progress").
- S-P: P1 worker registers (L3); P2 worker publishes (`pending -> ready`);
  P3 caller abandons (`pending -> abandoned`); P4 caller validates and
  accepts (L3; inputs: caller's context live?, bundle attached?); P5 detach
  (L3); P6 retire (cancel, start the tail); P7 the access point's `done`
  closes; P8 the tail closes the player; P9 the caller's context ends (an
  input that can arrive at any point).

Durable sections are serialized by L4, so any two of T1, T2, T5, T6, C, X are
ordered, and each sees the state the previous one left. T4 is serialized with
them by L5. That is the whole argument for S-T and S-F; the rows spell out
the pairs.

| # | Competing events | Order | What commits, and why |
|---|---|---|---|
| 1 | old refresh success vs new refresh success, one sign-in | any | Cannot be concurrent (I1). In sequence, flight k publishes `r+1`, flight k+1 starts from `r+1`; both commit, in order. |
| 2 | forced renewal vs automatic refresh | forced first | The automatic caller joins the forced flight. One request, one lease, one write. |
| 3 | forced renewal vs automatic refresh | automatic first | The forced caller (basis = current) joins; or, arriving after settle, finds `rev > basis` and returns the current lease with no request. |
| 4 | old refusal (T4 on lease r) vs new success (T1 from base r) | T4 first | T4 commits (`refused@r`); T1 commits `r+1`; refusal superseded (I4). The callers of T4 got `ErrNeedsAuth` for their request; the sign-in is healthy. |
| 5 | same | T1 first | T1 commits; T4's guard fails (`r != r+1`): dropped. Read retries on `r+1`. |
| 6 | old refusal (T2) vs new success | - | Cannot compete (I1). After T2 no flight starts (I4), so no success follows in this sign-in. |
| 7 | old success vs new refusal (T1 from base r, T4 on lease r+1) | - | T4 on `r+1` presupposes T1 settled. Sequential: both commit; the refusal is the newer fact. |
| 8 | old success vs new refusal (T1 from base r, T2) | - | Cannot compete (I1). |
| 9 | T4 vs T2 (401 refusal vs `invalid_grant`, same lease) | any | Both guards hold; both set `refused@r`. Idempotent. |
| 10 | T4 vs T4 (two requests, same lease) | any | First commits; second finds it current and refused: both report `ErrNeedsAuth`. |
| 11 | two forced renewals, same basis | any | One flight (I1); or the later finds `rev > basis`. |
| 12 | T1 (rotating) vs X (interactive replacement) | T1's section first | C1's lease and rotated token are accepted and written in T1's section. X then closes C1 and writes C2 in its own section. C2's credential is the last on disk. |
| 13 | same | X's section first | C1 is closed inside X. T1's section finds `closed`: dropped, nothing written. Waiters get "superseded": reads move to C2, writes end unsent. |
| 14 | same | T1's write in progress when X arrives | X waits on L4 until T1's rename has returned, then runs as row 12. No write of C1 can follow X. |
| 15 | T1 (rotating) vs T5 (close) | T1's section first | Accepted and written, then closed (I6). If the write failed, T5 retries it once before closing. |
| 16 | same | T5's section first | T1 finds `closed`: dropped, not written. The accepted limitation, 15.4(1): the result never reached acceptance. |
| 16a | same | T1's write in progress when Close arrives | Close waits on L4; the closed transition follows the rename. Ordering A excluded. |
| 16b | T1's memory step done, Close arrives before the write | - | Not an interleaving: the memory step and the write are one L4 hold. Ordering B excluded. |
| 16c | joined flight, T1 vs T5 | any | Waiters are released after T1's section. They get the lease (T1 first) or "superseded" (T5 first); never a lease whose rotation was skipped. |
| 16d | write fails in T1 | - | Lease published, `dirty` kept; the next section of this owner, or T5, writes it. If replaced first (X), it is superseded. |
| 17 | refusal vs session replacement | refusal first | C1 refused; publication replaces it with C2, unrefused. |
| 18 | same | X first | Refusal carries C1; C1 closed: dropped. Playback's `refuseSignIn(gen)` with the old `gen`: no-op. |
| 19 | rate-limit wait vs recovery renewal (own) | - | Q1: the wait cannot reset the budget. |
| 20 | rate-limit wait vs another request's renewal, or expiry | - | After the wait, step 5 takes the current lease, refreshing automatically if it expired (Q3). |
| 21 | two credential changes of one owner (T1 vs T6) | any | Two sections; each writes the state it produced; the later one's file is the later state. |
| 21a | C: first claim of a coordinator | - | `dirty` from construction: the seed is written. |
| 21b | C: new owner after an owner with any history | - | The new owner's `dirty` decides; it writes its own `creds`. |
| 22 | cancel (caller) vs T1 | any | The flight commits regardless (I11); the caller returns its context error. |
| 23 | cancel (sign-in) vs publish (session) | cancel first | The build fails; nothing is claimed. |
| 24 | same | publish first | The sign-in stands; the file was written inside the publication, before `Close` can take L3. |
| 25 | silent playback rebuild vs interactive sign-in | any | The silent publication is conditional on `gen == from`; if the sign-in won, the rebuild is closed. If the rebuild won, the sign-in replaces it. |
| 26 | silent playback rebuild (T6) vs flight (T1) | any | Row 21. |
| 27 | startup `ensureSession` vs `Authenticate`, no session, stored credential present | startup first | `Authenticate` registers (SB4) and joins the stored call. One refresh of the stored token. On SB2 the sign-in is done without a browser and the epoch moves in the commit. |
| 27a | same | `Authenticate` first | It starts the stored call (SB1); startup callers join. Same outcome. |
| 27b | same, stored refresh rotates the token | any | One answer; it is the seed of the one build; written at SB2. |
| 27c | same, stored refresh `invalid_grant` | any | SB3 sets `storedRefused` and hands the phase to the sign-in; startup callers get `ErrNeedsAuth`; one browser flow. |
| 27d | sign-in cancelled while the stored build runs, startup-first | cancel before commit | SB5: deregistered. The build goes on; its commit moves no epoch and hands no phase; the sign-in returns the cancellation. |
| 27e | sign-in cancelled, `Authenticate`-first, no other caller | cancel before commit | The same: the starter stops waiting like any waiter; the build it started continues as a background build; a startup caller arriving later joins it. |
| 27f | sign-in's context ends and the commit installs, at the same time | any | S3 decides under L2 from the state: commit first -> healthy session, success, epoch moved; deregistration first -> cancellation, the later commit moves no epoch. |
| 27g | stored build fails for a timeout or network error while a sign-in waits | - | SB3 moves `storedBuilding -> browserSignIn` in one section; no caller sees `idle`. |
| 27h | later `ensureSession` while the browser is open | any | SB8: turned away at once with `ErrNeedsAuth`; no stored call, no refresh, nothing changed. |
| 27i | browser sign-in ends (success, failure, cancellation) vs a startup caller | sign-in first | SB6 sets `idle` (or a session exists); the caller sees a session, or starts a stored build if there is still none. |
| 27j | same | caller first | Row 27h. |
| 27k | `Provider.Close` while the stored build runs | any | SB7 cancels it; SB3: closed unclaimed; a rotated answer is lost before acceptance (15.4(1)). |
| 27l | sign-in joins a sign-in | - | The second `Authenticate` waits on the first's `signInCall`; it never registers and never touches the phase. |
| 27m | build worker result vs the call's deadline | result first, context live | Completion step installs or records the failure as rows 27-27c. |
| 27n | same | deadline first | The controller abandons; the call completes with the deadline error; phase released (or handed to a registered sign-in); `storedRefused` unset. The worker's late result, success included, is retired by the worker: not installed, not claimed, not written. |
| 27o | same | both ready | Whichever branch the controller takes, the completion step's check "context not ended" decides under L2: ended -> row 27n's outcome with the received result retired by the controller. |
| 27p | worker stuck in an SDK getter or in the builder's own cleanup, past the deadline | - | Row 27n. Waiters are released at the deadline; the next `ensureSession` starts a new call; the stuck worker, when released, finds a dead context and sends no refresh. |
| 27q | browser build or silent rebuild whose worker never returns | - | The same wrapper: the sign-in or reconnect ends on its own context; `signingIn` and the phase are cleared by it. |
| 28 | `Provider.Close` vs sign-in in flight | any | A closed provider takes nothing; an untaken build is closed unclaimed. |
| 29 | restart after rotation | - | The file holds what the owner's last successful section wrote; the next start's stored build uses it. |
| 30 | request sent on lease r, lease r+1 published, answer 200 | - | Nothing to commit; a 200 carries no token state. |
| 31 | P1 (worker registers) vs P5 (detach) | P1 first | The worker is on the old bundle; the tail waits for it at T3; `b.ctx` and then `done` tell it to stop. |
| 32 | same | P5 first | The worker takes the new bundle (publication) or the open fails (closed). |
| 33 | P2 (worker publishes) vs P3 (caller abandons) | P2's CAS first | State `ready`. The caller's CAS fails; it receives the result, disposes of it, returns the cancellation. One owner: the caller. |
| 33a | same | P3's CAS first | State `abandoned`. The worker's CAS fails; it disposes of the result. One owner: the worker. |
| 33b | P2 done, delivery not yet received, then cancellation or detach | - | Not a third case: `ready` means the caller owns it. Cancellation: row 33. Detach: the caller receives, fails acceptance (row 33c), disposes. |
| 33c | P4 (accept) vs P5 (detach) | P4 first | Both under L3. The stream is established; retirement does not touch it. |
| 33d | same | P5 first | Acceptance fails; the caller disposes of the stream and returns "session changed". |
| 33e | worker returns an error, or panics; bundle current | any | The worker calls the handle, then publishes. The caller validates origin under L3: current -> the error is returned as it is; a live auth error keeps normal recovery. |
| 33f | worker published an error (for example a key error) for bundle b; b is then detached; the caller's select takes the result | - | Origin validation fails: "session changed", not the old error. No reconnect, refusal or pin change on the replacement. |
| 33k | P2 success, the caller takes the result branch, then P9 (context ends) before P4 obtains L3; bundle current | - | P4's check (a) fails: handle called, stream disposed once, the context outcome returned. Not accepted. |
| 33l | same, with a published key error instead of a stream | - | Check (a) fails: the context outcome is returned, not the key error. No silent recovery of the current session, no refusal. |
| 33m | result and caller's context both ready at the select; the result branch is taken | - | Rows 33k and 33l: check (a) decides under L3, whichever branch the select took. |
| 33n | P9 while P4 waits for L3 behind a local credential write | - | The open is rejected when L3 is obtained. No bound on that wait is claimed beyond the write's own (4.4). |
| 33o | P4 accepts, then P9 | - | The stream is established; the late cancellation does nothing to it. |
| 33p | caller's context live, bundle current, error result | - | Returned as it is; a live auth error is recovered as today. |
| 33g | caller gives up while the worker is stalled in the first chunk request | - | The caller calls the handle before its CAS; the request ends; the worker returns and releases its reference; the tail proceeds. |
| 33h | bundle retired while a worker is stalled in the first chunk request | - | The caller sees `b.ctx.Done`, calls the handle: as row 33g. |
| 33i | late success after abandonment | - | The worker disposes: handle (already called), decoder closed once, references dropped. |
| 33j | accepted stream vs any later retirement, cancellation of the setup context, or sign-in | - | Nothing calls its handle; it keeps reading. |
| 34 | worker stalled in the key pump vs retirement | - | P6 then P7: `done` closes with the first statement of the SDK's `Close`, run on the closer goroutine; the worker returns; P8. No caller waited. |
| 34a | SDK reconnecting when the bundle is retired | - | The closer's `Close` blocks after closing `done` (F2). Workers are released (P7); the player is closed (P8); the tail waits at T5 until the SDK's attempt ends. No caller, lock or other bundle waits. |
| 34b | SDK closes its own access point (failed reconnect) vs retirement | any | `Accesspoint.Close` is once-only; `done` is closed either way. |
| 35 | caller's deadline passes while the worker is in the SDK | - | The caller abandons (row 33a) and returns; the worker keeps the reference until it returns. |
| 36 | `Close` vs a publication's retirement of the same bundle; `Close` twice | any | Detachment under L3 and `retire.Do` make it once. |

## 11. R8-3: an explicit refresh is never lost behind an older run

**Owner decision (2026-10-12): the broader rule is approved.** An explicit
refresh of a non-quiet provider that arrives after the prior sync visibly
completed, but before that run released the provider, must not be dropped.
That covers the sync a successful sign-in requires and an explicit `r` in the
same window. Quiet and background refresh semantics are not broadened.

**Defect.** `librarySignedIn` calls the ordinary `refresh`
(`library_signin.go:261`). `catalogRuntime.sync` returns when `ps.running`
(`main_catalog.go:326-330`) and only a quiet source runs again (`:349`). The
engine emits `Finished` before the runtime gives the provider up, so a request
made by anyone who has seen the completion can still be dropped.

**Design.** In `main_catalog.go` (`providerSync` fields guarded by `rt.mu`),
for non-quiet providers only:

- `owed bool`: one more run is to follow the current one.
- `requiredSeq uint64`, `serving uint64`: a counter of required requests, and
  the highest one the current run was started to serve.
- `announced bool`: the current run's `Finished` has been emitted; set in
  `notify` under `rt.mu` **before** the message is sent (the lock is released
  before sending), cleared when a run starts.

Requests are of three kinds:

| Request | Source | While a run owns the provider |
|---|---|---|
| required | a successful sign-in (`refreshRequired`) | always sets `owed` |
| explicit | `r` (`refresh`) | sets `owed` only if `announced`; before that it is coalesced into the running sync, as today |
| background | the retry timer, the startup sync | never sets `owed`; a no-op, as today |

`rt.sync(provider, kind) (ticket uint64)`:

- not running: start a run as today; `serving = requiredSeq` (after
  incrementing it if required).
- running: apply the table; a required request also does `requiredSeq++`.
- returns `requiredSeq` when required, else 0; 0 also when closed or
  without an engine.

Release, in the existing critical section that decides to stop:
`again = !rt.closed && rt.ctx.Err() == nil && !ErrRunning && (owed ||
quietRule)`, where `quietRule` is today's expression, untouched. If `again`
by `owed`: `owed = false`, `announced = false`, `serving = requiredSeq`, all
in that same critical section, then loop; the retry timer is **not** settled
for the run that is being followed (the follow-up settles its own). Otherwise
exactly today's path. Deciding and giving up remain one step.

`notify` adds `Ticket: ps.serving` to `CatalogSyncMsg`. The UI gets the
required form through `SetCatalogSync` (a second function,
`refreshRequired func(provider string) uint64`); `r` keeps the ordinary one.

In `ui/model`: `librarySignedIn` stores `st.authTicket =
refreshRequired(provider)`. On `CatalogSyncFinished`, when
`st.authTicket != 0 && msg.Ticket < st.authTicket` the run is older than the
sign-in: its rows are still reread, but its verdict is not taken (`lastErr`,
`lastSuccess`, `needsAuth` and the sign-in row are left alone, and the list
keeps saying "Syncing your library…"). A `Finished` with
`msg.Ticket >= st.authTicket` is taken as now and clears `authTicket`.

**Guarantees** (each is tested, 14.2; argued again in 16(e)):

- **G1. At most one pending owed run per provider.** `owed` is one bit per
  provider. Any number of requests while a run owns the provider leave it set
  once.
- **G2. No explicit refresh is lost.** A required request, or an `r` made by
  someone who has seen the completion, finds either no run (and starts one)
  or a run that will see `owed` at its release: setting `owed` and the
  release decision are both under `rt.mu`, and `announced` is set before the
  completion can be seen.
- **G3. No unbounded queue.** There is no queue: one bit and two counters.
- **G4. No recursive self-trigger.** `owed` is cleared in the critical
  section that consumes it, and only `rt.sync` sets it. A run never calls
  `rt.sync` for its own provider with an explicit or required kind (its retry
  timer is background). So each follow-up is caused by a request that arrived
  after the previous run started, and N requests cause at most N runs.
- **G5. A stale completion cannot overwrite a newer verdict.** Runs of one
  provider are sequential on one goroutine and the UI handles their messages
  in order, so an older run's `Finished` always precedes the follow-up's.
  Against a sign-in, which is not a run, the ticket decides: a `Finished`
  older than the sign-in's ticket is not a verdict.

Other cases:

- **"Runs" means attempted.** The provider's rate gate and the retry backoff
  of the follow-up's own failure apply as usual.
- **Older completion delivered before the sign-in** (the reviewer's probe):
  its verdict was taken; the sign-in clears it; the required request finds
  `running` and sets `owed`; the follow-up runs at release.
- **Sign-in while an old collection read is pending.** The old run is not
  cancelled. Its remaining reads go out on the new sign-in (they are reads).
  The follow-up still runs: at most one sync more than strictly needed.
- **Quit.** `rt.closed` or the cancelled context: no follow-up; `owed` dies
  with the runtime.
- **Not Local first-run.** Nothing here changes which provider syncs at
  startup or what an empty Local library shows.

**The `TestAcceptanceSpotifyFailedSyncKeepsCacheAndRecovers` timeout
(read-only assessment).** `spotifyRun.refresh` returns when it receives
`Finished` (`acceptance_spotify_ddsonic_test.go:198-208`), and the test's next
`run.refresh()` (`:410`) calls `rt.sync` again. If the syncing goroutine has
not yet reached `ps.running = false` (`main_catalog.go:359`), that request
only counts, no sync starts, the retry timer is an hour, and the helper times
out after 10 s with "the sync did not finish". I reproduced the mechanism
deterministically with an uncommitted overlay test that holds the syncing
goroutine right after `Finished` is delivered: the next refresh is dropped,
3 of 3 (`-count=3`). This shows the window produces exactly that failure; it
does not prove that it caused the reviewer's one observed timeout. The
`announced` rule closes the window; with it the test needs no change.
`main_catalog.go` is unchanged on this branch, so the window is present in
`09b5bd8` too.

## 12. R8-4: a cancelled OAuth flow offers nothing

`performOAuth2PKCEFlows` (`session.go:480-533`), small `// ddsonic:` edits:

1. Return `authentication cancelled: ctx.Err()` at entry, before
   `net.Listen`: no port is bound.
2. Check again after the callback server is started and before
   `notifyAuthURL`: no URL is published (or logged).
3. Check again between `notifyAuthURL` and `browser.Open`: no browser.

Callers: `NewSession` checks `ctx.Err()` before falling through from a failed
stored session to `newInteractiveSession` (`session.go:144-146`);
`newSessionFromStored` checks before `doWebAPIAuth` (`:231`).

A cancellation that lands just after a check is an ordinary race with an
external action: the URL may already have been offered, or the browser
already asked to open, when the cancellation is seen. The function then
closes the listener and returns the cancellation, as it does today when
cancelled while waiting. The checks guarantee only that a flow already
cancelled at a check goes no further; they are not proof that no browser can
ever open after a cancellation.

## 13. Hygiene

Removed, because each is dead in production after this design and each is an
unguarded way to publish or persist token state:

- `webAPITokenSource` (`session.go:365-376`): no production caller today.
- `persistingTokenSource` (`session.go:337-363`): only user would be the
  above.
- `Session.webApiWithBody` (`session.go:664-667`): no production caller
  today.

Their upstream tests are retired or re-pointed (section 14.3), and
`docs/ddsonic/upstream.md` records the removal for the next merge. Nothing
else unrelated is touched.

## 14. Test plan

`go test -race` is necessary and not sufficient: every ordering below is
forced by a barrier, not by timing.

### 14.1 Barriers

- **B1 token endpoint**: the fake token handler signals `entered` and waits
  for `release` (`fakeSpotifyHTTP`, exists). Used with the **real** stored
  builder and the real coordinator, not a stand-in session.
- **B2 API endpoint**: the same for an API request (exists).
- **B3 flight joined**: a package seam `flightJoined func()` (nil in
  production) called by `renew` after a waiter has registered on a flight. A
  test that claims a join waits for B3; "started a goroutine" is not a join.
- **B4a durable snapshot**: a seam called inside a durable section after the
  L5 step, before the write.
- **B4b credential write**: `saveCreds` called through a package variable, so
  a test can hold, fail or count writes and read what was written.
- **B4c close waiting**: a seam called by `close()` and by a replacement just
  before it takes L4, so a test knows a close is parked behind a section.
- **B5 clock**: `tokenCoordinator.now`.
- **B6 rate wait**: the wait in `backRate` through a package variable, so 429
  tables do not sleep, and so a test can act during a wait.
- **B7 sync**: `rt.send` held before and after `Finished` is delivered, a
  seam at the release critical section, and the account's
  `blockAlbums`/`entered` (exists).
- **B8 OAuth**: `authURLObserver` count, `stubBrowser` marker, and the
  callback port being free afterwards.
- **B9 session build**: for the **stored** build, the real
  `newSessionFromStored` with only librespot stood in (`newLibrespotSession`,
  `sessionIdentity`, `initSessionPlayer`, exist) and B1 on its real setup
  refresh; for the browser build, `interactiveTokens` (exists). A builder
  seam that returns an independent synthetic session is not used for the
  stored-grant tests: it would hide the shared grant.
- **B10 stored build joined**: a package seam `storedJoined func()` (nil in
  production) called after a caller has registered on a stored build.
- **B11 stream setup, cooperative**: the player call held open by a stand-in
  that returns when released.
- **B11n stream setup, non-cooperative**: a stand-in that ignores its context
  and returns only when the bundle's access point is closed, modelling the
  SDK wait in 4.3; and the SDK contract test below, which is the real code.
- **B12 teardown**: seams for the access-point close and the player close of
  a bundle, held or counted, and `b.gone`. The access-point close seam can be
  made to block **after** closing `done`, as the SDK does (F2).
- **B13 handoff**: seams in the worker between its CAS and its send, and in
  the caller between its select and its CAS, and before acceptance takes L3.
- **B14 browser**: the `interactiveTokens` stand-in held open, so the
  browser phase lasts as long as a test needs.
- **B15 commit**: a seam just before a build's completion step takes L2.
- **B16 first chunk**: a real HTTP server (loopback) that accepts the first
  chunk request and stalls, before the headers or in the body, until
  released; used with the SDK's real chunked reader, not a stand-in.
- **B17 builder ignores its context**: a builder stand-in that returns only
  when released, whatever its context, and can then report success, refusal
  or an error; and a builder whose own cleanup blocks.
- **B18 decoder**: decoders built by the SDK from small synthetic MP3, FLAC
  and Vorbis fixtures, with an observable release (14.5).

### 14.2 New regressions

Coordinator (`token_coordinator_ddsonic_test.go`):

- `TestConcurrentRenewalsShareOneFlight` {success, invalid_grant, outage}:
  hold B1 on a forced `renew`; start a reader; wait B3; assert one token
  request; release; both get the flight's result; file is the answer's token
  (success) or untouched. Rows 2, 3, 6, 8, 11.
- `TestRenewAfterNewerLeaseSendsNothing`: basis older than `rev`. Rows 3, 11.
- `TestStale401DoesNotRefuseNewerLease` (B2 + B5) and
  `TestStale401WithRealExpiry` (`expires_in=11`, real 1.3 s wait). Row 5.
- `TestRefusalThenInFlightSuccess`: B1 held, `refuse(current)`, release.
  Row 4.
- `TestRefusalIsRemembered`: after T2, `token` makes no request. Row 6.
- `TestFlightOutlivesCancelledCaller`. Row 22.
- `TestClosedCoordinatorCommitsNothing` {success, invalid_grant} (B1), for
  close by `Session.Close` and by interactive publication. Rows 13, 16, 18.
- Durable section (section 8), each asserting the file's actual contents and
  the number of writes:
  - `TestCloseWaitsForWriteInProgress`: rotate, hold B4b, `Close` reaches
    B4c; assert not closed and Close not returned; release: file is the
    rotated credential, then closed. Row 16a (ordering A).
  - `TestAcceptedRotationIsWrittenBeforeClose`: hold B4a (memory step done),
    `Close` reaches B4c, release: the rotation is on disk. Row 16b
    (ordering B).
  - `TestCloseBeforeSettleDropsTheAnswer`: B1 held, `Close`, release: file
    unchanged, no write. Row 16.
  - `TestReplacementDuringWrite` and `TestReplacementAfterAcceptance`: the
    same two gaps with an interactive publication: the new credential is the
    last on disk, and no write of the old one follows it. Rows 12 to 14.
  - `TestJoinedFlightSeesTheWrite`: a caller joined at B3 returns only after
    the write; with `Close` first it gets "superseded". Row 16c.
  - `TestWriteFailureKeepsTokenAndRetries`: B4b fails once: lease usable,
    `dirty`; the next rotation, and separately `Close`, write it. Row 16d.
  - `TestFirstClaimWritesSeed`: a fresh coordinator after a file written by
    anyone: one write, the seed. Row 21a.
  - `TestNewOwnerWithEqualHistoryStillWrites`: two coordinators with the same
    number of prior credential changes and different credentials; claim the
    second: file is the second's, write count one. Row 21b.
  - `TestFailedWriteThenOwnerChange`: old owner `dirty` after a failed write,
    replaced: the old credential is never written; the new one is.
  - `TestTwoRotationsWriteInOrder`. Rows 21, 29.
- `TestHealthySilentRefresh`: expiry by B5, one request, rotation persisted,
  no refusal, no pin ended. `TestEveryPublicationAdvancesRevision`.
- `TestRestartAfterRotation`: rotate, close, build from the file; the setup
  refresh is sent the rotated token.
- `TestPlaybackRebuildKeepsTokens`: no token request during `Reconnect`;
  lease unchanged; identity half written; a pending write ends
  `errSignInChanged`. Rows 25, 26.

Request driver:

- `TestRequestKeepsRenewalBudgetAcross429`, plus a table: alternating
  401/429 repeated; cancellation during the wait; a sign-in during the wait
  (read moves on, write ends); a write (never resent after 401, resent after
  429 with the pin rechecked); `webAPIOnce` not waiting. Rows 19, 20.
- `TestExpiryDuringEveryRateWait` (B5 + B6): the token expires in each of
  seven waits: eight API attempts, eight automatic flights, zero recovery
  renewals, `authRound` 0, success. The same for a write.
- `TestMixedExpiry401AndSignIn`: expiry, then a 401 (one recovery renewal),
  then an interactive sign-in during a wait, then a 401 on the new sign-in
  (one more), then a third 401 there (refused). Counts asserted against
  16(d).
- `TestLastBudget`: the fourth auth edge fails "kept changing" without
  sending; the eighth 429 fails without waiting; each limit is checked before
  the edge.
- `TestNoWaitRequestNeverLoopsOnRate`: `webAPIOnce` with expiry and a 429:
  one attempt, at most one flight.
- `TestIndependentRequestsHaveIndependentBudgets`.

Sync and sign-in (root and `ui/model`):

- `TestSignInSyncIsNotLostBehindOlderSyncCompletion` (B7): the reviewer's
  production-shaped probe (runtime, engine, SQLite, library model), kept as
  written.
- Both sides of each boundary, by B7: a request **before** `announced` is
  set (coalesced for `r`, owed for a sign-in); **after** it is set and before
  the send; after the send and before the release; **after** the release (a
  new run starts).
- `TestSignInDuringPendingCollectionRead` (account barrier): one follow-up.
- `TestStaleSyncFailureAfterSignInKeepsSignedInState`: the older run's
  `Finished` with `ErrNeedsAuth` delivered after the sign-in.
- `TestRequiredSyncsCoalesce`; `TestRequestDuringFollowUp` (a request made
  while the follow-up runs yields exactly one more);
  `TestNoFollowUpAfterQuit`.
- `TestRefreshAfterAnnouncedFinishIsNotDropped` (the :410 window).

- `TestExplicitRefreshesCoalesceToOneFollowUp` (many `r` after the
  announcement: one run), `TestFollowUpDoesNotTriggerItself` (count runs
  after one owed request: exactly two), `TestBackgroundRetryIsNotOwed`,
  `TestQuietProviderRuleUnchanged`.

Session construction (4.2), with the real stored builder and B1 on its real
setup refresh:

- `TestStartupThenAuthenticateShareStoredBuild` and
  `TestAuthenticateThenStartupShareStoredBuild`: hold B1; the other caller
  reaches B10; exactly one token request; then, per outcome:
  - *rotated answer*: one session, the sign-in ends with no browser stand-in
    called, the file holds the rotated token, one write, the epoch moved
    once;
  - *`invalid_grant`*: `storedRefused`; startup callers get `ErrNeedsAuth`;
    the browser stand-in is called exactly once; the file is untouched until
    the browser sign-in is installed;
  - *outage*: a session without a token; `Authenticate` goes on to the
    browser rebuild; one stored refresh in total;
  - *sign-in cancelled while waiting*: it returns the cancellation; the build
    commits; no browser;
  - *`Close` while the build is held*: the build is closed unclaimed, no
    write, waiters released with `errProviderClosed`.
- `TestBrowserPhaseNeverOverlapsStoredBuild`: the stored refresh fails with a
  timeout (not a refusal) while a sign-in waits; the browser stand-in is held
  open (B14); three `ensureSession` callers then arrive: each returns at once
  with `ErrNeedsAuth`, the token endpoint has still seen exactly one request,
  `storedRefused` is unset, no goroutine is parked. Then, per ending:
  the browser succeeds (callers after it see the session; no stored refresh);
  it fails; it is cancelled (the next `ensureSession` starts one new stored
  build, and only then). No deadlock in any ending.
- `TestAuthenticateFirstCancelledAlone`: `Authenticate` starts the stored
  build, B1 held, no other caller; cancel the sign-in: it returns the
  cancellation while the build is still held; `signingIn` is nil and the
  phase is `storedBuilding`; release: the build installs, the epoch has
  **not** moved, the file is written once.
- `TestStartupJoinsAfterCancelledAuthenticate`: the same, with an
  `ensureSession` caller arriving after the cancellation: it joins (B10) and
  gets the session; one token request.
- `TestStartupFirstCancelledAuthenticate`: the control with startup as the
  starter.
- `TestCancelRacesCommit` (B15): the sign-in's context ends while the commit
  is held just before L2; both orders of S3 and the commit; the result is
  "success and epoch moved" or "cancelled and epoch not moved", never a
  mixture.
- `TestSecondAuthenticateJoinsFirst`.
- Build deadline (B17), each asserting the call's error, the phase, the token
  request count, the file's contents and write count, and that no application
  lock is waiting:
  - `TestStoredBuildCompletesOnItsDeadline`: the builder ignores its context;
    at the deadline every waiter gets the deadline error, the phase is
    `idle`, `storedRefused` is unset; the builder is still running.
  - `TestRetryAfterExpiredBuild`: a second `ensureSession` then starts a new
    call and can succeed while the first worker is still held.
  - `TestLateSuccessIsNotInstalled`: the held builder is released reporting
    success after the deadline: not installed, not claimed, no write; its
    session is retired; with a retry already installed, that session stands.
  - `TestResultAndDeadlineTogether` (B15): both ready; the completion step's
    context check decides; never a late install.
  - `TestBlockedBuilderCleanupDoesNotHoldTheCall`: the builder fails and its
    cleanup blocks: the call still completes at its deadline.
  - `TestLateWorkerSendsNoRefresh`: a worker released after its call
    completed makes no token request.
  - `TestWaiterCancelDoesNotEndBuild`: the control: only the call's own
    context ends a build.
  - `TestSignInEndsOnItsTimeoutWithHungBrowserBuild` and the same for a
    silent rebuild.
- `TestConcurrentStartupSharesOneBuild`: three `ensureSession` callers, one
  build.
- `TestStoredBuildHoldsNoProviderLock`: B1 held; a Web API read fails at
  once, `Close` returns, each without waiting for `release`.
- `TestStartupRefusalIsRemembered` and `TestStartupOutageIsNot`.
- `TestPublishIntoClosedSession` and `TestSilentRebuildAfterSignIn`: the
  revalidation at the commit lock discards the build.
- A pin begun before a stored install made by `ensureSession` survives it;
  one begun before a stored install made during a sign-in does not.

Playback bundle (4.3):

- **SDK contract tests, real SDK code** (the arrangement is in 14.5):
  the key-pump contract (F1 and its release by `done`) and the
  reconnect/`Close` boundary (F2).
- `TestCallerNeverCallsTheSDK`: the access-point close seam blocks forever
  after closing `done` (B12, the F2 shape): `publish`, `Session.Close` and
  `Provider.Close` each return; a token read, a stream open on the new bundle
  and `needsSignIn` return; the workers were released; the player was closed;
  `b.gone` is still open. Releasing the seam closes it.
- `TestRetireUnblocksStalledSetup` (B11n): a worker blocked as the SDK
  blocks; retire; the worker returns when `done` closes; then, and only then,
  the player is closed (order asserted).
- `TestConcurrentAccessPointClose`: the SDK-side close and the retirement
  race; one close, no panic, the tail completes.
- `TestSetupTimeoutKeepsReference`: the caller's deadline passes while the
  worker is held (B11): the caller returns; the reference is still held and
  the player is not closed until the worker returns.
- Handoff (B13), each asserting who disposed and that it happened once:
  - `TestWorkerWinsThenCancel`: the worker's CAS has succeeded and its send
    is held; the caller's context ends: the caller's CAS fails, it receives
    the stream, disposes of it, returns the cancellation.
  - `TestWorkerWinsThenDetach`: the same gap, then a publication: validation
    fails; the caller disposes; "session changed".
  - `TestCallerAbandonsFirst`: the worker then disposes.
  - `TestBothReady`: result and cancellation both ready before the select;
    run until both branches have been taken; one owner each time.
  - Ended caller at acceptance (B13's seam before L3, and L3 held by a
    test): `TestEndedContextSuccessIsNotAccepted` and
    `TestEndedContextKeyErrorIsNotRecovered`, each in three arrangements: the
    result branch forced with the context already ended; the context ended
    between receiving the result and obtaining L3; L3 held while the context
    ends, then released. Asserted: no stream returned, the handle called
    once, the decoder closed once (success case), the context outcome
    returned, zero silent reconnects and zero refusals. Controls with a live
    context: `TestLiveContextSuccessIsAccepted`,
    `TestLiveContextKeyErrorIsRecovered`, and
    `TestCancelAfterAcceptanceLeavesStream`.
  - `TestAcceptBeforeDetach` and `TestDetachBeforeAccept`: the O3 boundary on
    both sides; the accepted stream still reads after its bundle's tail has
    finished and after a sign-in, and its handle has not been called.
- Transport (B16, the SDK's real chunked reader through our real client and
  stream transport), each asserting that the stalled request ends, the worker
  exits, its reference is released and the tail proceeds:
  - `TestSetupTimeoutEndsFirstChunkRequest` {stall before headers, stall in
    body};
  - `TestRetirementEndsFirstChunkRequest`;
  - `TestErrorAndPanicCallTheHandle`: an error and a panic before any stream
    exists;
  - `TestLateSuccessAfterAbandonIsDisposed`;
  - `TestEstablishedStreamSurvivesRetirement`: the control: after acceptance
    the same server keeps serving chunks through a retirement.
- Stale errors, deterministic:
  - `TestRetiredBundleErrorIsNotRecovered`: the worker has published a
    `KeyProviderError` for bundle b (B13); b is detached; the caller is made
    to take the result branch: it returns "session changed"; the provider
    performs zero silent reconnects and records zero refusals on the new
    bundle; no pin ended.
  - `TestLiveBundleKeyErrorIsRecovered`: the control: the same error from the
    current bundle goes through today's reconnect and, failing that,
    `ErrNeedsAuth`.
  - `TestReconnectForOldGenerationIsNoOp`.
- Decoder release (B18), asserting **release**, not merely that a method ran:
  - `TestDisposedMP3ReleasesItsHandle`: a never-accepted MP3 result is
    disposed; the decoder's `cgo.Handle` is gone (its lookup panics) and its
    native pointers are nil; the control without disposal shows the handle
    still resolving after two GCs.
  - `TestDisposedFLACAndVorbisAreClosedOnce`: `Close` returns; a second
    disposal attempt is impossible by ownership and is asserted not to
    happen (a FLAC double close is not survivable).
  - `TestAcceptedStreamIsNotClosedByThisPath`.
- `TestRetireIsOnce`: `Close` during a publication's retirement, `Close`
  twice, publication into a closed session: one retirement, one player
  close.
- `TestOpenAfterDetachUsesNewBundle` and `TestOpenAfterCloseFails`.

OAuth: `TestCancelledFlowOffersNothing` (entry; before URL; between URL and
browser) and `TestCancelledBetweenStoredAndInteractive`.

### 14.3 Existing tests

- **Adapted mechanically** (about 45 sites): every `Session{tokenSource: ...}`
  literal in `session_auth_ddsonic_test.go` and
  `session_lifecycle_ddsonic_test.go` becomes a helper that gives the session
  a coordinator with a seed lease; `expiredStagedSession`, `signedInSession`,
  `brokenSession` likewise. Assertions are kept.
- **Re-expressed**: `TestObsoleteTokenSourceDoesNotRewriteCredential` (as
  `TestClosedCoordinatorCommitsNothing`), the token-revision refusal tests
  (as lease tests), `TestBackgroundAlbumReadRecoversFrom401` (unchanged
  assertions, driver underneath).
- **Retired with their subject**: `TestPersistingTokenSourcePersistsRotationOnce`,
  `TestPersistingTokenSourceDoesNotBlockOnSaveFailure`,
  `TestWebAPITokenSourcePersistsRotatedRefreshToken` (`session_test.go`),
  `TestWebAPITokenRefreshIsBounded` (`token_timeout_test.go`). Each property
  is re-asserted on the coordinator (`TestFlushWritesLatestOnce`,
  `TestFlushFailureKeepsTokenAndRetries`, a bounded-flight test).
- **Re-pointed**: tests calling `Session.webApiWithBody` (seven sites) call
  the provider's request path.
- **Adapted**: `TestAwaitSpotifyStreamTimeoutCancelsTransportAndReleasesReadLock`
  (`session_test.go:31`): the waiter returns and holds no lock; the reference
  stays with the worker.
- **Mapped, because their choreography can no longer be performed:**
  `TestStartupBuildDoesNotReplaceNewerSignIn`
  (`session_lifecycle_ddsonic_test.go:843`) and the reviewer's overlays
  `TestReviewerStartupRestoreCannotOverwriteNewSignIn` and
  `TestReviewerStartupRealBuilderCannotOverwriteNewSignIn` hold a startup
  build, complete a sign-in, then release the build. A sign-in now waits for
  that build, so run unchanged they would deadlock, like the forced-refresh
  probe of 16(a). The two properties they protect are kept and tested where
  an old build can still be late:
  - *a stale build is never published over a newer sign-in:* a stored build
    held (B15) while the provider is closed, and a silent rebuild held while
    an interactive publication completes
    (`TestSilentRebuildAfterSignIn`): the late build is closed, unpublished;
  - *a stale build never writes the credential:* the same two tests assert
    the file's contents and write count, and
    `TestStartupThenAuthenticateShareStoredBuild` asserts that the one build
    that does win writes once.
  The reviewer is asked to approve this mapping as 16(a)'s was.
- **Kept as they are, not replaced by seam tests:** the real-expiry test,
  SPOT-004 (`TestAcceptanceSpotifyFirstRunSignIn`), the UI sign-in tests and
  the root-package runtime tests. Seam tests are in addition to these.
- Unchanged: pin, action, IPC, daemon, overlay, UI sign-in and SPOT-004
  tests, and the earlier reviewer overlays **other than** the three
  choreographies mapped above and the forced-refresh probe of 16(a), must
  stay green without edits to their assertions.

### 14.4 Reviewer round-8 probes

| Probe | Regression | Note |
|---|---|---|
| `TestReviewerReadKeepsItsRenewalBudgetThrough429` | `TestRequestKeepsRenewalBudgetAcross429` | Same sequence and assertion. |
| `TestReviewerStale401DoesNotRefuseAutomaticRefreshWithinOneSource` | `TestStale401DoesNotRefuseNewerLease` | The probe expires the token by editing the cached `*oauth2.Token`; that object no longer exists, so the clock (B5) is advanced instead. |
| `TestReviewerStale401WithActualOAuthExpiry` | `TestStale401WithRealExpiry` | Same real wait; no seam. |
| `TestReviewerForcedAnswerCannotOutliveNewerAutomaticRefresh` | `TestConcurrentRenewalsShareOneFlight`, both join orders (16(a)) | **The probe's ordering cannot be constructed** and, run as written, would block: its reader joins the held flight, and the probe releases the flight only after the reader returns. The regression asserts the stronger fact (one request, one answer). For `invalid_grant` the sign-in *is* refused afterwards, correctly: no newer accepted token exists. The probe's property is I2/I3 and is exercised where it can occur, in rows 5 and 13. The reviewer should confirm this reading. |
| `TestReviewerActuallyAdoptedSourceStillRotates` | `TestSignedInCoordinatorRotatesAfterPublication` | Same flow through `Authenticate`. |
| `TestReviewerCancelledFlowDoesNotOfferBrowserSignIn` | `TestCancelledFlowOffersNothing` | As written. |
| `TestReviewerSignInSyncIsNotLostBehindOlderSyncCompletion` | same name without "Reviewer" | As written. |

### 14.5 The SDK contract tests

These are internal-contract tests of a dependency at one pinned version, not
a guarantee of its public API. They do not replace the worker and retirement
tests above or live librespot acceptance.

**Files**, under `external/spotify/testdata/sdkcontract/`, with a `.txt`
suffix so that nothing in this repository compiles them:

- `ap_helper.go.txt`: package `ap`, **not** a test file, present only in the
  overlay. It builds an `Accesspoint` over a supplied `net.Conn` and address
  function by setting the package's own unexported fields, as the reviewer's
  helper does. It is needed because a test in package `audio` cannot set
  `ap`'s unexported `conn`/`encConn`. No `unsafe`, no reflection, and no
  helper in production code or in the real dependency.
- `ap_contract_test.go.txt` (package `ap`),
  `audio_contract_test.go.txt` (package `audio`) and
  `mp3_contract_test.go.txt` (package `mp3`).
- `tone.mp3.bin`, `tone.flac.bin`, `tone.ogg.bin`: a second or less of a
  synthetic tone each, a few kilobytes, generated once with
  `ffmpeg -f lavfi` (the command is recorded beside them), copied into the
  overlay by the runner. They are also the B18 fixtures.

**Tests**, all named `TestDDSonicContract...`:

| Test | Asserts | If a future SDK changes it |
|---|---|---|
| `...KeyRequestIgnoresContext` | pump send blocked; a second `Request` is still blocked after its context expired | The SDK improved (F1 gone). A reviewed expectation update, not a product regression. |
| `...DoneReleasesKeyRequest` | that blocked `Request` returns when `Close` is called on another goroutine | **The design's assumption is broken. Stop.** |
| `...RequestAfterCloseFails` | fails at once | Stop. |
| `...DoneClosesBeforeCloseReturns` | in the actual `recvLoop` reconnect, with the handshake held: after `Close` is called, `Done()` is closed promptly | Stop. |
| `...CloseWaitsForReconnect` | in that same state `Close` has not returned until the reconnect is released | The SDK improved (F2 gone). A reviewed expectation update; the design stays correct. |
| `...CloseIsOnce` | concurrent `Close` calls | Stop. |
| `...FirstChunkIgnoresSetupContext` (audio) | the chunked reader's first request, through a client whose transport carries an outer context, is still in flight after an unrelated setup context ended | The SDK improved. A reviewed expectation update; the handle stays correct. |
| `...ClientContextEndsFirstChunk` (audio) | cancelling that outer context ends the request and the constructor returns an error | **Stop:** the open handle would no longer release a stalled open. |
| `...IdentityGetterWaitsForReconnect` (ap) | in the reconnect state `Username` is still blocked after the setup deadline and after `Done()` closed | The SDK improved. A reviewed expectation update; the build deadline stays correct. |
| `...MP3HandleSurvivesDropAndGC` (mp3) | after dropping the decoder and two GCs its `cgo.Handle` still resolves | The SDK improved (a finalizer, say). A reviewed expectation update. |
| `...MP3CloseReleasesHandle` (mp3) | `Close` deletes the handle and frees the native state | **Stop.** |

The fixtures are committed with the commands that generated them; `ffmpeg` is
not run by `make check`. Native release is exercised on Linux, where the
tests run; **on macOS it is unverified.**

These eleven tests establish only the properties they name: the key wait,
the close/reconnect boundary, the first-chunk transport, one getter, and the
MP3 handle. They are not evidence that the SDK cleans up as a whole, that
every goroutine of a closed session ends, or that FLAC and Vorbis `Close`
are safe on every platform (those two are exercised by B18 in this
repository's own tests, on the platform the tests run on).

**Runner**, a Go test in this module (`TestSDKContract`, package
`external/spotify/sdkcontract`) behind the build tag `sdkcontract`.

**It is a separate, mandatory gate, not part of the routine suite** (owner
decision, 2026-10-13; revision 9 had it under `go test ./...` and
`make check`). It has its own target, `make sdk-contract`; the tag keeps it
out of `go test ./...`, `make test` and `make check`; `make ci` runs it. It
is required, and may not be skipped, for: the full validation of a change to
the Spotify session code, every release candidate's validation, and any
change that touches the boundary with go-librespot or the version of it that
`go.mod` selects. CI runs it in the job that has the pinned toolchain; the
macOS job takes its Go version from `go.mod` and does not run it, so the
native release checks are unverified on macOS until macOS acceptance. On
macOS the runner adds Homebrew's two prefixes to the child's `PATH` and
`PKG_CONFIG_PATH`, where pkg-config and the codec libraries live there; that
too is unverified until then.

Its steps:

0. **Environment first, pinned toolchain always.** The child's whole
   environment (private `HOME` and `TMPDIR`, caches, proxy, `GOENV=off`,
   `GOTOOLCHAIN=local`, `GOFLAGS`, `CGO_ENABLED=1`, and a `PATH` that holds
   only the system directories where the C compiler lives, which the SDK's
   cgo packages and the race detector need) is set before **any** tool is
   invoked, `go list` and `go env` included. The Go executable is not looked up through `PATH` under
   that environment: a version-manager shim can select a different toolchain
   once `HOME` is private (the reviewer's first run got another Go build that
   way). The runner uses the executable that built the running test
   (`runtime.GOROOT()`'s `bin/go`), then requires `go env GOVERSION` to equal
   both `runtime.Version()` and the version pinned for this repository
   (`mise.toml`, 1.27.2). Any mismatch fails.
1. Refuses to run recursively: it sets, and checks, an environment marker.
2. **Private caches and the offline source.** The child gets private
   `GOCACHE`, `GOPATH` and `GOMODCACHE`, all in the test's temporary
   directory and **empty at the start of every run**, so nothing the user
   owns is written (`-mod=readonly` protects only `go.mod`, not the module
   cache's lock and metadata files). Its only source of modules is the real
   module cache's download directory, read-only, as
   `GOPROXY=file://<real module cache>/cache/download`, with `GOSUMDB=off`
   and `GOFLAGS="-mod=readonly -modcacherw"`; checksums are still verified
   against this repository's `go.sum`. The real module cache is never used as
   `GOMODCACHE`. `-modcacherw` applies to the **private** cache only and is
   needed: Go otherwise makes extracted module directories read-only, and
   the test's temporary directory then cannot be removed (observed).
3. **Select and validate the version** (metadata only): `go list -m -json
   github.com/devgianlu/go-librespot` in this module, with that environment.
   `Version` must equal the pin written in the runner (`v0.9.1`). **No
   directory is required of this step:** `go list -m` resolves metadata and
   does not extract a module's source into a cold cache (the reviewer
   reproduced exit 0 with no `Dir`).
4. **Extract the pinned module, offline:** `go mod download -json
   github.com/devgianlu/go-librespot` with the same environment. It must
   exit 0 and report the pinned version and a `Dir`. A missing archive or a
   checksum that does not match `go.sum` makes this step fail, and the
   runner fails with the tool's output. **The exit is read first:** a
   failing tool may report its error as structured JSON on stdout (an
   `Error` field), or only on stderr with a non-zero exit (Go's "checksum
   mismatch ... SECURITY ERROR" text), or both. So the runner looks at the
   exit status before it parses anything as a result, never takes stdout of
   a failed tool for a success, and keeps both streams in the failure it
   reports. This holds for every tool call, not only this one
   (`TestToolFailureIsReadBeforeItsOutput` covers the stderr-only form).
5. **Only now require the directory:** repeat the `go list -m -json` of step
   3 and require `Dir` to exist, to lie inside the private module cache, and
   to equal step 4's. This directory is read from here on and is **never an
   overlay target**: Go 1.27.2 refuses any overlay that replaces a file
   beneath the active `GOMODCACHE`, a private one included
   (`cmd/go/internal/work/init.go:69-70`: "Files beneath GOMODCACHE ... must
   not be replaced"), and the runner does not work around that guard.
6. **Stage the verified source outside the module cache.** Copy the whole
   module directory of step 5 to `<tmp>/sdk`, in the test's temporary
   directory and outside `GOMODCACHE`, `GOPATH` and `GOCACHE`. Then compare
   the two trees: the same set of relative paths, and every file byte for
   byte equal (file modes are not part of the comparison). A difference
   fails the run. The extracted copy in the private
   cache and the real module cache are not modified, now or later.
7. **A scratch module that keeps the repository's graph.** Create
   `<tmp>/mod` with byte copies of this repository's `go.mod` and `go.sum`,
   and append to that `go.mod` exactly one line:
   `replace github.com/devgianlu/go-librespot => <tmp>/sdk`. Nothing else
   differs, and the repository's own files are not touched. With
   `<tmp>/mod` as the working directory and the same environment, run
   `go list -m -json all` there and in the repository: every selected module
   version must be identical, the only permitted difference being the
   `Replace` of the SDK; and the scratch `go.sum` must be byte-identical to
   the repository's after every command of the run.
8. **Overlay and run.** Copy the contract files and fixtures into
   `<tmp>/contract`, and write `<tmp>/overlay.json` mapping them to paths
   under **`<tmp>/sdk`** (`<tmp>/sdk/ap/...`, `<tmp>/sdk/audio/...`,
   `<tmp>/sdk/mp3/...`); the staged copy itself stays byte-identical, the
   files exist only in the overlay. Then, with working directory
   `<tmp>/mod`, run one child: `go test -overlay=<tmp>/overlay.json -race
   -count=1 -json -run '^TestDDSonicContract'
   github.com/devgianlu/go-librespot/ap
   github.com/devgianlu/go-librespot/audio
   github.com/devgianlu/go-librespot/mp3`. Those import paths resolve to the
   staged copy through the replacement. The modules they depend on are
   fetched into the private cache from the same offline source. The anchored
   pattern selects only these tests, so no upstream suite and no network test
   runs.

Where each command runs: steps 0, 3, 4 and 5 in the repository directory
(read-only use of its `go.mod`/`go.sum`); step 7's second `go list` and step
8's `go test` in `<tmp>/mod`. Every one of them has the environment of steps
0 and 2. No command runs with the staged copy or the module cache as its
working directory.

9. Bounds every tool invocation with a deadline and kills it on expiry;
   captures its output and reports it on any failure.
10. Parses the JSON events and requires a `pass` for every name in its own
   list of expected tests. A missing name, a `skip`, or "no tests to run" is
   a failure: the tests must be shown to have run.
11. **Fails visibly, never skips,** when the toolchain is missing or not the
   pinned one, the race detector or cgo is unavailable, a private cache
   cannot be created, the module or one
   of its dependencies is not in the real module cache's download directory,
   the native libraries the SDK's decoders need are missing, or the version
   differs from the pin.

**What earlier approval did and did not establish.** The overlay approach was
approved in principle on the strength of probes that overlaid a copy of the
SDK **outside** any module cache. Revisions 3 to 7 of this document
nevertheless specified the module cache directory as the overlay target; that
mapping was never shown to work, and it cannot. The staging above is the
arrangement the probes actually used.

**Tests of the runner itself** (it is code, and a warm cache would hide its
ordering):

- `TestSDKContractFromEmptyCache`: the whole sequence, for real: empty
  private cache, metadata, verified extraction, verified external staging,
  the scratch module, and the **actual overlay run**, ending in all eleven
  named contract passes. The normal run is this case by construction (step
  2); the test additionally asserts that the private module cache was empty
  before step 3, that step 4 extracted the pinned module into it, that the
  staged copy lies outside the module cache and compared equal, that the
  module graphs matched, and that the scratch `go.sum` is unchanged. A test
  that stopped at metadata or at the directory check would not have met the
  module-cache guard, so it does not count.
- `TestSDKContractStagedCopyMismatchFails`: one byte of the staged copy is
  changed before the comparison: the runner fails at step 6 and runs no
  contract test.
- `TestSDKContractGraphMismatchFails`: the scratch `go.mod` is given a
  second difference: the runner fails at step 7.
- `TestSDKContractMissingArchiveFails`: the offline source is a copy of the
  download directory without the pinned module's archive: the runner fails
  at step 4, visibly, and runs no contract test.
- `TestSDKContractChecksumMismatchFails`: the offline source holds a valid
  archive whose source bytes were changed, so it does not match `go.sum`:
  the same. The changed archive must be a well-formed **module** zip (file
  entries only, under the `module@version/` prefix); one that also contains
  directory entries is rejected earlier, for a different reason ("has
  unexpected file"), which fails visibly too but is not this case. The test
  asserts the checksum-mismatch text.
- `TestSDKContractWrongToolchainFails`: step 0 with a mismatching version.

**Executed once, 2026-10-12, outside the repository** (a shell script that
follows steps 0 to 8 literally, in the author's scratch directory; Go 1.27.2
by absolute path; `unshare -rn` around every tool call, so no network;
private empty `HOME`, `TMPDIR`, `GOCACHE`, `GOPATH`, `GOMODCACHE`). In place
of the eleven contract tests, which do not exist yet, the overlay carried
four smoke tests: two in `ap` (one using an overlay-only non-test helper),
one in `audio` that calls that helper across packages, and one in `mp3`
(cgo).

- The full sequence passed, with a read-only copy of the download directory
  as the offline source and again with the real download directory itself
  (read only): version `v0.9.1` with no `Dir` at step 3; `Dir` inside the
  private cache after step 4; 257 files staged, `diff -r` clean; 187 modules
  in both graphs, no version difference, one new replacement; `go.sum`
  byte-identical before and after; all four named tests passed under
  `-race`; the staged copy still byte-identical afterwards.
- Missing archive: step 4 exits 1 ("no such file or directory"); nothing
  staged, no test run. Changed archive (well-formed): step 4 exits 1 with
  "checksum mismatch"; nothing staged, no test run. Staged copy altered by
  one line: step 6 fails; no test run.
- No file in the real module cache or the repository was created or changed.
- **Cost, from empty caches every time:** about 10.5 s wall time in total on
  this machine (32 cores, scratch on tmpfs), of which the `go test` step
  (downloading, compiling the three packages and their dependencies with the
  race detector, running) was about 10.3 s and every other step together
  about 0.2 s. It left a build cache of about 150 MB and a private module
  cache of about 46 MB, both removed with the temporary directory. The real
  contract tests will add their own running time to this; a slower machine
  or disk will take longer. Not executed: the wrong-toolchain case, and the
  eleven contract tests themselves.

**Upgrade procedure**, recorded in `docs/ddsonic/upstream.md`: a go-librespot
bump changes the pin in the runner in the same change; if the helper no
longer compiles it is ported; a "reviewed expectation update" row is changed
only in a change the reviewer sees; a "Stop" row going red blocks the bump
until 4.3 has been re-examined.

## 15. Size, risks, order, decisions

### 15.1 Estimate

Nothing here can be confirmed before the code exists, and no risk acceptance
should be read from a line count. These are my estimates.

Production: about 1100 to 1400 changed lines in 11 files. New
`token_coordinator_ddsonic.go` with the durable section (about 300);
`session_auth_ddsonic.go` (about 350 removed, 220 added); `provider.go` and
`catalog_sync.go` (request driver, build phase, build controller and worker,
the sign-in's phases, generation-tied reconnect, about 290); `session.go`
(builders, view, playback bundle with tail and closer, the open protocol with
its handle, origin validation and disposal, playback rebuild, removals, R8-4,
about 380, of which bundle and open protocol are about 250);
`session_errors.go`, `lyrics.go` (small); `main_catalog.go` (about 50);
`ui/model/library_nav.go`, `library_signin.go`, `state.go` (about 35).

Tests: about 1750 added, 350 removed or rewritten. SDK contract tests: about
400 lines of test data, three small audio fixtures, and a runner of about
260 (it now stages the source and builds a scratch module).

The figure has grown at every revision (300 to 450 after round 7; 650 to 800;
750 to 900; 900 to 1100; 1000 to 1250; now 1100 to 1400). Each increase came
from a boundary the earlier text left unspecified, lately all of them at the
edge between this code and the SDK.

### 15.2 Risks

- (a) **Liveness.** All callers needing a new token wait for one flight (up to
  the token client's bound). Today they wait on `oauth2`'s internal mutex and
  cannot be cancelled; now they can.
- (b) **Rewriting the most-reviewed code**, and about 45 test sites change
  shape. Mitigation: the steps in 15.5 keep the suite green at each commit.
- (c) **Playback-only rebuild** is a behaviour change inside `Reconnect`
  (no Web API token refresh there). Real librespot behaviour is not covered by
  tests; live testing remains required (16(b)).
- (d) **Playback bundle (4.3)** changes upstream's locking around the player
  and the order of teardown (the access point's close is started first, on its
  own goroutine; the player is closed after its workers have returned). Its
  key assumption about the SDK is pinned by the SDK contract test; everything
  else about real librespot (recovery, long streams across a sign-in,
  teardown) stays in the live-acceptance set.
- (d2) **The SDK contract test** compiles tests into a staged copy of a
  dependency with `-overlay`, in a scratch module, and runs the toolchain
  from a test. It is unusual, needs the pinned `go` wherever `make check`
  runs, copies the SDK's source tree on every run, and builds it with the
  race detector from an empty build cache, which costs time on each run
  (about 10 s measured once with smoke tests; see 14.5).
  The step order was wrong twice before this revision; it is now covered by
  a regression that runs the whole sequence from an empty cache.
- (d3) **A stream being opened when its session is replaced now fails** at
  once instead of delaying the replacement (4.3; owner decision O3).
- (d4) **Retention after retirement has no bound** (4.3; accepted by the
  owner as a documented 1.0 fault-time limitation, O4). Per retired object stuck in the SDK: its cleanup goroutines, the
  SDK's own goroutines, its objects and sockets, including a possibly still
  authenticated connection. One set per retirement or late build that
  happens while the SDK is in that state; silent playback rebuilds count;
  nothing caps the total.
- (d5) **A startup caller during a browser sign-in is turned away** with
  "sign-in in progress" instead of trying the stored credential (4.2;
  approved by the reviewer as routine). It applies to callers with no
  session: a provider that has a session answers from it before the phase is
  looked at. Cached browsing is unaffected, and the sync that the sign-in
  requires follows it (section 11).
- (d6) **Existing defect, not addressed here, and not accepted or waived by
  this document:** an established stream's teardown does not close its
  decoder, so each MP3 track played leaves a `cgo.Handle` rooted (4.3). This
  design closes decoders only for streams that were never accepted. The owner
  has placed the established-track case outside this slice, to be tracked for
  investigation after 1.0; its release disposition is a separate question
  that the dispatcher is taking to the owner, and nothing here settles it.
- (d7) **Closing decoders of never-accepted streams** is new behaviour on
  native code. It is argued from exclusive ownership and tested for MP3
  release and for FLAC and Vorbis on Linux; it is unverified on macOS.
- (d8) **A successful build that arrives after its deadline is thrown away**
  (4.2), including a rotated refresh token it may carry. Decided by the owner
  (15.4(7)): it is the 15.4(1) class, a result that never reached acceptance.
- (e) **Upstream merge cost**: three upstream symbols and four upstream tests
  removed, `NewStream`/`Close`/`reconnect` edited; recorded in
  `docs/ddsonic/upstream.md`.

### 15.3 Interpretations for the reviewer to confirm

Approved in the reviews of revisions 2 and 3 and unchanged: the probe mapping
(16(a)), playback-only reconnect (16(b)), whole-session refusal on `Session`
(16(c)), the owed-sync guarantees (16(e)), the durable section (section 8),
the per-coordinator dirty flag, the request machine and its three counts
(16(d)), the SDK contract test in principle.

Approved in the review of revision 5: the open handle, decoder disposal for
never-accepted streams, origin validation and the generation-tied reconnect,
the build controller and worker (for all three builds), the extended SDK
contract tests and synthetic committed fixtures.

Open for confirmation in revision 6:

1. Step 5's check (a): an open whose caller's context has ended is rejected
   under L3 for both result kinds; acceptance linearizes only with a live
   context and an attached bundle (4.3).
2. The wording about "immediately" and the local lock wait (4.3, 4.4).
3. The runner's pinned-toolchain check (14.5).

This document contains no exception to the lock rule.

### 15.4 Owner decisions

Items 1 to 7 were decided on 2026-10-12. None is pending in this document.

1. **Quit-time durability of a rotated refresh token: accepted as a
   documented 1.0 limitation.** If Spotify rotates a refresh token and ddsonic
   quits, or the work is cancelled, before that result reaches the
   coordinator's accepted commit point (a settle's durable section on an open
   coordinator; `claim` for a build), the unaccepted result is not persisted
   and the previously committed credential is left untouched. The next launch
   may require interactive sign-in if the old refresh token is no longer
   usable. This is a narrow reliability limitation, not a credential-integrity
   defect: the file always holds a credential that was accepted as current,
   never a mixed or unaccepted one. The two rules that produce it are not
   weakened: a closed or cancelled session cannot commit credentials (I2, I6,
   I8), and a credential is persisted only after its result is accepted as
   current (I7, I9). The limitation is to be recorded in `docs/ddsonic/` and
   the changelog when the design is implemented. Matrix row 16 is this case.
2. **R8-3 owed-sync scope: the broader rule is approved** (section 11).
3. **O1, approved.** When the stored librespot session is usable but Web API
   recovery still needs interactive authentication, the full existing
   interactive flow is used (for a custom client ID: Web API consent, then
   playback consent, in the same tab). The point at which a browser is
   required is unchanged. A new device identity and a rebuilt playback
   session are acceptable. No separate partial-authentication flow is kept
   to preserve the old distinction (4.2).
4. **O2, approved.** A user's sign-in may wait for a stored-credential
   startup build in flight. Only one stored-grant build owns that credential
   at a time; the wait is cancellable; if the stored build succeeds no
   browser flow starts; if it fails in a way that needs interactive
   authentication the sign-in goes on to the browser; a competing refresh of
   the same stored refresh token is never started. The setup timeout bounds
   that build's network setup only (4.2).
5. **O3, approved.** A track open still in progress when its session is
   replaced or closed fails immediately; replacement and close do not wait
   for it; an established stream keeps its own lifetime. The boundary is
   exact: acceptance under `Session.mu` before the bundle's detachment (4.3,
   step 5).

6. **O4, approved.** For 1.0, "closed immediately after the swap" means
   "retired immediately from ddsonic application use". Immediately after
   retirement no new ddsonic work may use the old session; callers do not
   wait for its physical SDK teardown; established streams may keep their
   independent lifetime; the old SDK session or connection may stay alive
   while go-librespot is reconnecting or blocked; no bound is claimed for the
   physical close of its socket or session in that fault state; repeated
   replacements during such a fault may temporarily accumulate retired SDK
   objects, goroutines and sockets. This is accepted as a **documented 1.0
   fault-time resource limitation**, to be recorded in `docs/ddsonic/` and
   the changelog with the implementation. No dependency fork or pin is added
   to force a physical close. It does not waive the reviewer's findings, live
   acceptance, the security audit, or future SDK-lifetime cleanup (4.3).
7. **A build that succeeds after its deadline is discarded, approved.** A
   successful build that returns after its call's deadline, when the
   acceptance boundary has closed, is discarded together with any rotated
   refresh token it carries. This is the class of item 1 (a result that
   never reached acceptance), not a new credential-integrity exception, and
   a late result is never committed merely to keep a rotated token. It does
   not permit losing a rotation **after** a settle or a claim, weakening the
   closed or cancelled guards, or suppressing the write of an accepted,
   still-dirty credential. The next launch may need sign-in; that stays
   documented with item 1 (4.2).

**Not decided here, and not waived: the established-track decoder leak**
(risk d6). The owner has placed it outside this slice for investigation after
1.0. That is tracking, not a waiver; whether 1.0 may ship with it is a
separate release question the dispatcher is taking to the owner.

Apart from that separate question, this design needs no further owner-level
decision: no product wording, no new key, no CLI or IPC surface, no change to
the mutation rule, no change to credential deletion, and it asks for no
waiver.

### 15.5 Order of implementation

Regressions are written first and shown red where a defect exists today.
Steps 1 to 3 are each approvable on their own. **Steps 4 to 7 are one
authority change and are approvable only together:** until step 7 the tree
still has a second refresher (the old reconnect, the old sign-in builder), so
no intermediate commit may be described as "one token authority".

1. R8-4 (isolated, `session.go`).
2. R8-3 (runtime and UI; no Spotify package change).
3. Coordinator type, `credStore`, the durable section and their unit tests,
   unused by production.
4. Playback bundle and retirement (4.3), with the SDK contract test.
5. Builders create coordinators; `Session.view`; `publish`, `Close` and the
   acceptance points use the durable section. **In the same commit**
   `Session.Reconnect` becomes playback-only, so coordinator activation never
   coexists with a reconnect that refreshes the stored token.
6. The shared stored build replaces `sessionMu` and `NewSession` as the
   sign-in builder (4.2).
7. The request driver replaces `authorized`/`unauthorized`/`accessToken`/
   `renewToken`/`restoreWebAPIToken`; `restoreMu`, `sourceID`, `credential`,
   `tokenStamp` deleted. R8-1 and R8-2 regressions go green here.
8. Hygiene removals; docs (`plan.md`, `docs/ddsonic/upstream.md`,
   `CHANGELOG.md`, the 15.4(1) limitation).

## 16. Reviewer hotspots

Five places where this design removes or reinterprets something the round-8
review relied on. Each needs the reviewer's explicit approval, not silence.

### 16(a) The obsolete probe `TestReviewerForcedAnswerCannotOutliveNewerAutomaticRefresh`

The probe holds a forced refresh, lets an automatic refresh publish a later
token, then releases the forced answer and asserts the later token survives.
Under this design that ordering does not exist, and the probe run unchanged
would block (its reader joins the held flight; the probe releases the flight
only after the reader returns). It is not deleted. It is replaced by tests of
the four invariants that make its ordering impossible, and the reviewer is
asked to approve this mapping:

| Invariant | Test |
|---|---|
| One refresh flight per coordinator (I1) | `TestConcurrentRenewalsShareOneFlight` {success, invalid_grant, outage} x {forced first, automatic first}: six cases. The first caller is held at the token endpoint (B1); the second is a **real joined caller**, proven by B3 and not by timing; exactly one token request has been made. Both go through the production request path, not a test adapter around the coordinator. |
| Automatic and forced renewal share that flight | In "forced first" the joiner is a Web API read whose lease has expired (B5); in "automatic first" the joiner is a read that got a 401 on the same lease. Both callers receive the one answer, and the asserted revision is the same `rev+1` for both. |
| Every accepted token publication advances the revision (D3, I2) | `TestHealthySilentRefresh` and `TestEveryPublicationAdvancesRevision`: the actual `rev` is read after the seed, an automatic refresh and a forced renewal: 1, 2, 3; a refused or failed flight leaves it unchanged. |
| No second refresh answer for one coordinator can outlive a newer accepted refresh (I2, I9) | `TestRenewAfterNewerLeaseSendsNothing` (a forced caller with an old basis makes no request), and `TestClosedCoordinatorCommitsNothing` (the only way an answer can be late is that its coordinator was closed, and then it commits and writes nothing). |

The probe's three outcomes after the change: *success*: one answer, in memory
and on disk; *invalid_grant*: the sign-in is refused, correctly, because that
answer is the newest and only one and no accepted token is newer; *outage*:
nothing changes. The property the probe was after, "an older answer must not
defeat a newer accepted token", is I2/I3 and is tested where a late answer can
still occur: a late 401 (`TestStale401DoesNotRefuseNewerLease`, with real
expiry too) and a late flight on a replaced sign-in.

### 16(b) Playback-only reconnect

`Session.Reconnect` rebuilds the librespot session and player and keeps the
live coordinator (7.5).

- **No second refresh authority.** The rebuild makes no token request and
  builds no coordinator. It reads the librespot identity from
  `tokens.snapshot()`, not from the file. Today's reconnect refreshes the same
  refresh token as the live source, outside `restoreMu`, and stages a second
  credential; that is removed. `TestPlaybackRebuildKeepsTokens` asserts zero
  token requests and an unchanged lease.
- **Auth-dead state is not bypassed.** The coordinator's refusal is not
  touched by the rebuild: a refused Web API sign-in stays refused after a
  successful playback reconnect, and `needsSignIn` stays true. The rebuild
  clears only `playbackRefused`, the mark it is the recovery for.
- **No stale-token publication.** The rebuild publishes no lease. Its only
  credential effect is `setIdentity` (the librespot half), inside the
  publication step that revalidated `closed` and `gen == from`, followed by a
  write in the same durable section (section 8). A rebuild that lost to a
  sign-in is closed and sets nothing.
- **Playback recovery is preserved.** The stream path is unchanged: auth
  error, silent reconnect, retry, and `ErrNeedsAuth` with
  `refuseSignIn(gen)` when the reconnect fails for the sign-in or the retry
  still fails. A stream auth error concerns librespot's keys, which the Web
  API token does not feed.
- **Not covered by tests:** real librespot. Whether a stored-credential
  rebuild restores playback against the real service, and what blob it
  returns, stays a required live check on the next candidate.

### 16(c) Whole-session refusal stays on `Session`

`refuseSignIn(gen)` is raised only by playback (`provider.go:764,779`), when
librespot refused a stream and a silent rebuild did not cure it. It says
"this session build cannot play; a sign-in must replace it". It is a different
kind of fact from token state:

- **Different subject.** Its subject is a session build (`gen`), not a token.
  A token renewal does not cure it, which is why round 5 required it to
  survive renewal. Inside the coordinator it would need its own key and its
  own clearing rule, driven by playback events the coordinator knows nothing
  about.
- **It cannot invalidate a newer token revision.** It never reads or writes
  `rev`, `refused` or `refusedRev`. It is one boolean in `Session.view`, set
  under L3 only if `gen` is still current.
- **It is not a token authority.** It issues, caches, refreshes and persists
  nothing. Its only effects: `needsSignIn` is true (so `Authenticate`
  replaces the session), and step 4 of the request loop returns
  `errSignInRefused` before any token is asked for.
- **It cannot defeat the revision checks.** It only ever blocks; it never
  admits a request or a result. Every admit decision (`settle`, `refuse`,
  the durable section) is made by the coordinator on its own state. It is cleared only by
  a publication into the session (new `gen`), and an interactive publication
  brings a new coordinator anyway.

### 16(d) Request retry as a finite state machine

One logical request (`apiCall`), section 5. This table is the rule; section 5
describes the same machine. Counters: `authRound` (0..3), `rateTry` (0..7),
`renewedIn` (the coordinator this request last spent its recovery renewal in;
a replaced coordinator is closed and never met again, so one pointer is a
set). Every edge marked **auth** or **rate** goes through `backAuth` or
`backRate`, which check the limit before moving: at `authRound == 3` an auth
edge is FAIL ("kept changing"); at `rateTry == 7` a rate edge is FAIL (rate
limited).

| State | Event | Next | Edge |
|---|---|---|---|
| GATE | rate block active | FAIL (rate limit) | |
| GATE | no session / closed / playback refused | FAIL | |
| GATE | write and pin not held | FAIL (`errSignInChanged`) | |
| GATE | otherwise | TOKEN | |
| TOKEN | lease (after an automatic flight if it was unusable) | SEND | |
| TOKEN | refused, or no refresh token | FAIL (`ErrNeedsAuth`) | |
| TOKEN | outage, or caller cancelled | FAIL | |
| TOKEN | superseded, read | GATE | auth |
| TOKEN | superseded, write | FAIL (`errSignInChanged`) | |
| SEND | any status other than 401, 429 | DONE | |
| SEND | 429, `waitRate` false | FAIL (rate limit) | |
| SEND | 429, `waitRate` true, long `Retry-After` | FAIL (gate closed) | |
| SEND | 429, `waitRate` true, otherwise | WAIT then GATE | rate |
| WAIT | caller cancelled | FAIL | |
| SEND | 401, coordinator is not `renewedIn` | RENEW | `renewedIn` = it |
| SEND | 401, coordinator is `renewedIn`, lease current | FAIL (`ErrNeedsAuth`; refused) | |
| SEND | 401, coordinator is `renewedIn`, lease superseded, read | GATE | auth |
| RENEW | success, read | GATE | auth |
| RENEW | success, write | PROBE (`GET /v1/me`, once), then FAIL (not sent, or `ErrNeedsAuth`) | |
| RENEW | refused / outage | FAIL | |
| RENEW | superseded, read | GATE | auth |
| RENEW | superseded, write | FAIL (`errSignInChanged`) | |

**Termination.** The only edges back to GATE are auth and rate edges. Each
increases a counter that is bounded and never decreased or reset (Q1). So a
request visits GATE at most 1 + 3 + 7 = 11 times (a write, which takes no
auth edge, at most 8).

**Three different things are counted, and they are not the same:**

| Count | One read | One write | Why |
|---|---|---|---|
| Web API attempts (SEND, plus the write's one PROBE) | 11 | 9 | one per GATE visit; the write adds its probe |
| Recovery renewals (explicit `renew` after a 401) | 4, and 1 per sign-in | 1 | RENEW needs a coordinator that is not `renewedIn`; every non-final RENEW of a read takes an auth edge (3), plus one final |
| Token flights started or joined (recovery **and** automatic) | 15 | 10 | at most one automatic flight per TOKEN visit (11; write 8), plus one per RENEW (4; write 1), plus one for the write probe's `token()` |
| Interactive sign-ins started | 0 | 0 | no state opens a browser or calls `Authenticate` |

- **Automatic refreshes are not limited by the recovery budget.** If the
  token expires before each of eight TOKEN visits separated by rate waits,
  the request makes eight automatic flights with `authRound == 0` and zero
  recovery renewals. That is correct and is today's behaviour: the `oauth2`
  source refreshes whenever it is asked while expired. Revision 2's "renew at
  most 4 / at most 1 per sign-in" described recovery renewals only and was
  wrong as a statement about all refreshes.
- **A flight is not an HTTP request count.** One flight is one call of
  `silentTokenRefresh`. The `oauth2` library may send a refused request a
  second time with the other client-authentication style until it has
  learned the endpoint's style (the existing tests count two requests for an
  `invalid_grant`). So token-endpoint HTTP requests are at most twice the
  flights: 30 for a read, 20 for a write, in a worst case that needs an
  expiry before every visit. A joined flight costs the joiner no request.
- **No sign-in loop.** A sign-in happens only when the user asks. A
  user-driven sign-in moves a pending read through "superseded" at the cost
  of one auth edge; the fourth ends the request.

Combinations asked about: 401, renew, 429, 401 -> refused after one recovery
renewal (R8-2). 401, renew, sign-in, 401 -> the second 401 is on a new
coordinator: one recovery renewal there, then a third 401 on it refuses. 429,
401, 429, 401 -> the second 401 on a current lease refuses; if other requests
keep publishing newer leases, each pass costs an auth edge and the fourth
ends it. Expiry in every wait -> eight attempts, eight automatic flights,
success.

### 16(e) Owed-sync coalescing

Stated as guarantees in section 11 (G1 to G5). The argument in one place:
`owed` is a single bit per provider, written only under `rt.mu`: set by
`rt.sync` for a required request, or for an explicit one after `announced`;
cleared by the release step in the same critical section that decides to run
again. Therefore:

- at most one owed run per provider is ever pending (one bit);
- an explicit refresh in the window is not lost (set and release decision are
  under the same lock, and `announced` precedes visibility);
- there is no queue to grow (no allocation per request);
- a run cannot cause its own follow-up (the bit is consumed when read, and a
  run issues only background requests, which never set it);
- an older run's completion cannot replace a newer verdict (runs are
  sequential and their messages ordered; against a sign-in the ticket
  decides).

Background and quiet behaviour is unchanged: the quiet expression is the one
in the code today, and the retry timer's request is a no-op while a run owns
the provider.
