// Added by ddsonic on 2026-11-12: an opt-in diagnostic hook on the stream
// setup, for a non-release investigation
// (patches/go-librespot/0004-player-diagnostic-hook.patch).

package player

import (
	"context"
	"sync/atomic"

	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"
)

// DiagPoint is a point of NewStream that a diagnostic hook is told about.
type DiagPoint int

const (
	// DiagFormatSelected: the audio file to decode was chosen.
	DiagFormatSelected DiagPoint = iota
	// DiagDecoderOpened: the format's decoder was constructed.
	DiagDecoderOpened
	// DiagDecoderFailed: the format's decoder could not be constructed.
	DiagDecoderFailed
	// DiagFailedBeforeHandoff: a constructed decoder failed validation or
	// the initial seek, and NewStream returned an error without it. The
	// decoder is not closed here, as before.
	DiagFailedBeforeHandoff
)

// DiagHook is told the point reached and the selected file's format name
// (for example "OGG_VORBIS_320"). ctx is NewStream's. It must not block.
type DiagHook func(ctx context.Context, point DiagPoint, format string)

var diagHook atomic.Pointer[DiagHook]

// SetDiagHook installs h, or removes the hook when h is nil. With no hook,
// the diagnostic points do nothing.
func SetDiagHook(h DiagHook) {
	if h == nil {
		diagHook.Store(nil)
		return
	}
	diagHook.Store(&h)
}

func diagNote(ctx context.Context, point DiagPoint, file *metadatapb.AudioFile) {
	h := diagHook.Load()
	if h == nil {
		return
	}
	format := "UNKNOWN"
	if file != nil && file.Format != nil {
		format = file.Format.String()
	}
	(*h)(ctx, point, format)
}

// diagSelected notes the selected file and returns it.
func diagSelected(ctx context.Context, file *metadatapb.AudioFile) *metadatapb.AudioFile {
	diagNote(ctx, DiagFormatSelected, file)
	return file
}

// diagCtor passes a decoder constructor's results through, noting whether
// it succeeded.
type diagCtor[T any] struct {
	ctx  context.Context
	file *metadatapb.AudioFile
}

func (c diagCtor[T]) done(d T, err error) (T, error) {
	if err != nil {
		diagNote(c.ctx, DiagDecoderFailed, c.file)
	} else {
		diagNote(c.ctx, DiagDecoderOpened, c.file)
	}
	return d, err
}

// diagGap notes a failure after the decoder was constructed and before the
// stream was handed off, and returns err.
func diagGap(ctx context.Context, file *metadatapb.AudioFile, err error) error {
	diagNote(ctx, DiagFailedBeforeHandoff, file)
	return err
}
