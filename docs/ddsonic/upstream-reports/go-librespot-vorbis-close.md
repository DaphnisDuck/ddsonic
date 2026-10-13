# go-librespot: `vorbis.(*Decoder).Close` can abort the process

> **DRAFT, NOT YET FILED.** Prepared 2026-10-13 for the go-librespot project. Nothing has been sent anywhere. The owner (Steve), or whoever the owner designates, reviews this text and files it. Until then go-librespot's maintainers do not know of it. ddsonic's 1.0 release does not wait for an answer (`plan.md`, Decisions log, 2026-10-13).

## Title

`vorbis.(*Decoder).Close` frees the stream info before clearing the dsp state, and can abort with `free(): invalid pointer`

## Version

- `github.com/devgianlu/go-librespot` v0.9.1
  - `go.sum`: `h1:cgHB0Qo3GgqBe+8iZ1qw+kPJVp1s0obTlTXMdPsgb0U=`
  - `go.sum`, `go.mod` line: `h1:tcLnLzCmW9xbPe2Uo0wMiu08d1wzNI2b2zuWlFYEx00=`
- `github.com/xlab/vorbis-go` v0.0.0-20210911202351-b5b85f1ec645 (the binding go-librespot uses)
  - `go.sum`: `h1:lYg/+vV/Fd5WM1+Ptg54Am3y4mDXaMSrT+mKUHV5uVc=`

The code in question is `vorbis/decoder.go`, `(*Decoder).Close` and `(*Decoder).decoderStateCleanup` (lines 115-159 at v0.9.1). Whether a later version changed it has not been checked.

## Symptom

Closing a Vorbis decoder sometimes aborts the whole process:

```
free(): invalid pointer
SIGABRT: abort
signal arrived during cgo execution

github.com/xlab/vorbis-go/vorbis._Cfunc_vorbis_dsp_clear(...)
github.com/xlab/vorbis-go/vorbis.DspClear(...)
        vorbis-go/vorbis/vorbis.go:402
github.com/devgianlu/go-librespot/vorbis.(*Decoder).decoderStateCleanup(...)
        go-librespot/vorbis/decoder.go:148
github.com/devgianlu/go-librespot/vorbis.(*Decoder).Close(...)
        go-librespot/vorbis/decoder.go:125
```

It happens in `Close` itself. The decoder does not have to have been read from, `Close` is called once, from one goroutine, and nothing else is using the decoder.

## Minimal reproduction

A test inside go-librespot's own `vorbis` package, so that nothing but the package and its dependencies is involved. Any small Ogg Vorbis file will do; this one was made with:

```sh
ffmpeg -f lavfi -i "sine=frequency=440:duration=0.3:sample_rate=44100" -ac 2 -c:a libvorbis -q:a 3 -f ogg tone.ogg
```

```go
package vorbis

import (
	"bytes"
	"os"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
)

func TestCloseLoop(t *testing.T) {
	data, err := os.ReadFile(os.Getenv("OGG"))
	if err != nil {
		t.Fatal(err)
	}
	for range 400 {
		d, err := New(&librespot.NullLogger{}, bytes.NewReader(data), nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}
```

```sh
OGG=$PWD/tone.ogg go test -count=1 -run TestCloseLoop ./vorbis
```

The abort is intermittent, so the test is run several times.

### What was measured

Each run is a separate process doing 400 construct-and-close cycles.

| Cleanup order | Runs | Runs that aborted |
|---|---|---|
| `Close` as it is at v0.9.1 | 15 | 12 |
| The same cleanup calls in the order below ("Proposed change") | 15 | 0 |

The second row used the same loop with the decoder's cleanup calls written out in the test in the changed order, in place of `d.Close()`.

## Observed cause

`decoderStateCleanup` releases the decoder's state in this order:

1. `OggSyncClear`, `OggStreamClear`
2. `CommentClear` and free of the comment
3. `InfoClear` and free of the info
4. `DspClear` and free of the dsp state
5. `BlockClear` and free of the block

The dsp state keeps a pointer to the info it was initialised with (`vorbis_synthesis_init(&dsp, &info)`), and `vorbis_dsp_clear` reads that info while it tears the dsp state down. In the order above the info has been cleared, and its memory freed, by the time `vorbis_dsp_clear` runs, so it reads freed memory. What it then does depends on what that memory holds, which is why the abort is intermittent.

libvorbis's own decoding example (`examples/decoder_example.c`) cleans up in the opposite order: `vorbis_block_clear`, then `vorbis_dsp_clear`, and later `vorbis_comment_clear` and `vorbis_info_clear`, the last with the comment "must be called last".

This is a reading of the code together with the measurement above. The exact path inside `vorbis_dsp_clear` that ends in `free(): invalid pointer` was not traced further.

## Proposed change

Move the dsp-state and block cleanup ahead of the comment and info cleanup, block first:

```go
func (d *Decoder) decoderStateCleanup() {
	if d.block.Ref() != nil {
		vorbis.BlockClear(&d.block)
		d.block.Free()
	}

	if d.dspState.Ref() != nil {
		vorbis.DspClear(&d.dspState)
		d.dspState.Free()
	}

	vorbis.OggSyncClear(&d.syncState)
	d.syncState.Free()

	if d.streamState.Ref() != nil {
		vorbis.OggStreamClear(&d.streamState)
		d.streamState.Free()
	}

	if d.comment.Ref() != nil {
		vorbis.CommentClear(&d.comment)
		d.comment.Free()
	}

	if d.info.Ref() != nil {
		vorbis.InfoClear(&d.info)
		d.info.Free()
	}

	d.packet.Free()
	d.page.Free()
}
```

Only the two blocks for the block and the dsp state move; every call is otherwise as it is today. This is the order the second row of the table was measured with.

## Environment

- Linux x86-64 (kernel 7.2.5), glibc 2.44
- Go 1.27.2, cgo, GCC 16.2.1
- libvorbis 1.3.7, libogg 1.3.6 (system libraries, found through pkg-config)

Not tried: other platforms, other libvorbis versions, other allocators, a decoder that has been read from before `Close`.

## Why it matters to a caller

A caller that has finished with a decoder has no safe way to release it: `Close` is the only release, the fields it works on are unexported, and there is no finalizer. A caller that wants to stay up has to leave the decoder's native memory unreleased.
