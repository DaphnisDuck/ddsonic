// Package spotify integrates Spotify playback into cliamp via go-librespot.
package spotify

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	"github.com/devgianlu/go-librespot/mp3"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/gopxl/beep/v2"
)

const (
	spotifySampleRate = 44100
	spotifyChannels   = 2
)

// spotifyStreamer bridges a go-librespot AudioSource to beep.StreamSeekCloser.
// go-librespot outputs interleaved stereo float32 at 44100Hz; this converts
// to Beep's [][2]float64 sample format.
type spotifyStreamer struct {
	mu         sync.Mutex
	source     librespot.AudioSource
	buf        []float32
	durationMs int64
	err        error
	cancel     func()
	closing    atomic.Bool
	closed     bool
	diagSeq    uint64 // ddsonic: the open's #168 diagnostic sequence number, or 0
}

// newSpotifyStreamer wraps a go-librespot Stream as a beep.StreamSeekCloser.
func newSpotifyStreamer(stream *librespotPlayer.Stream, cancel func()) *spotifyStreamer {
	var dur int64
	if stream.Media != nil {
		dur = int64(stream.Media.Duration())
	}
	if cancel == nil {
		cancel = func() {}
	}
	return &spotifyStreamer{
		source:     stream.Source,
		durationMs: dur,
		cancel:     cancel,
	}
}

// Stream reads interleaved float32 from the AudioSource and converts to
// [][2]float64 stereo pairs for Beep's audio pipeline.
func (s *spotifyStreamer) Stream(samples [][2]float64) (n int, ok bool) {
	if s.closing.Load() {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return 0, false
	}

	// Each stereo sample pair needs 2 float32 values (L, R).
	needed := len(samples) * spotifyChannels
	if len(s.buf) < needed {
		s.buf = make([]float32, needed)
	}

	nRead, err := s.source.Read(s.buf[:needed])
	if err != nil && err != io.EOF {
		if !s.closing.Load() {
			s.err = err
		}
		return 0, false
	}

	// Ensure we only process complete stereo pairs (drop any trailing mono sample).
	nRead -= nRead % spotifyChannels

	// Convert interleaved float32 [L0,R0,L1,R1,...] to [][2]float64 pairs.
	pairs := nRead / spotifyChannels
	for i := range pairs {
		samples[i][0] = float64(s.buf[i*2])
		samples[i][1] = float64(s.buf[i*2+1])
	}

	if pairs == 0 && err == io.EOF {
		return 0, false
	}
	return pairs, true
}

func (s *spotifyStreamer) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Len returns the total number of sample pairs (at 44100Hz stereo).
func (s *spotifyStreamer) Len() int {
	return int(s.durationMs * spotifySampleRate / 1000)
}

// Position returns the current playback position in sample pairs.
func (s *spotifyStreamer) Position() int {
	if s.closing.Load() {
		return 0
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return 0
	}
	return int(s.source.PositionMs() * spotifySampleRate / 1000)
}

// Seek moves to sample position p (in sample pairs at 44100Hz).
func (s *spotifyStreamer) Seek(p int) error {
	if s.closing.Load() {
		return net.ErrClosed
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return net.ErrClosed
	}
	ms := int64(p) * 1000 / spotifySampleRate
	return s.source.SetPositionMs(ms)
}

// Close cancels stream I/O and releases cliamp's references. AudioSource does
// not expose Close intentionally; calling the concrete C-backed decoder Close
// methods during a track handoff can crash go-librespot.
//
// ddsonic: except the MP3 and FLAC decoders, which nothing else frees (RC4-S3).
func (s *spotifyStreamer) Close() error {
	if s.closing.CompareAndSwap(false, true) {
		// A decoder read may be waiting on a chunk request while holding mu.
		// Cancel it before waiting to close the decoder.
		s.cancel()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true

	// ddsonic: an accepted MP3 or FLAC decoder holds native memory (for MP3
	// a cgo.Handle and the mpg123 handle) and the downloaded chunks until it
	// is closed. It is closed here, once, after its read was cancelled and
	// under mu, so no Stream, Seek or Position is using it. Other decoders
	// are left as before: Vorbis's Close is not safe here (playback_ddsonic.go,
	// releaseDecoder).
	noteAcceptedClose(s.diagSeq, s.source, false) // ddsonic: #168 diagnostics, a no-op when off
	switch d := s.source.(type) {
	case *mp3.Decoder:
		closeAcceptedDecoder(d)
	case *flac.Decoder:
		closeAcceptedDecoder(d)
	}
	noteAcceptedClose(s.diagSeq, s.source, true) // ddsonic: #168
	s.source = nil
	s.buf = nil
	return nil
}

// closeAcceptedDecoder closes the decoder of an accepted stream (Close). A
// test replaces it to count the closes it cannot observe otherwise.
var closeAcceptedDecoder = func(d io.Closer) { _ = d.Close() }

// Format returns the Beep audio format for Spotify streams.
func (s *spotifyStreamer) Format() beep.Format {
	return beep.Format{
		SampleRate:  beep.SampleRate(spotifySampleRate),
		NumChannels: spotifyChannels,
		Precision:   4, // float32 = 4 bytes
	}
}

// Duration returns the track duration.
func (s *spotifyStreamer) Duration() time.Duration {
	return time.Duration(s.durationMs) * time.Millisecond
}
