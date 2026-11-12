# #168 memory diagnostics: a non-release diagnostic build

This branch (`investigate/168-memory-diag`) is for the #168 investigation only. It is never merged into `main`, tagged or released, and its binary is not a release candidate. #168 is the authority; #156 indexes the release evidence.

## What it adds

All of it is off unless `DDSONIC_MEMDIAG` names a directory. Unset or empty, nothing is created and no goroutine or signal handler is started; the code paths reduce to a nil check.

- `internal/memdiag`: counters, an event log and snapshots.
- go-librespot patch `0004-player-diagnostic-hook.patch`: an opt-in hook in `Player.NewStream`. It reports the selected format, each decoder's construction (success or failure, by codec), and a failure after a decoder was constructed and before the stream was handed off. The patch edits existing lines of `player/player.go` only, so no line of it moves. It adds `player/diag_ddsonic.go`.
- `external/spotify/memdiag_ddsonic.go` and small `// ddsonic:` hooks: attempted starts, open attempts, setup results, accepted and discarded opens (with why), decoder closes requested and completed, Vorbis closes deliberately skipped (accepted and discarded), prefetch ring bytes, open streams played and not yet played (a gapless preload is "not yet played"), completions, closes while playing (a hand skip, a stop or a replacement), closes before any audio, retired bundles and their tails, and open workers.

Nothing changes what it observes: decoder selection, the lifecycle and cleanup (Vorbis `Close` stays skipped; the SDK's pre-handoff gap is counted, not fixed), retries and backoff, prefetch sizes, GC settings and timing are as on `main`.

The output names no account, track, URI, file name, token or credential. Events carry a process-local sequence number per open, fixed names, and codec or format names (for example `OGG_VORBIS_320`), and never an error's text.

## Build

On the machine that will run it, from a clean checkout of the branch head:

```sh
test -z "$(git status --porcelain)" || { echo "the tree is not clean"; exit 1; }
COMMIT=$(git rev-parse HEAD)
VERSION=v1.1.0-diag.1
LIBS="vorbis=$(pkg-config --modversion vorbis),ogg=$(pkg-config --modversion ogg),libmpg123=$(pkg-config --modversion libmpg123),flac=$(pkg-config --modversion flac)"
GOTOOLCHAIN=local go build -trimpath \
  -ldflags "-X main.version=$VERSION -X github.com/bjarneo/cliamp/internal/memdiag.codecLibs=$LIBS" \
  -o "ddsonic-$VERSION" .
echo "$VERSION $COMMIT"
sha256sum "ddsonic-$VERSION"
```

- Use the pinned Go toolchain (`mise.toml`).
- The binary is not stripped (no `-s -w`), so external probes can read its symbols.
- `-trimpath` keeps build paths out of the binary and out of its goroutine dumps and heap profiles.
- Go records the commit and whether the tree was modified (`vcs.revision`, `vcs.modified`). The diagnostics copy both into the first line of `snapshots.jsonl`.
- The codec versions are the `pkg-config` versions at build time; the library loaded at run time is the system's.
- Create no tag. Keep the version string, the commit and the checksum with the run's private notes.

## Run

```sh
mkdir -m 700 /path/to/private/diag
DDSONIC_MEMDIAG=/path/to/private/diag ./ddsonic-v1.1.0-diag.1
```

- Each run writes a subdirectory `run-<UTC time>-<pid>` (mode 700). Its files are mode 600.
- An existing directory must be a directory, not a symlink, owned by the user and private (no group or other permission). Otherwise the program stops with an error rather than run without output.

To take a snapshot at a checkpoint:

```sh
kill -USR1 "$(pgrep -f 'ddsonic-v1.1.0-diag.1$')"
```

Send SIGUSR1 only to this binary with `DDSONIC_MEMDIAG` set: any other ddsonic, or this one without the variable, is ended by it.

Each snapshot appends one JSON line to `snapshots.jsonl` and writes two files beside it:

- the counters;
- Go's runtime/metrics: every `/memory/classes/` class, the live heap, `committed_bytes` (mapped and not released), GC cycles, goroutines and cgo calls;
- `/proc/self/status` `Vm*` and `Rss*` (kB);
- `/proc/self/smaps_rollup` (kB, including anonymous and file-backed);
- the thread count;
- open descriptors counted by type (socket, pipe, anon_inode kind, dev, file), never by path;
- `goroutines-NNN.txt`, a full goroutine dump;
- `heap-NNN.pb.gz`, a heap profile.

A last snapshot is taken at exit. The snapshot does not run the GC: the heap profile is as of the last cycle, whose count the line records. Heap profiles show Go allocations only, not the codec libraries' native memory. `/proc` values are Linux-only.

`events.jsonl` has one line per event, with the open's sequence number. It reads as a lifecycle per open: `open_attempt`, `sdk_format_selected`, `sdk_decoder_open_ok`, `open_setup_ok`, `open_accepted`, `prefetch_open`, `first_audio`, `stream_completed`, then `decoder_close_requested`/`decoder_close_completed` or `vorbis_close_skipped`, and `prefetch_close_completed_*`.

The files are private evidence: keep them out of the repository and out of public issues. Publish only sanitized figures.
