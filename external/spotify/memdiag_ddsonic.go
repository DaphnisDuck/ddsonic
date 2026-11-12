package spotify

// ddsonic: what a track open and its stream tell the opt-in memory
// diagnostics (#168, internal/memdiag). Every function here returns at once
// when diagnostics are off, and none changes what it observes: decoder
// selection, closing, retries, prefetch sizes and timing are as before. They
// record sequence numbers, fixed names and codec names only.

import (
	"context"
	"errors"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	"github.com/devgianlu/go-librespot/mp3"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/vorbis"

	"github.com/bjarneo/cliamp/internal/memdiag"
)

// EnableMemDiag installs go-librespot's diagnostic hook (patch 0004). main
// calls it once, only when diagnostics are on; DisableMemDiag removes it.
func EnableMemDiag()  { librespotPlayer.SetDiagHook(sdkDiag) }
func DisableMemDiag() { librespotPlayer.SetDiagHook(nil) }

// sdkDiag counts the stream setup's points inside go-librespot.
func sdkDiag(ctx context.Context, point librespotPlayer.DiagPoint, format string) {
	seq, codec := memdiag.OpenSeq(ctx), memdiag.Codec(format)
	var name string
	switch point {
	case librespotPlayer.DiagFormatSelected:
		name = "sdk_format_selected"
		memdiag.Count("sdk_format_selected_by_format", format)
	case librespotPlayer.DiagDecoderOpened:
		name = "sdk_decoder_open_ok"
	case librespotPlayer.DiagDecoderFailed:
		name = "sdk_decoder_open_failed"
	case librespotPlayer.DiagFailedBeforeHandoff:
		name = "sdk_failed_after_decoder_before_handoff"
	default:
		return
	}
	memdiag.Count(name, codec)
	memdiag.Event(seq, name, codec, format)
}

// decoderCodec names a stream's decoder by its type.
func decoderCodec(src any) string {
	switch src.(type) {
	case nil:
		return "none"
	case *vorbis.Decoder:
		return "vorbis"
	case *mp3.Decoder:
		return "mp3"
	case *flac.Decoder:
		return "flac"
	}
	return "other"
}

func streamCodec(stream *librespotPlayer.Stream) string {
	if stream == nil {
		return "none"
	}
	return decoderCodec(stream.Source)
}

// noteOpenAttempt counts one Session.NewStream that took a bundle.
func noteOpenAttempt(seq uint64) {
	if !memdiag.On() {
		return
	}
	memdiag.Count("open_attempts", "")
	memdiag.Event(seq, "open_attempt", "", "")
}

// noteSetupResult counts what librespot's setup returned to the worker.
func noteSetupResult(seq uint64, res openResult) {
	if !memdiag.On() {
		return
	}
	if res.err != nil {
		memdiag.Count("open_setup_error", "")
		memdiag.Event(seq, "open_setup_error", "", "")
		return
	}
	codec := streamCodec(res.stream)
	memdiag.Count("open_setup_ok", codec)
	memdiag.Event(seq, "open_setup_ok", codec, "")
}

// noteAccepted counts a stream the caller accepted, or an error it accepted.
func noteAccepted(seq uint64, res openResult) {
	if !memdiag.On() {
		return
	}
	if res.err != nil {
		memdiag.Count("open_error_accepted", "")
		memdiag.Event(seq, "open_error_accepted", "", "")
		return
	}
	codec := streamCodec(res.stream)
	memdiag.Count("open_accepted", codec)
	memdiag.Event(seq, "open_accepted", codec, "")
}

// noteDiscarded counts an open discarded before it was accepted, by why
// and, when the caller knows it (outcome not nil), the reason:
// with its codec when a stream was set up (releaseDecoder then closes its
// decoder, or for Vorbis deliberately does not), or "none" when the setup
// returned no stream (it failed, or was cancelled, first).
func noteDiscarded(seq uint64, why string, outcome error, stream *librespotPlayer.Stream) {
	if !memdiag.On() {
		return
	}
	if outcome != nil {
		why += "_" + openReason(outcome)
	}
	codec := streamCodec(stream)
	memdiag.Count("open_discarded_"+why, codec)
	memdiag.Event(seq, "open_discarded_"+why, codec, "")
}

// noteRelease counts releaseDecoder's work on a discarded stream's decoder:
// at "requested" and "completed" for a decoder it closes, or "skipped" for a
// Vorbis one. The events are the open's (noteDiscarded); these are counters.
func noteRelease(stage string, src any) {
	if !memdiag.On() {
		return
	}
	codec := decoderCodec(src)
	switch stage {
	case "skipped":
		memdiag.Count("vorbis_close_skipped", "discarded")
	default:
		memdiag.Count("decoder_close_"+stage, "discarded."+codec)
	}
}

// noteAcceptedClose counts spotifyStreamer.Close's work on an accepted
// stream's decoder, before (done false) and after (done true) the close.
func noteAcceptedClose(seq uint64, src librespot.AudioSource, done bool) {
	if !memdiag.On() {
		return
	}
	codec := decoderCodec(src)
	var name string
	switch {
	case codec == "vorbis" && !done:
		name = "vorbis_close_skipped"
		memdiag.Count(name, "accepted")
	case codec == "vorbis", codec == "none":
		return
	case codec != "mp3" && codec != "flac":
		if !done {
			memdiag.Count("decoder_not_closed", "accepted."+codec)
		}
		return
	case done:
		name = "decoder_close_completed"
		memdiag.Count(name, "accepted."+codec)
	default:
		name = "decoder_close_requested"
		memdiag.Count(name, "accepted."+codec)
	}
	memdiag.Event(seq, name, codec, "")
}

