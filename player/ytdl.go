package player

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"

	"github.com/bjarneo/cliamp/applog"
	"github.com/bjarneo/cliamp/internal/ytdlcookies"
	"github.com/bjarneo/cliamp/internal/ytdlp"
)

// pipeBufSize is the buffer size for audio pipe readers (yt-dlp, ffmpeg).
const pipeBufSize = 64 * 1024

// ytdlPipeTimeout limits how long we wait for yt-dlp to produce initial audio.
const ytdlPipeTimeout = 30 * time.Second

// ytdlCauseGrace bounds how long buildYTDLPipeline waits, after the audio pipe
// closes with no data, for yt-dlp or ffmpeg to report why. yt-dlp typically
// exits quickly with a stderr message (bot wall, 404, DRM); this only matters
// when the process is slow to flush and exit.
const ytdlCauseGrace = 3 * time.Second

const ytdlPipelineMaxAttempts = 3

// YTDLPAvailable reports whether yt-dlp is installed and on PATH.
func YTDLPAvailable() bool {
	_, err := exec.LookPath("yt-dlp")
	return err == nil
}

func appendYTDLCookieArgs(args []string, pageURL string) []string {
	if browser := ytdlcookies.ForURL(pageURL); browser != "" {
		return append(args, "--cookies-from-browser", browser)
	}
	return args
}

// probeYTDLDuration runs a quick yt-dlp --print duration to obtain
// the track duration when --flat-playlist didn't provide it.
func probeYTDLDuration(ctx context.Context, pageURL string) time.Duration { // ddsonic: ctx, the load the probe is for
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"--skip-download", "--no-playlist", "--socket-timeout", "10", "--print", "duration"}
	args = appendYTDLCookieArgs(args, pageURL)
	// "--" stops yt-dlp parsing pageURL as a flag. Callers gate on
	// playlist.IsURL, but keep the terminator so a future caller cannot turn
	// a crafted URL into --exec and reach arbitrary command execution.
	args = append(args, "--", pageURL)
	cmd, release, err := ytdlp.Command(ctx, args...) // ddsonic: no ambient configuration, no cookie copy left behind
	if err != nil {
		applog.UserError("%v", err)
		return 0
	}
	defer func() {
		if err := release(); err != nil {
			applog.UserError("%v", err)
		}
	}()
	// WaitDelay ensures cmd.Output() doesn't hang indefinitely if the
	// process is killed but I/O pipe goroutines haven't drained. Without
	// this, a zombie yt-dlp child keeping stdout open can block Output()
	// forever, which in turn blocks PlayYTDL and leaves the UI stuck at
	// "Buffering...".
	cmd.WaitDelay = 3 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

// ddsonic: no InstallYTDLP. ddsonic never installs yt-dlp; it says how
// (YtdlpInstallHint, #82).

// YtdlpInstallHint returns a platform-specific install command suggestion.
func YtdlpInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install yt-dlp"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install yt-dlp"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S yt-dlp"
		}
		return "pip install yt-dlp"
	case "windows":
		return "winget install yt-dlp"
	default:
		return "pip install yt-dlp"
	}
}

// ffmpegInstallHint returns a platform-specific install command suggestion.
func ffmpegInstallHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "brew install ffmpeg"
	case "linux":
		if _, err := exec.LookPath("apt-get"); err == nil {
			return "sudo apt install ffmpeg"
		}
		if _, err := exec.LookPath("pacman"); err == nil {
			return "sudo pacman -S ffmpeg"
		}
		return "see https://ffmpeg.org/download.html"
	case "windows":
		return "winget install ffmpeg"
	default:
		return "see https://ffmpeg.org/download.html"
	}
}

// ytdlPipeStreamer streams PCM audio from a yt-dlp | ffmpeg pipe chain.
// yt-dlp downloads the best audio and writes raw data to stdout; ffmpeg reads
// that via a pipe and converts it to PCM on its stdout, which we consume.
type ytdlPipeStreamer struct {
	ytdlCmd    *exec.Cmd
	ffmpegCmd  *exec.Cmd
	pipe       io.ReadCloser // ffmpeg stdout (PCM output)
	reader     *bufio.Reader // buffered reader over pipe
	ytdlErr    <-chan error  // yt-dlp exit error from monitoring goroutine
	ffmpegErr  <-chan error  // ffmpeg exit error from monitoring goroutine
	ytdlDone   <-chan struct{}
	ffmpegDone <-chan struct{}
	pcmBuf     []byte
	state      *pipeStreamState
	f32        bool // true = f32le, false = s16le
	closeOnce  sync.Once
	released   *ytdlRelease // ddsonic: how removing yt-dlp's temporary directory went
}

