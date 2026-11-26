package player

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/wav"

	"github.com/bjarneo/cliamp/internal/ytdlp/ytdlptest"
)

// #83 (S4): a seek while paused leaves frames from before the seek in the
// suspended device, on each of the three seek paths: a native decoder seek,
// an ffmpeg restart prepared outside the speaker lock, and a yt-dlp restart.
// The resume drops them, with the chain out of the mixer; a seek while
// playing and a plain pause and resume drop nothing.

// seekDevice records the output's device calls.
type seekDevice struct {
	drops       int
	mixerAtDrop []int // streamers in the mixer at each Drop
}

func useSeekDevice(t *testing.T) *seekDevice {
	t.Helper()
	d := &seekDevice{}
	oldSuspend, oldResume, oldDrop, oldDrain := speakerSuspend, speakerResume, speakerDrop, drainAfter
	speakerSuspend = func() error { return nil }
	speakerResume = func() error { return nil }
	speakerDrop = func() {
		speaker.Lock()
		d.mixerAtDrop = append(d.mixerAtDrop, speaker.mixer.Len())
		speaker.Unlock()
		d.drops++
	}
	drainAfter = func(time.Duration, func()) *time.Timer { return nil }
	speaker.Clear()
	t.Cleanup(func() {
		speaker.Clear()
		speakerSuspend, speakerResume, speakerDrop, drainAfter = oldSuspend, oldResume, oldDrop, oldDrain
	})
	return d
}

// seekPathTrack returns a playing track for the seek path kind.
func seekPathTrack(t *testing.T, kind string) *trackPipeline {
	t.Helper()
	switch kind {
	case "native":
		f, err := os.Open(writeFixture(t, "a.wav", 1, 3))
		if err != nil {
			t.Fatal(err)
		}
		d, format, err := wav.Decode(f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return &trackPipeline{decoder: d, stream: d, format: format, seekable: true}
	case "ffmpeg":
		dir := t.TempDir()
		writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nprintf '\\000\\100\\000\\300'\nexec sleep 30\n")
		writeExecutable(t, filepath.Join(dir, "ffprobe"), "#!/bin/sh\nprintf '10\\n'\n")
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		d, format, err := decodeFFmpegLocal(filepath.Join(dir, "track.m4a"), 44100, 16)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = d.Close() })
		return &trackPipeline{decoder: d, stream: d, format: format, seekable: true}
	case "ytdl":
		ytdlptest.InstallCopier(t, ytdlptest.Finish, strings.Repeat("\x00", 4096))
		installPassThroughFFmpeg(t)
		d := &ytdlPipeStreamer{
			state:     newPipeStreamState(44100),
			ytdlCmd:   exec.Command("true"),
			ffmpegCmd: exec.Command("true"),
			pipe:      io.NopCloser(bytes.NewReader(nil)),
		}
		return &trackPipeline{
			decoder: d, stream: d, path: loadURL, ytdlSeek: true, knownDuration: time.Hour,
			format: beep.Format{SampleRate: 44100, NumChannels: 2, Precision: 2},
		}
	}
	t.Fatalf("unknown seek path %q", kind)
	return nil
}

func TestR12SeekWhilePausedDropsOnResume(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell fixtures")
	}
	for _, kind := range []string{"native", "ffmpeg", "ytdl"} {
		for _, tt := range []struct {
			name      string
			pause     bool // pause before the seek and resume after it
			seek      bool
			wantDrops int
		}{
			{"seek while paused", true, true, 1},
			{"seek while playing", false, true, 0},
			{"pause and resume", true, false, 0},
		} {
			t.Run(kind+"/"+tt.name, func(t *testing.T) {
				d := useSeekDevice(t)
				p := newLoadPlayer()
				p.suspended = true
				tp := seekPathTrack(t, kind)
				if err := p.playPipelineForGeneration(tp, 0); err != nil {
					t.Fatalf("playPipelineForGeneration: %v", err)
				}
				t.Cleanup(p.Stop)

				if tt.pause {
					p.TogglePause()
				}
				if tt.seek {
					if err := p.Seek(time.Second); err != nil {
						t.Fatalf("Seek: %v", err)
					}
					p.mu.Lock()
					replaced := p.current != tp
					p.mu.Unlock()
					if kind == "ytdl" && !replaced {
						t.Fatal("the yt-dlp seek did not replace the track")
					}
				}
				if tt.pause {
					p.TogglePause()
				}
				if d.drops != tt.wantDrops {
					t.Errorf("%d drops, want %d", d.drops, tt.wantDrops)
				}
				for _, n := range d.mixerAtDrop {
					if n != 0 {
						t.Errorf("the chain was in the mixer at a drop (%d streamers)", n)
					}
				}
				speaker.Lock()
				n := speaker.mixer.Len()
				speaker.Unlock()
				if n != 1 {
					t.Errorf("after the resume the mixer has %d streamers, want the chain", n)
				}
			})
		}
	}
}
