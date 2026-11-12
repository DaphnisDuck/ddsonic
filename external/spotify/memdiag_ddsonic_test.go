package spotify

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/flac"
	"github.com/devgianlu/go-librespot/mp3"
	librespotPlayer "github.com/devgianlu/go-librespot/player"
	"github.com/devgianlu/go-librespot/vorbis"
	"github.com/gopxl/beep/v2"
	"golang.org/x/oauth2"

	"github.com/bjarneo/cliamp/internal/memdiag"
)

// The account and token a diagnostic test signs in with: distinctive, so the
// output can be searched for them.
const (
	diagUser    = "diaguser7f3a"
	diagData    = "diagdata51c0"
	diagDevice  = "diagdevice0e9"
	diagRefresh = "diagrefresha44"
	diagAccess  = "diagaccessc2d"
)

func startDiag(t *testing.T) (run string, stop func()) {
	t.Helper()
	stopRec, err := memdiag.Start(filepath.Join(t.TempDir(), "diag"))
	if err != nil {
		t.Fatal(err)
	}
	EnableMemDiag()
	stopAll := func() { DisableMemDiag(); stopRec() }
	t.Cleanup(stopAll)
	return memdiag.Dir(), stopAll
}

// diagProvider is a provider signed in with the distinctive account, whose
// track opens are kit's.
func diagProvider(t *testing.T) (*SpotifyProvider, *playKit) {
	t.Helper()
	storeCreds(t)
	if err := saveCreds(&storedCreds{Username: diagUser, Data: []byte(diagData), DeviceID: diagDevice, RefreshToken: diagRefresh}); err != nil {
		t.Fatal(err)
	}
	stubBuild(t)
	k := newPlayKit(t)
	fakeSpotifyHTTP(t, tokenOK, apiOK)
	sess := tokenSession(&oauth2.Token{AccessToken: diagAccess, Expiry: time.Now().Add(time.Hour)})
	retireAtEnd(t, sess)
	return New(sess, "client", 320), k
}

var diagFormats = map[string]string{"mp3": "MP3_320", "flac": "FLAC_FLAC", "vorbis": "OGG_VORBIS_320"}

// fixtureDecoder is librespot's real decoder of codec over its fixture.
func fixtureDecoder(t *testing.T, codec string) librespot.AudioSource {
	t.Helper()
	log := &librespot.NullLogger{}
	var src librespot.AudioSource
	var err error
	switch codec {
	case "mp3":
		src, err = mp3.New(log, fixture(t, "tone.mp3.bin"), 1)
	case "flac":
		src, err = flac.New(log, fixture(t, "tone.flac.bin"), 1)
	case "vorbis":
		src, err = vorbis.New(log, fixture(t, "tone.ogg.bin"), nil, 1)
	}
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// opensFixture makes each of kit's setups go through the SDK's diagnostic
// points as go-librespot's NewStream does (patch 0004) and return a real
// decoder of codec.
func opensFixture(t *testing.T, k *playKit, codec string) {
	k.open = func(_ *playback, ctx context.Context, _ *http.Client) (*librespotPlayer.Stream, error) {
		sdkDiag(ctx, librespotPlayer.DiagFormatSelected, diagFormats[codec])
		src := fixtureDecoder(t, codec)
		sdkDiag(ctx, librespotPlayer.DiagDecoderOpened, diagFormats[codec])
		return &librespotPlayer.Stream{Source: src}, nil
	}
}

// diagPull plays s until it ends (max 0 for no limit) or max pulls of audio, and
// reports whether it ended.
func diagPull(t *testing.T, s beep.StreamSeekCloser, max int) bool {
	t.Helper()
	buf := make([][2]float64, 512)
	deadline := time.Now().Add(10 * time.Second)
	for audio := 0; max == 0 || audio < max; {
		n, ok := s.Stream(buf)
		if !ok {
			return true
		}
		for _, x := range buf[:n] {
			if x != [2]float64{} {
				audio++
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the stream did not end")
		}
		runtime.Gosched()
	}
	return false
}

func wantCounters(t *testing.T, what string, want map[string]int64) {
	t.Helper()
	got := memdiag.Counters()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %d, want %d", what, k, got[k], v)
		}
	}
	for _, k := range []string{"prefetch_streams_open", "prefetch_streams_open_played", "prefetch_streams_open_not_yet_played", "prefetch_ring_bytes_open", "open_workers_active"} {
		if got[k] != 0 {
			t.Errorf("%s: gauge %s = %d after every stream closed, want 0", what, k, got[k])
		}
	}
	if t.Failed() {
		t.Logf("counters: %v", got)
	}
}