func (y *ytdlPipeStreamer) Stream(samples [][2]float64) (int, bool) {
	n, ok := streamFromReader(y.reader, samples, &y.pcmBuf, y.f32, y.state)
	y.state.pos.Add(int64(n))
	// On EOF with no frames read, surface why the pipe closed (yt-dlp bot
	// wall, 404, DRM, or undecodable ffmpeg input) instead of a bare EOF.
	if !ok && n == 0 && y.state.err.load() == nil {
		if cause := y.waitCause(0); cause != nil {
			y.state.err.publish(cause)
		}
	}
	return n, ok
}

// waitCause reports why the audio pipe closed without producing audio,
// preferring yt-dlp's reason (bot wall, 404, DRM, region block) over ffmpeg's
// (undecodable input). With d <= 0 it polls without blocking; otherwise it
// waits up to d for a process to report. Returns nil if neither reported an
// error, leaving the caller to surface the bare EOF.
func (y *ytdlPipeStreamer) waitCause(d time.Duration) error {
	if d <= 0 {
		select {
		case e := <-y.ytdlErr:
			if e != nil {
				return e
			}
		default:
		}
		select {
		case e := <-y.ffmpegErr:
			return e
		default:
			return nil
		}
	}
	deadline := time.After(d)
	ytdlDone, ffmpegDone := false, false
	var ffErr error
	for !ytdlDone || !ffmpegDone {
		select {
		case e := <-y.ytdlErr:
			ytdlDone = true
			if e != nil {
				return e
			}
		case e := <-y.ffmpegErr:
			ffmpegDone = true
			ffErr = e
		case <-deadline:
			return ffErr
		}
	}
	return ffErr
}

func (y *ytdlPipeStreamer) Err() error {
	if y.state == nil {
		return nil
	}
	return y.state.err.load()
}
func (y *ytdlPipeStreamer) Len() int { return 0 }
func (y *ytdlPipeStreamer) Position() int {
	if y.state == nil {
		return 0
	}
	return int(y.state.pos.Load())
}
func (y *ytdlPipeStreamer) Seek(int) error { return nil }

// interrupt releases a blocked PCM read without waiting for yt-dlp or ffmpeg.
// A stalled download otherwise holds Stream, and the speaker lock with it,
// until yt-dlp gets data or gives up. Close reaps both processes later.
func (y *ytdlPipeStreamer) interrupt() {
	_ = y.pipe.Close()
	if y.ytdlCmd.Process != nil {
		_ = y.ytdlCmd.Process.Kill()
	}
	if y.ffmpegCmd.Process != nil {
		_ = y.ffmpegCmd.Process.Kill()
	}
}

func (y *ytdlPipeStreamer) Close() error {
	y.closeOnce.Do(func() {
		// Kill both processes to stop downloading/decoding.
		if y.ytdlCmd.Process != nil {
			y.ytdlCmd.Process.Kill()
		}
		if y.ffmpegCmd.Process != nil {
			y.ffmpegCmd.Process.Kill()
		}
		y.pipe.Close()
		// The monitor goroutines own Wait. Killing both children and waiting
		// for their done signals guarantees Close does not leave zombies.
		if y.ytdlDone != nil {
			<-y.ytdlDone
		}
		if y.ffmpegDone != nil {
			<-y.ffmpegDone
		}
	})
	return y.released.result() // ddsonic
}

// monitorExit waits for cmd to exit and reports the result on a buffered
// channel: a wrapped error preferring captured stderr over the bare exit code
// on failure, or nil on clean exit. The channel is buffered so the goroutine
// always completes even with no receiver (e.g. after Close kills the process).
func monitorExit(cmd *exec.Cmd, stderr *limitedBuffer, name string) (<-chan error, <-chan struct{}) {
	ch := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := cmd.Wait()
		switch trimmed := strings.TrimSpace(stderr.String()); {
		case err == nil:
			ch <- nil
		case trimmed != "":
			ch <- fmt.Errorf("%s: %w: %s", name, err, trimmed)
		default:
			ch <- fmt.Errorf("%s: %w", name, err)
		}
	}()
	return ch, done
}