// withDiagSeq gives an accepted stream its open's sequence number.
func withDiagSeq(s *spotifyStreamer, ctx context.Context) *spotifyStreamer {
	s.diagSeq = memdiag.OpenSeq(ctx)
	return s
}

// streamDiag is a prefetched stream's diagnostic state, nil when off. It is
// read and written under the stream's mu.
type streamDiag struct {
	seq       uint64
	codec     string
	ringBytes int64
	played    bool // the speaker has had audio from it
	ended     string
	closed    bool
}

func newStreamDiag(s *spotifyStreamer, ringBytes int64) *streamDiag {
	if !memdiag.On() {
		return nil
	}
	d := &streamDiag{seq: s.diagSeq, codec: decoderCodec(s.source), ringBytes: ringBytes}
	memdiag.Count("prefetch_opens", d.codec)
	memdiag.Add("prefetch_ring_bytes_allocated_total", "", ringBytes)
	memdiag.Add("prefetch_ring_bytes_open", "", ringBytes)
	memdiag.Add("prefetch_streams_open", "", 1)
	memdiag.Add("prefetch_streams_open_not_yet_played", "", 1)
	memdiag.Event(d.seq, "prefetch_open", d.codec, "")
	return d
}

// firstAudio notes the speaker's first audio from the stream: from here on
// it is the stream playing, not one opened ahead (a gapless preload).
func (d *streamDiag) firstAudio() {
	if d == nil || d.played {
		return
	}
	d.played = true
	memdiag.Add("prefetch_streams_open_not_yet_played", "", -1)
	memdiag.Add("prefetch_streams_open_played", "", 1)
	memdiag.Event(d.seq, "first_audio", d.codec, "")
}

// endedWith notes that the speaker has had all of the stream: "completed"
// for a clean end (err nil), "ended_error" otherwise. Only the first counts.
func (d *streamDiag) endedWith(err error) {
	if d == nil || d.ended != "" {
		return
	}
	how := "completed"
	if err != nil {
		how = "ended_error"
	}
	d.ended = how
	memdiag.Count("stream_"+how, d.codec)
	memdiag.Event(d.seq, "stream_"+how, d.codec, "")
}

// close notes a completed Close (the fill goroutine has ended), once, and
// how far the stream had got: completed, ended with an error, closed while
// playing (a hand skip, a stop or a replacement), or closed before any of
// its audio was played (a preload dropped, or a play replaced first).
func (d *streamDiag) close() {
	if d == nil || d.closed {
		return
	}
	d.closed = true
	how := "closed_before_audio"
	switch {
	case d.ended != "":
		how = "closed_after_" + d.ended
	case d.played:
		how = "closed_while_playing"
	}
	if d.played {
		memdiag.Add("prefetch_streams_open_played", "", -1)
	} else {
		memdiag.Add("prefetch_streams_open_not_yet_played", "", -1)
	}
	memdiag.Add("prefetch_streams_open", "", -1)
	memdiag.Add("prefetch_ring_bytes_open", "", -d.ringBytes)
	memdiag.Count("prefetch_close_completed", d.codec)
	memdiag.Count("stream_"+how, d.codec)
	memdiag.Event(d.seq, "prefetch_close_completed_"+how, d.codec, "")
}

// openReason names why an open ended unaccepted, from the error it ended
// with: the caller's cancellation or deadline, or the session's bundle
// retired (a replacement or a close). Anything else is "other".
func openReason(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, errSessionChanged):
		return "session_changed"
	}
	return "other"
}

// noteGaveUp counts an open its caller gave up on before accepting a result.
func noteGaveUp(seq uint64, outcome error) {
	if !memdiag.On() {
		return
	}
	why := openReason(outcome)
	memdiag.Count("open_gave_up", why)
	memdiag.Event(seq, "open_gave_up_"+why, "", "")
}

// noteCloseEntry counts a streamer Close as it starts, before it takes the
// decoder's lock, so a Close blocked behind a read is seen; the function it
// returns, deferred, counts its return.
func noteCloseEntry(seq uint64) func() {
	if !memdiag.On() {
		return func() {}
	}
	memdiag.Count("streamer_close_entry", "")
	memdiag.Add("streamer_close_in_progress", "", 1)
	memdiag.Event(seq, "streamer_close_entry", "", "")
	return func() {
		memdiag.Add("streamer_close_in_progress", "", -1)
		memdiag.Count("streamer_close_returned", "")
	}
}

// noteStartEntry counts a start the player asked for (PlaybackStreamer) as
// it begins; noteStart counts how it ended.
func noteStartEntry() {
	if !memdiag.On() {
		return
	}
	memdiag.Count("starts_attempted", "")
	memdiag.Add("starts_in_progress", "", 1)
}

// noteStart counts how a start ended.
func noteStart(err error) {
	if !memdiag.On() {
		return
	}
	memdiag.Add("starts_in_progress", "", -1)
	if err != nil {
		memdiag.Count("starts_failed", "")
	} else {
		memdiag.Count("starts_ok", "")
	}
}

// noteBundle counts playback bundles made, retired and torn down, and the
// open workers inside librespot.
func noteBundle(what string, d int64) {
	if !memdiag.On() {
		return
	}
	switch what {
	case "created", "retired", "tail_finished":
		memdiag.Count("bundles_"+what, "")
	}
	switch what {
	case "retired", "tail_finished":
		memdiag.Add("bundle_tails_running", "", d)
	case "worker":
		memdiag.Add("open_workers_active", "", d)
	}
}