const ringBytes = 4 * spotifySampleRate * 16 // prefetchQueue of [2]float64

// A track played to its end, with each real decoder: what was selected,
// opened, accepted, prefetched, completed and closed, or for Vorbis not
// closed, is counted once.
func TestDiagCountsAPlayedTrack(t *testing.T) {
	for _, codec := range []string{"mp3", "flac", "vorbis"} {
		t.Run(codec, func(t *testing.T) {
			p, k := diagProvider(t)
			opensFixture(t, k, codec)
			startDiag(t)
			s, _, _, err := p.PlaybackStreamer(testTrackURI)
			if err != nil {
				t.Fatal(err)
			}
			if !diagPull(t, s, 0) {
				t.Fatal("not ended")
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			want := map[string]int64{
				"starts_attempted": 1, "starts_ok": 1, "open_attempts": 1,
				"sdk_format_selected." + codec: 1, "sdk_format_selected_by_format." + diagFormats[codec]: 1,
				"sdk_decoder_open_ok." + codec: 1, "open_setup_ok." + codec: 1, "open_accepted." + codec: 1,
				"prefetch_opens." + codec: 1, "prefetch_ring_bytes_allocated_total": ringBytes,
				"stream_completed." + codec: 1, "prefetch_close_completed." + codec: 1,
				"stream_closed_after_completed." + codec: 1, "stream_closed_while_playing." + codec: 0,
			}
			if codec == "vorbis" {
				want["vorbis_close_skipped.accepted"] = 1
			} else {
				want["decoder_close_requested.accepted."+codec] = 1
				want["decoder_close_completed.accepted."+codec] = 1
			}
			wantCounters(t, codec, want)
		})
	}
}

// A track closed while it plays (a hand skip), and one opened and closed
// before any of it was played (a preload dropped), are told apart.
func TestDiagCountsSkipsAndUnplayedStreams(t *testing.T) {
	p, k := diagProvider(t)
	opensFixture(t, k, "mp3")
	startDiag(t)
	skipped, _, _, err := p.PlaybackStreamer(testTrackURI)
	if err != nil {
		t.Fatal(err)
	}
	if diagPull(t, skipped, 1) {
		t.Fatal("the track ended before the skip")
	}
	unplayed, _, _, err := p.PlaybackStreamer(testTrackURI)
	if err != nil {
		t.Fatal(err)
	}
	if got := memdiag.Counters(); got["prefetch_streams_open_played"] != 1 || got["prefetch_streams_open_not_yet_played"] != 1 || got["prefetch_ring_bytes_open"] != 2*ringBytes {
		t.Errorf("with one playing and one opened ahead: %v", got)
	}
	_ = skipped.Close()
	_ = unplayed.Close()
	_ = unplayed.Close() // a second Close counts nothing
	wantCounters(t, "skip", map[string]int64{
		"starts_ok": 2, "open_accepted.mp3": 2, "prefetch_opens.mp3": 2,
		"stream_closed_while_playing.mp3": 1, "stream_closed_before_audio.mp3": 1, "stream_completed.mp3": 0,
		"prefetch_close_completed.mp3": 2, "decoder_close_completed.accepted.mp3": 2,
	})
}

// A stream set up and then not accepted is counted as discarded, and its
// decoder's release as releaseDecoder does it: closed, or for Vorbis not.
func TestDiagCountsDiscardedOpens(t *testing.T) {
	for _, codec := range []string{"mp3", "flac", "vorbis"} {
		t.Run(codec, func(t *testing.T) {
			startDiag(t)
			if err := notAccepted(t, "cancelled", fixtureDecoder(t, codec)); err == nil {
				t.Fatal("the open was accepted")
			}
			want := map[string]int64{"open_attempts": 1, "open_setup_ok." + codec: 1, "open_discarded_rejected_at_accept." + codec: 1, "open_accepted." + codec: 0}
			if codec == "vorbis" {
				want["vorbis_close_skipped.discarded"] = 1
			} else {
				want["decoder_close_requested.discarded."+codec] = 1
				want["decoder_close_completed.discarded."+codec] = 1
			}
			wantCounters(t, codec, want)
		})
	}
}

// Retired bundles and their tails are counted.
func TestDiagCountsRetiredBundles(t *testing.T) {
	newPlayKit(t)
	startDiag(t)
	s := goodSession()
	s.Close()
	tails.Wait()
	wantCounters(t, "retire", map[string]int64{"bundles_retired": 1, "bundles_tail_finished": 1, "bundle_tails_running": 0})
}

// Nothing the diagnostics write names the account, the token, the track or
// a file: the event log, the snapshots, the goroutine dumps and the heap
// profiles are searched for each.
func TestDiagWritesNoIdentifiers(t *testing.T) {
	p, k := diagProvider(t)
	opensFixture(t, k, "vorbis")
	run, stop := startDiag(t)
	s, _, _, err := p.PlaybackStreamer(testTrackURI)
	if err != nil {
		t.Fatal(err)
	}
	diagPull(t, s, 0)
	if err := memdiag.Snapshot("test"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	stop()

	secrets := []string{diagUser, diagData, diagDevice, diagRefresh, diagAccess, testTrackURI, testTrack.Base62(), testTrack.Hex(), "spotify:track", "tone.", ".bin"}
	ents, err := os.ReadDir(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) < 6 {
		t.Fatalf("only %d files written", len(ents))
	}
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(run, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(e.Name(), ".gz") {
			zr, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if data, err = io.ReadAll(zr); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range secrets {
			if bytes.Contains(bytes.ToLower(data), bytes.ToLower([]byte(s))) {
				t.Errorf("%s holds %q", e.Name(), s)
			}
		}
	}
	// The JSON lines hold no path: no string value is absolute or names the
	// home or temporary directory (the keys are fixed names).
	home, _ := os.UserHomeDir()
	for _, name := range []string{"events.jsonl", "snapshots.jsonl"} {
		data, _ := os.ReadFile(filepath.Join(run, name))
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
			var v any
			if err := json.Unmarshal(line, &v); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			walkStrings(v, func(s string) {
				if strings.HasPrefix(s, "/") || strings.Contains(s, os.TempDir()) || (home != "" && strings.Contains(s, home)) {
					t.Errorf("%s holds a path: %q", name, s)
				}
			})
		}
	}
}

func walkStrings(v any, f func(string)) {
	switch v := v.(type) {
	case string:
		f(v)
	case map[string]any:
		for _, x := range v {
			walkStrings(x, f)
		}
	case []any:
		for _, x := range v {
			walkStrings(x, f)
		}
	}
}

// go-librespot's NewStream reaches each diagnostic point where patch 0004
// says, and no line of it moved: the format's selection, each decoder's
// construction, and every failure between a decoder's construction and the
// stream's handoff.
func TestSDKDiagnosticPointsAreWired(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "third_party", "go-librespot", "player", "player.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	lines := strings.Split(src, "\n")
	at := func(n int, want string) {
		t.Helper()
		if n > len(lines) || !strings.Contains(lines[n-1], want) {
			t.Errorf("player.go:%d does not hold %q", n, want)
		}
	}
	at(832, `log.Debugf("selected format %s (%x)", diagSelected(ctx, file).Format.String(), file.FileId)`)
	at(910, `diagCtor[*vorbis.Decoder]{ctx, file}.done(vorbis.New(`)
	at(925, `diagCtor[*flac.Decoder]{ctx, file}.done(flac.New(`)
	at(941, `diagCtor[*mp3.Decoder]{ctx, file}.done(mp3.New(`)
	at(965, "streamHandedOff = true")
	for _, ctor := range []string{"vorbis.New(", "flac.New(", "mp3.New("} {
		if n := strings.Count(src, ctor); n != 1 {
			t.Errorf("%s appears %d times, want once", ctor, n)
		}
	}
	// Every error return from the first decoder construction to the
	// handoff goes through diagGap, except the one for a format with no
	// decoder.
	gap := strings.Join(lines[909:964], "\n")
	ret := regexp.MustCompile(`return nil, (.*)`)
	var wrapped int
	for _, m := range ret.FindAllStringSubmatch(gap, -1) {
		switch {
		case strings.HasPrefix(m[1], "diagGap(ctx, file, "):
			wrapped++
		case strings.Contains(m[1], "failed initializing"), strings.Contains(m[1], "unsupported audio format"):
		default:
			t.Errorf("an error return after a decoder's construction is not noted: %s", m[0])
		}
	}
	if wrapped != 7 {
		t.Errorf("%d returns go through diagGap, want 7 (sample rate and channels for each codec, and the seek)", wrapped)
	}
}