// decodeYTDLPipe starts a yt-dlp | ffmpeg pipe chain for the given page URL
// and returns a streaming PCM decoder. If startSec > 0, ffmpeg -ss is used
// to skip to the desired position in the input stream.
func decodeYTDLPipe(ctx context.Context, pageURL string, sr beep.SampleRate, bitDepth, startSec int) (*ytdlPipeStreamer, beep.Format, error) { // ddsonic: ctx
	if _, err := exec.LookPath("yt-dlp"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("yt-dlp is required — install: %s", YtdlpInstallHint())
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, beep.Format{}, fmt.Errorf("ffmpeg is required — install: %s", ffmpegInstallHint())
	}

	// os.Pipe connects yt-dlp stdout → ffmpeg stdin.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, beep.Format{}, fmt.Errorf("os.Pipe: %w", err)
	}

	// Start yt-dlp: download best audio to stdout.
	// Prefer direct HTTPS/HTTP streams over HLS (m3u8). HLS requires segment
	// downloading and muxing which doesn't pipe cleanly to stdout.
	// Live streams (e.g. YouTube live) expose no audio-only formats at all,
	// only muxed video+audio over HLS, so fall back to "best" as a last
	// resort; the ffmpeg stage below outputs PCM audio and drops the video.
	ytdlArgs := []string{
		"-f", "bestaudio[protocol=https]/bestaudio[protocol=http]/bestaudio[protocol!=m3u8_native][protocol!=m3u8]/bestaudio/best",
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		"--socket-timeout", "15",
		"-o", "-",
	}
	ytdlArgs = appendYTDLCookieArgs(ytdlArgs, pageURL)
	ytdlArgs = append(ytdlArgs, "--", pageURL)
	// ddsonic: no ambient configuration, no cookie copy left behind. Playback
	// stops yt-dlp by killing it, at once; releaseYTDL then removes what the
	// run left.
	ytdlCmd, releaseYTDL, err := ytdlp.Command(ctx, ytdlArgs...)
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, beep.Format{}, err
	}
	ytdlCmd.Stdout = pw
	var ytdlStderr limitedBuffer
	ytdlCmd.Stderr = &ytdlStderr
	if err := ytdlCmd.Start(); err != nil {
		reportYTDLRelease(releaseYTDL()) // ddsonic
		pr.Close()
		pw.Close()
		return nil, beep.Format{}, fmt.Errorf("yt-dlp start: %w", err)
	}

	// Start ffmpeg: read from pipe, output PCM to stdout.
	// If startSec > 0, use -ss to seek into the input stream.
	pcmFmt, codec, precision := ffmpegPCMArgs(bitDepth)
	var ffmpegArgs []string
	if startSec > 0 {
		ffmpegArgs = append(ffmpegArgs, "-ss", strconv.Itoa(startSec))
	}
	ffmpegArgs = append(ffmpegArgs,
		"-i", "pipe:0",
		"-f", pcmFmt,
		"-acodec", codec,
		"-ar", strconv.Itoa(int(sr)),
		"-ac", "2",
		"-loglevel", "error",
		"pipe:1",
	)
	ffmpegCmd := exec.Command("ffmpeg", ffmpegArgs...)
	ffmpegCmd.Stdin = pr
	var ffmpegStderr limitedBuffer
	ffmpegCmd.Stderr = &ffmpegStderr
	// An owned pipe carries ffmpeg's PCM, like the yt-dlp → ffmpeg hop above.
	// StdoutPipe would tie the read end to cmd.Wait, which monitorExit runs
	// during playback: Wait closes it as soon as ffmpeg exits, dropping PCM
	// still in the pipe and failing the next read instead of reaching EOF.
	pcmR, pcmW, err := os.Pipe()
	if err != nil {
		pw.Close()
		pr.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		reportYTDLRelease(releaseYTDL()) // ddsonic
		return nil, beep.Format{}, fmt.Errorf("os.Pipe: %w", err)
	}
	ffmpegCmd.Stdout = pcmW
	if err := ffmpegCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		pcmR.Close()
		pcmW.Close()
		ytdlCmd.Process.Kill()
		ytdlCmd.Wait()
		reportYTDLRelease(releaseYTDL()) // ddsonic
		return nil, beep.Format{}, fmt.Errorf("ffmpeg start: %w", err)
	}

	// Close parent's copies of pipe ends. yt-dlp owns pw (write end), ffmpeg
	// owns pr (read end) and pcmW (PCM write end). If the parent keeps these
	// open, EOF won't propagate when the owning process exits.
	pw.Close()
	pr.Close()
	pcmW.Close()

	// Monitor each process's exit so we can surface why the pipe closed. A
	// process's stderr is only safe to read after Wait() returns, so the
	// capture happens inside monitorExit.
	ytdlErrCh, ytdlDone := monitorExit(ytdlCmd, &ytdlStderr, "yt-dlp")
	released := &ytdlRelease{} // ddsonic: Close waits for the cleanup too, and reports it
	ytdlDone = afterExit(ytdlDone, func() { released.err = reportYTDLRelease(releaseYTDL()) })
	ffmpegErrCh, ffmpegDone := monitorExit(ffmpegCmd, &ffmpegStderr, "ffmpeg")

	format := beep.Format{
		SampleRate:  sr,
		NumChannels: 2,
		Precision:   precision,
	}

	return &ytdlPipeStreamer{
		ytdlCmd:    ytdlCmd,
		ffmpegCmd:  ffmpegCmd,
		pipe:       pcmR,
		reader:     bufio.NewReaderSize(pcmR, pipeBufSize),
		ytdlErr:    ytdlErrCh,
		ffmpegErr:  ffmpegErrCh,
		ytdlDone:   ytdlDone,
		ffmpegDone: ffmpegDone,
		state:      newPipeStreamState(0),
		f32:        bitDepth == 32,
		released:   released,
	}, format, nil
}

