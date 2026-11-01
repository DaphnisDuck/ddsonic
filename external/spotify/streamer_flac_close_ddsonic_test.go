package spotify

import (
	"io"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
)

// An accepted FLAC stream's decoder, librespot's real one over real
// (synthetic) audio, is closed when the streamer is closed, and once only: a
// second close would free its native decoder twice (RC4-S3, FLAC). Its
// Close leaves nothing to inspect, so the closes are counted.
func TestStreamerCloseClosesAcceptedFLACDecoderOnce(t *testing.T) {
	d, err := flac.New(&librespot.NullLogger{}, fixture(t, "tone.flac.bin"), 1)
	if err != nil {
		t.Fatal(err)
	}
	var closes []io.Closer
	orig := closeAcceptedDecoder
	closeAcceptedDecoder = func(c io.Closer) { closes = append(closes, c); orig(c) }
	t.Cleanup(func() {
		closeAcceptedDecoder = orig
		if len(closes) == 0 {
			_ = d.Close() // the streamer left it open
		}
	})
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: d}, nil)
	if n, ok := s.Stream(make([][2]float64, 512)); !ok || n == 0 {
		t.Fatalf("Stream() = %d, %v before Close, want samples", n, ok)
	}

	for i := range 2 {
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d = %v", i+1, err)
		}
		if len(closes) != 1 || closes[0] != io.Closer(d) {
			t.Errorf("after Close #%d the decoder was closed %d times, want once", i+1, len(closes))
		}
	}
	if n, ok := s.Stream(make([][2]float64, 512)); ok || n != 0 {
		t.Errorf("Stream() after Close = %d, %v; want nothing", n, ok)
	}
}
