package spotify

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	"github.com/devgianlu/go-librespot/mp3"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/vorbis"
)

func fixture(t *testing.T, name string) *bytes.Reader {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(data)
}

// countedMP3 and countedFLAC are librespot's real decoders, with their
// Close counted on its way through.
type countedMP3 struct {
	*mp3.Decoder
	closes atomic.Int32
}

func (d *countedMP3) Close() error { d.closes.Add(1); return d.Decoder.Close() }

type countedFLAC struct {
	*flac.Decoder
	closes atomic.Int32
}

func (d *countedFLAC) Close() error { d.closes.Add(1); return d.Decoder.Close() }

// realDecoder builds one of librespot's decoders over a synthetic file, as
// its player would for a stream, and says how often it has been closed.
func realDecoder(t *testing.T, format string) (source librespot.AudioSource, closes func() int32) {
	t.Helper()
	log := &librespot.NullLogger{}
	switch format {
	case "mp3":
		d, err := mp3.New(log, fixture(t, "tone.mp3.bin"), 1)
		if err != nil {
			t.Fatal(err)
		}
		c := &countedMP3{Decoder: d}
		return c, c.closes.Load
	case "flac":
		d, err := flac.New(log, fixture(t, "tone.flac.bin"), 1)
		if err != nil {
			t.Fatal(err)
		}
		c := &countedFLAC{Decoder: d}
		return c, c.closes.Load
	}
	t.Fatalf("no decoder for %q", format)
	return nil, nil
}

// notAccepted opens a track whose setup returns source, and ends the open
// before the stream is accepted: by the caller's cancellation, or by the
// retirement of the session's playback. It returns what the open returned.
func notAccepted(t *testing.T, how string, source librespot.AudioSource) error {
	t.Helper()
	k := newPlayKit(t)
	k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
		return &librespotPlayer.Stream{Source: source}, nil
	}
	s := goodSession()
	t.Cleanup(s.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reached, release := hookAt("caller-before-accept")
	open := openAsync(s, ctx)
	<-reached
	if how == "cancelled" {
		cancel()
	} else {
		s.Close()
	}
	release()
	o := within(t, "the open", open)
	if o.stream != nil {
		t.Fatalf("the open returned a stream")
	}
	builds.Wait()
	tails.Wait()
	return o.err
}

// A stream that is never accepted has its decoder closed, once, with
// librespot's real MP3 and FLAC decoders: built by their own constructors
// over real (synthetic) audio, never read, and closed through the open's own
// disposal, however the open ended, time after time. The process survives
// it, which a stand-in cannot show.
func TestRealDecodersAreClosedWhenAnOpenIsNotAccepted(t *testing.T) {
	for _, format := range []string{"mp3", "flac"} {
		for _, how := range []string{"cancelled", "retired"} {
			t.Run(format+", "+how, func(t *testing.T) {
				want := map[string]error{"cancelled": context.Canceled, "retired": errSessionChanged}[how]
				for i := range 25 {
					source, closes := realDecoder(t, format)
					if err := notAccepted(t, how, source); !errors.Is(err, want) {
						t.Fatalf("open #%d = %v, want %v", i+1, err, want)
					}
					if n := closes(); n != 1 {
						t.Fatalf("open #%d: the decoder was closed %d times, want once", i+1, n)
					}
				}
			})
		}
	}
}

// A real Ogg Vorbis decoder that is never accepted is NOT closed (review
// round 9, R9-4; accepted by the owner on 2026-10-13 as a 1.0 limitation),
// because go-librespot v0.9.1's Close of it can
// abort the process (releaseDecoder says why). This test holds that:
// the open's disposal, repeated with real decoders, does not bring the
// process down. It does not show that the decoder's memory is given back;
// it is not, and the test is no proof of release. It changes when the SDK's
// Close is safe to call.
func TestRealVorbisDecoderIsNotClosedByDisposal(t *testing.T) {
	for _, how := range []string{"cancelled", "retired"} {
		t.Run(how, func(t *testing.T) {
			for range 100 {
				d, err := vorbis.New(&librespot.NullLogger{}, fixture(t, "tone.ogg.bin"), nil, 1)
				if err != nil {
					t.Fatal(err)
				}
				notAccepted(t, how, d)
			}
		})
	}
}

// Which decoders the disposal closes, by what librespot's player hands out.
func TestReleaseDecoderByKind(t *testing.T) {
	mp3Source, mp3Closes := realDecoder(t, "mp3")
	releaseDecoder(mp3Source)
	flacSource, flacCloses := realDecoder(t, "flac")
	releaseDecoder(flacSource)
	if mp3Closes() != 1 || flacCloses() != 1 {
		t.Errorf("closes: mp3 %d, flac %d; want 1 and 1", mp3Closes(), flacCloses())
	}
	plain := &plainCloser{}
	releaseDecoder(plain)
	if plain.closes.Load() != 1 {
		t.Errorf("a source with Close() was closed %d times, want once", plain.closes.Load())
	}
	releaseDecoder(&fakeSource{}) // nothing to close: no panic
}