// buildYTDLPipeline creates a trackPipeline for a yt-dlp URL.
// If startSec > 0, playback begins at that offset (seek-by-restart).
//
// ddsonic: load is the load this is for. Cancelling it stops the wait for the
// first audio, and yt-dlp and ffmpeg with it.
func (p *Player) buildYTDLPipeline(load *ytdlLoad, pageURL string, startSec int) (*trackPipeline, error) {
	p.streamTitle.Store("")

	for attempt := 1; ; attempt++ {
		if load.unwanted() {
			return nil, load.err(nil)
		}
		decoder, format, err := decodeYTDLPipe(load.ctx, pageURL, p.sr, p.bitDepth, startSec)
		if err != nil {
			return nil, load.err(err)
		}

		if err := prefillYTDLPipe(load.ctx, decoder); err != nil {
			if load.unwanted() || attempt == ytdlPipelineMaxAttempts || !isTransientYTDL403(err) {
				return nil, load.err(err)
			}
			continue
		}

		// ddsonic: upstream 70e5f79e. The prefetch reads the pipe away from the
		// speaker callback, so a stalled download plays silence instead of
		// holding the speaker lock, which froze every control.
		return p.prefetchNetworkPipeline(&trackPipeline{
			decoder:      decoder,
			stream:       decoder,
			format:       format,
			seekable:     false,
			path:         pageURL,
			ytdlSeek:     true,
			streamOffset: time.Duration(startSec) * time.Second,
			load:         load,
		}, true), nil
	}
}

func prefillYTDLPipe(ctx context.Context, decoder *ytdlPipeStreamer) error { // ddsonic: ctx
	// Pre-fill: block until yt-dlp + ffmpeg produce initial audio data.
	// This runs in a tea.Cmd goroutine (not the UI thread), ensuring the
	// speaker goroutine won't block on an empty pipe and hold its lock
	// (which would freeze the UI). A 30s timeout prevents hanging when
	// yt-dlp is slow to produce output.
	peekErr := make(chan error, 1)
	go func() {
		_, err := decoder.reader.Peek(1)
		peekErr <- err
	}()
	select {
	case err := <-peekErr:
		if err != nil {
			// The audio pipe closed before producing a byte. Prefer the real
			// cause from yt-dlp (e.g. "Sign in to confirm you're not a bot",
			// "HTTP Error 404", DRM, region block) or ffmpeg over the opaque
			// EOF — the pipe only tells us the upstream closed, not why.
			cause := decoder.waitCause(ytdlCauseGrace)
			decoder.Close()
			if cause != nil {
				return cause
			}
			return fmt.Errorf("waiting for audio data: %w", err)
		}
	case <-ctx.Done():
		// ddsonic: the track is no longer wanted. Close kills and reaps both
		// processes and removes yt-dlp's temporary directory, here on the
		// load's goroutine, not on that of whoever cancelled.
		decoder.Close()
		<-peekErr
		return context.Cause(ctx)
	case <-time.After(ytdlPipeTimeout):
		decoder.Close()
		<-peekErr // drain goroutine after Close() unblocks the pipe
		return fmt.Errorf("timed out waiting for audio data (%v)", ytdlPipeTimeout)
	}
	return nil
}

func isTransientYTDL403(err error) bool {
	message := err.Error()
	return strings.Contains(message, "yt-dlp:") && strings.Contains(message, "HTTP Error 403: Forbidden")
}
