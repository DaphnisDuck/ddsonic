# #168 memory diagnostics: a non-release diagnostic build

This branch (`investigate/168-memory-diag`) is for the #168 investigation only. It is never merged into `main`, tagged or released, and its binary is not a release candidate. #168 is the authority; #156 indexes the release evidence.

## What it adds

All of it is off unless `DDSONIC_MEMDIAG` names a directory. Unset or empty, nothing is created and no goroutine or signal handler is started; the code paths reduce to a nil check.

- `internal/memdiag`: counters, an event log and snapshots.
- go-librespot patch `0004-player-diagnostic-hook.patch`: an opt-in hook in `Player.NewStream`. It reports the selected format, each decoder's construction (success or failure, by codec), and a failure after a decoder was constructed and before the stream was handed off. The patch edits existing lines of `player/player.go` only, so no line of it moves. It adds `player/diag_ddsonic.go`.
- `external/spotify/memdiag_ddsonic.go` and small `// ddsonic:` hooks: the counters listed below.

The semantics are unchanged: decoder selection, the lifecycle and cleanup (Vorbis `Close` stays skipped; the SDK's pre-handoff gap is counted, not fixed), retries and backoff, prefetch sizes, GC settings and timing settings are as on `main`. With diagnostics on, the instrumentation itself has a cost: counter locks, event and context allocations, a log writer goroutine, and the snapshots' profile capture. It can perturb scheduling and memory a little, most of all during a snapshot. Compare runs with diagnostics on with each other, not with a run of a release build.

The output names no account, track, URI, file name, token or credential. Events carry a process-local sequence number per open, fixed names, and codec or format names (for example `OGG_VORBIS_320`), and never an error's text. Profiles and dumps are written with their paths removed (see Run).

## Counters

Each name is a key of `counters` in a snapshot line. `<codec>` is `mp3`, `vorbis`, `flac`, `aac`, `other` or `none` (no stream). A gauge goes up and down; the others only count up.

Starts (the player asking for a Spotify track, current or preloaded):

- `starts_attempted` (at entry), `starts_in_progress` (gauge), `starts_ok`, `starts_failed`. A start includes at most one recovery, so it can make two open attempts.

Opens (one librespot setup each):

- `open_attempts`; `open_setup_ok.<codec>`, `open_setup_error` (what the setup returned to its worker).
- `open_accepted.<codec>`, `open_error_accepted` (an error the caller accepted).
- `open_gave_up.<reason>`: the caller stopped waiting before a result. `<reason>` is `cancelled`, `deadline`, `session_changed` (the session's bundle was retired: replaced or closed) or `other`.
- `open_discarded_<why>.<codec>`: a result discarded before acceptance; `.none` when the setup returned no stream. `<why>` is `caller_gave_up_first` (the worker found the caller gone; it does not know why: the same open's `open_gave_up.<reason>` does), `caller_gave_up_after_result_<reason>`, or `rejected_at_accept_<reason>`.

Inside go-librespot (patch 0004):

- `sdk_format_selected.<codec>`, `sdk_format_selected_by_format.<format>` (for example `MP3_320`, `OGG_VORBIS_320`).
- `sdk_decoder_open_ok.<codec>`, `sdk_decoder_open_failed.<codec>`.
- `sdk_failed_after_decoder_before_handoff.<codec>`: a decoder the SDK built and then returned an error without (it is not closed).

Decoder release:

- `streamer_close_entry`, `streamer_close_in_progress` (gauge), `streamer_close_returned`: an accepted stream's `Close`, counted on entry before it takes the decoder's lock, so a `Close` blocked behind a read shows as in progress.
- `decoder_close_requested.accepted.<codec>`, `decoder_close_completed.accepted.<codec>` (MP3 and FLAC); `vorbis_close_skipped.accepted`; `decoder_not_closed.accepted.<codec>` (another kind).
- `decoder_close_requested.discarded.<codec>`, `decoder_close_completed.discarded.<codec>`; `vorbis_close_skipped.discarded`.

Decode-ahead (prefetch) streams:

- `prefetch_opens.<codec>`, `prefetch_close_completed.<codec>` (the fill goroutine has ended).
- `prefetch_streams_open`, `prefetch_streams_open_played`, `prefetch_streams_open_not_yet_played` (gauges).
- `prefetch_ring_bytes_allocated_total`, `prefetch_ring_bytes_open` (gauge).
- `stream_completed.<codec>`, `stream_ended_error.<codec>` (the speaker has had all of it).
- `stream_closed_after_completed.<codec>`, `stream_closed_after_ended_error.<codec>`, `stream_closed_while_playing.<codec>`, `stream_closed_before_audio.<codec>`.

Bundles: `bundles_created`, `bundles_retired`, `bundles_tail_finished`, `bundle_tails_running` (gauge), `open_workers_active` (gauge).

Collection: `memdiag_events_dropped`, `memdiag_write_errors.events`, `memdiag_write_errors.snapshot`, `memdiag_snapshot_errors`. Any of them above zero makes the run's lines `collection_incomplete`.

What the counters cannot say:

- "Played" and "not yet played" are inferred from whether the speaker has had audio from a stream. A stream not yet played is usually a gapless preload, but the count is not proof of one: a stream just opened for playback is also "not yet played" until its first audio.
- `stream_closed_while_playing` is a hand skip, a stop or a replacement while playing: the code cannot tell them apart. The operator's own record of what was done at each checkpoint is needed for that.
- `prefetch_ring_bytes_open` is the logical size of the decode-ahead rings of open streams (4 s of audio each). It is not the audio actually buffered, and not memory given back once a stream closes (that is the GC's).
- The downloaded chunks (512 KiB each, held by the SDK's reader) are not counted. The heap profiles are the first place to look for them.

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

Each snapshot appends one JSON line to `snapshots.jsonl` and writes two files beside it, both with their paths removed:

- the counters;
- Go's runtime/metrics: every `/memory/classes/` class, the live heap, `committed_bytes` (mapped and not released), GC cycles, goroutines and cgo calls;
- `/proc/self/status` `Vm*` and `Rss*` (kB);
- `/proc/self/smaps_rollup` (kB, including anonymous and file-backed);
- the thread count;
- open descriptors counted by type (socket, pipe, anon_inode kind, dev, file), never by path;
- `goroutines-NNN.txt`, a full goroutine dump. Each source location is cut to its file's base name (`file.go:123`). Kept: goroutine numbers, states and wait times, function names with their module paths, and the hex argument words (raw register and stack values; nothing is dereferenced).
- `heap-NNN.pb.gz`, a heap profile. The program's own mapping is named `executable`, other mappings (shared libraries) and absolute source files by their base names. Function names, lines, addresses, build IDs and samples are kept. Read it with the binary named explicitly: `go tool pprof ./ddsonic-v1.1.0-diag.1 heap-001.pb.gz`.

A last snapshot is taken at exit. Then each of `events.jsonl` and `snapshots.jsonl` ends with a line of `"kind":"end"`, which says whether the run is `collection_incomplete` and gives the `memdiag_*` counters. Each snapshot line carries the same flag, for that snapshot and everything before it. An incomplete run also prints one line on stderr at exit: `memdiag: collection incomplete` (no path, no error text). Collection failures never change playback. The snapshot does not run the GC: the heap profile is as of the last cycle, whose count the line records. Heap profiles show Go allocations only, not the codec libraries' native memory. `/proc` values are Linux-only.

`events.jsonl` has one line per event, with the open's sequence number. It reads as a lifecycle per open: `open_attempt`, `sdk_format_selected`, `sdk_decoder_open_ok`, `open_setup_ok`, `open_accepted`, `prefetch_open`, `first_audio`, `stream_completed`, then `decoder_close_requested`/`decoder_close_completed` or `vorbis_close_skipped`, and `prefetch_close_completed_*`.

The files are private evidence: keep them out of the repository and out of public issues. Publish only sanitized figures.
