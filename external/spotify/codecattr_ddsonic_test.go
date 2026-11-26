package spotify

// ddsonic: each stream an open gets is named, at Debug level, by a number,
// its codec and its format, and named again with the same number when it is
// discarded unaccepted. Nothing else of the stream is logged.

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	librespotPlayer "github.com/devgianlu/go-librespot/player"
	metadatapb "github.com/devgianlu/go-librespot/proto/spotify/metadata"

	"github.com/bjarneo/cliamp/applog"
)

type streamLine struct{ event, seq, codec, format string }

var streamLineRE = regexp.MustCompile(`spotify: stream (opened|discarded) seq=(\d+) codec=(\w+) format=(\w+)`)

// debugLog sends the log to a file at Debug level, and returns what reads
// back the stream lines written until then.
func debugLog(t *testing.T) func() []streamLine {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ddsonic.log")
	closeLog, err := applog.Init(path, applog.LevelDebug)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	return func() []streamLine {
		t.Helper()
		if err := closeLog(); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var out []streamLine
		for _, m := range streamLineRE.FindAllStringSubmatch(string(data), -1) {
			out = append(out, streamLine{m[1], m[2], m[3], m[4]})
		}
		return out
	}
}

func audioFile(f metadatapb.AudioFile_Format) *metadatapb.AudioFile {
	return &metadatapb.AudioFile{Format: &f}
}

// An opened stream is named by its file's format, and a stream with no file,
// no format or no stream at all is named unknown, without a panic.
func TestAStreamOpenNamesItsCodec(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stream        *librespotPlayer.Stream
		codec, format string
	}{
		{"vorbis", &librespotPlayer.Stream{File: audioFile(metadatapb.AudioFile_OGG_VORBIS_320)}, "vorbis", "OGG_VORBIS_320"},
		{"mp3", &librespotPlayer.Stream{File: audioFile(metadatapb.AudioFile_MP3_160)}, "mp3", "MP3_160"},
		{"flac", &librespotPlayer.Stream{File: audioFile(metadatapb.AudioFile_FLAC_FLAC)}, "flac", "FLAC_FLAC"},
		{"aac", &librespotPlayer.Stream{File: audioFile(metadatapb.AudioFile_AAC_24)}, "aac", "AAC_24"},
		{"unknown format", &librespotPlayer.Stream{File: audioFile(metadatapb.AudioFile_Format(9999))}, "unknown", "9999"},
		{"no format", &librespotPlayer.Stream{File: &metadatapb.AudioFile{}}, "unknown", "none"},
		{"no file", &librespotPlayer.Stream{}, "unknown", "none"},
		{"no stream", nil, "unknown", "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newPlayKit(t)
			k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) { return tc.stream, nil }
			lines := debugLog(t)
			sess := goodSession()
			o := within(t, "the open", openAsync(sess, context.Background()))
			if o.err != nil || o.stream != tc.stream {
				t.Fatalf("the open = %v, %v; want its stream", o.stream, o.err)
			}
			got := lines()
			if len(got) != 1 || got[0].event != "opened" || got[0].codec != tc.codec || got[0].format != tc.format {
				t.Errorf("logged %v; want one opened line, codec=%s format=%s", got, tc.codec, tc.format)
			}
			o.cancel()
			sess.Close()
		})
	}
}

// An open whose setup fails logs no stream, and returns its error as before;
// so does one whose caller gave up on it, which disposes of no stream.
func TestAFailedOpenLogsNoStream(t *testing.T) {
	for _, gaveUp := range []bool{false, true} {
		k := newPlayKit(t)
		failure := errors.New("no such track")
		k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) { return nil, failure }
		lines := debugLog(t)
		sess := goodSession()
		if gaveUp {
			reached, release := hookAt("worker-before-publish")
			ctx, cancel := context.WithCancel(context.Background())
			open := openAsync(sess, ctx)
			<-reached
			cancel()
			if o := within(t, "the open", open); !errors.Is(o.err, context.Canceled) {
				t.Errorf("the open given up = %v, want the cancellation", o.err)
			}
			release() // the worker disposes of what it got: no stream
		} else {
			o := within(t, "the open", openAsync(sess, context.Background()))
			var oe *originError
			if !errors.As(o.err, &oe) || oe.cause != failure || o.stream != nil {
				t.Errorf("the open = %v, %v; want the setup's error as an origin error", o.stream, o.err)
			}
		}
		sess.Close()
		tails.Wait()
		if got := lines(); len(got) != 0 {
			t.Errorf("a failed open (given up: %t) logged %v; want nothing", gaveUp, got)
		}
		openHook = nil
	}
}

// A stream discarded unaccepted is logged with its open's number and codec,
// whoever discards it; an accepted stream is not logged as discarded.
func TestADiscardedStreamCarriesItsOpensNumber(t *testing.T) {
	for _, how := range []string{"published, then cancelled", "published, then retired", "the caller gave up first", "accepted"} {
		t.Run(how, func(t *testing.T) {
			k := newPlayKit(t)
			k.open = func(*playback, context.Context, *http.Client) (*librespotPlayer.Stream, error) {
				return &librespotPlayer.Stream{Source: &closableSource{}, File: audioFile(metadatapb.AudioFile_MP3_320)}, nil
			}
			lines := debugLog(t)
			sess := goodSession()
			// An open accepted first, so the numbers are not all 1.
			first := within(t, "the first open", openAsync(sess, context.Background()))
			if first.err != nil {
				t.Fatal(first.err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch how {
			case "published, then cancelled":
				reached, release := hookAt("worker-published")
				open := openAsync(sess, ctx)
				<-reached
				cancel()
				time.Sleep(10 * time.Millisecond) // the caller's swap fails: it disposes
				release()
				within(t, "the open", open)
			case "published, then retired":
				reached, release := hookAt("caller-before-accept")
				open := openAsync(sess, ctx)
				<-reached
				sess.Close()
				release()
				within(t, "the open", open)
			case "the caller gave up first":
				reached, release := hookAt("worker-before-publish")
				open := openAsync(sess, ctx)
				<-reached
				cancel()
				within(t, "the open", open)
				release() // the worker disposes
			case "accepted":
				o := within(t, "the open", openAsync(sess, ctx))
				if o.err != nil {
					t.Fatal(o.err)
				}
				o.cancel()
			}
			first.cancel()
			sess.Close()
			tails.Wait() // every worker has returned
			got := lines()
			want := []streamLine{{"opened", "", "mp3", "MP3_320"}, {"opened", "", "mp3", "MP3_320"}}
			if how != "accepted" {
				want = append(want, streamLine{"discarded", "", "mp3", "MP3_320"})
			}
			if len(got) != len(want) {
				t.Fatalf("logged %v; want %d lines", got, len(want))
			}
			for i := range want {
				if got[i].event != want[i].event || got[i].codec != want[i].codec || got[i].format != want[i].format {
					t.Errorf("line %d = %v, want %v", i, got[i], want[i])
				}
			}
			if got[0].seq == got[1].seq {
				t.Errorf("two opens share the number %s", got[0].seq)
			}
			if how != "accepted" && got[2].seq != got[1].seq {
				t.Errorf("discarded seq=%s, want the second open's seq=%s", got[2].seq, got[1].seq)
			}
		})
	}
}
