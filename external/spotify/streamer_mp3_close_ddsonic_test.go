package spotify

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime/cgo"
	"sync"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/mp3"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
)

// mp3Native is what an MP3 decoder holds outside Go's reach: the cgo.Handle
// its reader callbacks use, the C cell that carries it, and the mpg123
// handle. They are unexported, so they are read by reflection.
func mp3Native(d *mp3.Decoder) (handle cgo.Handle, cellSet, mpg123Set bool) {
	v := reflect.ValueOf(d).Elem()
	return cgo.Handle(v.FieldByName("handle").Uint()), !v.FieldByName("handlePtr").IsNil(), !v.FieldByName("mh").IsNil()
}

// resolves reports whether h still names a live value.
func resolves(h cgo.Handle) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	_ = h.Value()
	return true
}

func newMP3(t *testing.T, input librespot.SizedReadAtSeeker) *mp3.Decoder {
	t.Helper()
	d, err := mp3.New(&librespot.NullLogger{}, input, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() }) // a no-op once the streamer has closed it
	return d
}

// An accepted MP3 stream's decoder is released when the streamer is closed:
// its cgo.Handle no longer resolves and its native handles are gone. A second
// Close changes nothing (RC4-S3).
func TestStreamerCloseReleasesAcceptedMP3Decoder(t *testing.T) {
	d := newMP3(t, fixture(t, "tone.mp3.bin"))
	s := newSpotifyStreamer(&librespotPlayer.Stream{Source: d}, nil)
	if n, ok := s.Stream(make([][2]float64, 512)); !ok || n == 0 {
		t.Fatalf("Stream() = %d, %v before Close, want samples", n, ok)
	}
	h, cell, mh := mp3Native(d)
	if !resolves(h) || !cell || !mh {
		t.Fatalf("before Close: handle resolves = %v, cell = %v, mpg123 = %v; want all set", resolves(h), cell, mh)
	}

	for i := range 2 {
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d = %v", i+1, err)
		}
		after, cell, mh := mp3Native(d)
		if resolves(h) || after != 0 || cell || mh {
			t.Errorf("after Close #%d: old handle resolves = %v, handle = %d, cell = %v, mpg123 = %v; want all released",
				i+1, resolves(h), after, cell, mh)
		}
	}
}

// gatedInput is an MP3 file that, once armed, holds the decoder's next read
// or seek of it until released.
type gatedInput struct {
	*bytes.Reader
	mu       sync.Mutex
	entered  chan struct{}
	released chan struct{}
}

func (g *gatedInput) arm() (entered chan struct{}, release func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entered, g.released = make(chan struct{}), make(chan struct{})
	return g.entered, sync.OnceFunc(func() { close(g.released) })
}

func (g *gatedInput) gate() {
	g.mu.Lock()
	entered, released := g.entered, g.released
	g.entered = nil
	g.mu.Unlock()
	if entered != nil {
		close(entered)
		<-released
	}
}

func (g *gatedInput) Read(p []byte) (int, error) { g.gate(); return g.Reader.Read(p) }

func (g *gatedInput) Seek(off int64, whence int) (int64, error) {
	g.gate()
	return g.Reader.Seek(off, whence)
}

// A Close that arrives while a Stream or a Seek is inside the MP3 decoder
// waits for it: the decoder is released only once that use has left the
// streamer's lock, never under it (RC4-S3).
func TestStreamerClosesMP3DecoderOnlyAfterItsUseEnds(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "tone.mp3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"Stream", "Seek"} {
		t.Run(op, func(t *testing.T) {
			in := &gatedInput{Reader: bytes.NewReader(data)}
			d := newMP3(t, in)
			s := newSpotifyStreamer(&librespotPlayer.Stream{Source: d}, nil)
			h, _, _ := mp3Native(d)
			if op == "Seek" {
				// Read to the end first, so that seeking back to the start
				// has to seek the input.
				for {
					if _, ok := s.Stream(make([][2]float64, 4096)); !ok {
						break
					}
				}
			}

			entered, release := in.arm()
			t.Cleanup(release)
			used := make(chan struct{})
			go func() {
				defer close(used)
				if op == "Stream" {
					// Until the end, so the decoder reads its input.
					for {
						if _, ok := s.Stream(make([][2]float64, 4096)); !ok {
							return
						}
					}
				}
				_ = s.Seek(0)
			}()
			select {
			case <-entered:
			case <-used:
				t.Fatalf("%s finished without reaching the decoder's input", op)
			case <-time.After(10 * time.Second):
				t.Fatalf("%s never reached the decoder's input", op)
			}

			closed := make(chan error, 1)
			go func() { closed <- s.Close() }()
			deadline := time.Now().Add(10 * time.Second)
			for !s.closing.Load() {
				if time.Now().After(deadline) {
					t.Fatal("Close never began")
				}
				time.Sleep(time.Millisecond)
			}
			time.Sleep(50 * time.Millisecond) // room for a Close that does not wait to go wrong
			select {
			case err := <-closed:
				t.Fatalf("Close returned (%v) while %s was inside the decoder", err, op)
			default:
			}
			if !resolves(h) {
				t.Fatalf("the decoder was released while %s was inside it", op)
			}

			release()
			<-used
			if err := <-closed; err != nil {
				t.Fatalf("Close = %v", err)
			}
			if resolves(h) {
				t.Error("the decoder was not released once its use ended")
			}
		})
	}
}
