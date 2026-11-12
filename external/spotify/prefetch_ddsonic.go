package spotify

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
)

// The player's Spotify stream is decoded away from the speaker. Beep calls
// Stream while it holds its global speaker lock, the lock that pausing,
// seeking, stopping and the position on screen take; a decoder read that
// waits for a chunk from the network held that lock, and froze them all for
// as long as the network was gone. The decoder is now read by one goroutine
// into a queue of a few seconds, and Stream only takes from the queue: when
// it is empty, the speaker gets silence. This follows the player's own
// livePrefetchStreamer (player/live_prefetch.go), which does the same for
// YouTube and radio, with what a Spotify stream has besides: seeking.

const (
	prefetchQueue  = 4 * time.Second        // decoded ahead of the speaker
	prefetchResume = 500 * time.Millisecond // queued before playing again after silence
	prefetchFade   = 64                     // samples faded in or out at silence
	prefetchChunk  = 4096                   // pairs per decoder read
)

// PlaybackStreamer is NewStreamer for the player: the stream it plays.
func (p *SpotifyProvider) PlaybackStreamer(uri string) (beep.StreamSeekCloser, beep.Format, time.Duration, error) {
	noteStartEntry() // #168 diagnostics, a no-op when off
	s, format, dur, err := p.NewStreamer(uri)
	noteStart(err)
	if err != nil {
		return nil, format, dur, err
	}
	if ss, ok := s.(*spotifyStreamer); ok {
		return newPlaybackStreamer(ss), format, dur, nil
	}
	return s, format, dur, nil
}

// prefetchedStream is a spotifyStreamer decoded ahead of the speaker.
//
// Its fill goroutine is the only caller of the streamer's reads and seeks
// until Close, so a read that waits for the network holds nothing but the
// streamer's own lock, which no caller of the speaker's takes. Stream,
// Position, Len, Err and Seek take only mu, never for longer than a copy.
//
// A seek changes the generation at once: the queue is dropped, the position
// is the target, and what a read already under way brings back is dropped
// when it arrives, as is its end or error. The fill goroutine then seeks the
// decoder to the latest target; the speaker gets silence until the audio
// from there is queued. A seek that waits for the network waits there, not
// in Seek. Position counts the samples handed to the speaker.
type prefetchedStream struct {
	src *spotifyStreamer

	mu   sync.Mutex
	cond *sync.Cond // signalled when the queue, a seek or closed changes

	buf      [][2]float64 // ring; r is the next sample to play, w the next to fill
	r, w     int
	full     bool
	resumeAt int

	gen      uint64 // moved by every seek
	seekTo   int    // the latest target, when seekDue
	seekDue  bool
	base     int // the position the queue starts at: the last target, or 0
	consumed int // samples played since base

	ended   bool  // the decoder ended in this generation: no more to queue
	endErr  error // why, when it was not a clean end
	err     error // the stream's error, once the speaker has had all before it
	closed  bool
	silence bool // waiting for prefetchResume to be queued before playing
	faded   bool // the last pull ended in silence: fade the next audio in

	fillDone chan struct{}

	diag *streamDiag // #168 diagnostics; nil when off
}

func newPlaybackStreamer(s *spotifyStreamer) beep.StreamSeekCloser {
	sr := beep.SampleRate(spotifySampleRate)
	capacity := sr.N(prefetchQueue)
	p := &prefetchedStream{
		src:      s,
		buf:      make([][2]float64, capacity),
		resumeAt: min(sr.N(prefetchResume), capacity),
		silence:  true,
		fillDone: make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	p.diag = newStreamDiag(s, int64(capacity)*16) // 16 bytes a [2]float64
	go p.fill()
	return p
}

// errFillClosed is what the decoder's read returns once the stream is closing.
var errFillClosed = errors.New("spotify: stream closed")

// read reads up to len(samples) pairs from the decoder, as Stream does, and
// returns the read's own error: io.EOF at the end, errFillClosed once the
// streamer is closing. Stream keeps the first error for Err; a later read's
// error is not that one.
func (s *spotifyStreamer) read(samples [][2]float64) (int, error) {
	if s.closing.Load() {
		return 0, errFillClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing.Load() || s.source == nil {
		return 0, errFillClosed
	}
	needed := len(samples) * spotifyChannels
	if len(s.buf) < needed {
		s.buf = make([]float32, needed)
	}
	nRead, err := s.source.Read(s.buf[:needed])
	if err != nil && err != io.EOF {
		if s.closing.Load() {
			return 0, errFillClosed
		}
		return 0, err
	}
	nRead -= nRead % spotifyChannels
	pairs := nRead / spotifyChannels
	for i := range pairs {
		samples[i][0] = float64(s.buf[i*2])
		samples[i][1] = float64(s.buf[i*2+1])
	}
	return pairs, err
}

// fill decodes into the queue until the stream is closed. It waits while the
// queue is full, and after the decoder's end until a seek or Close.
func (p *prefetchedStream) fill() {
	defer close(p.fillDone)
	chunk := make([][2]float64, prefetchChunk)
	var at uint64 // the generation the decoder is positioned for
	for {
		p.mu.Lock()
		for !p.closed && !p.seekDue && p.ended {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return
		}
		seek, target := p.seekDue, p.seekTo
		gen := p.gen
		p.seekDue = false
		p.mu.Unlock()

		if seek {
			at = gen
			if err := p.src.Seek(target); err != nil {
				if p.src.closing.Load() {
					return // Close: its error is no one's
				}
				// Refused, whatever the error wraps: the decoder stays where
				// it was (beep's StreamSeeker.Seek), and the stream plays on
				// from there. Where that is, the decoder says, asked here and
				// not under mu.
				p.rebase(at, p.src.Position())
			}
			continue
		}

		n, err := p.src.read(chunk)
		if err == errFillClosed {
			return
		}
		if n > 0 && !p.queue(chunk[:n], at) {
			return
		}
		switch {
		case err == io.EOF, err == nil && n < len(chunk):
			p.end(at, nil) // a short read ends the track, as it did at the speaker
		case err != nil:
			p.end(at, err)
		}
	}
}

// queue adds samples decoded for generation gen, waiting for room. It drops
// them if a seek has moved the generation, and reports false once closed.
func (p *prefetchedStream) queue(samples [][2]float64, gen uint64) bool {
	for len(samples) > 0 {
		p.mu.Lock()
		for !p.closed && p.gen == gen && p.roomLocked() == 0 {
			p.cond.Wait()
		}
		if p.closed {
			p.mu.Unlock()
			return false
		}
		if p.gen != gen {
			p.mu.Unlock()
			return true // the seek's: dropped
		}
		n := min(len(samples), p.roomLocked())
		for i := range n {
			p.buf[p.w] = samples[i]
			p.w = (p.w + 1) % len(p.buf)
		}
		if n > 0 && p.w == p.r {
			p.full = true
		}
		samples = samples[n:]
		p.cond.Broadcast()
		p.mu.Unlock()
	}
	return true
}

// rebase makes pos the position generation gen plays from, after the decoder
// refused gen's seek. A newer seek, or Close, has the stream since, and wins.
func (p *prefetchedStream) rebase(gen uint64, pos int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != gen || p.closed {
		return
	}
	p.base, p.consumed = pos, 0
}

// end records the decoder's end, clean or with err, for generation gen; a
// seek since has made it another generation's, and it is dropped.
func (p *prefetchedStream) end(gen uint64, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gen != gen || p.closed {
		return
	}
	p.ended, p.endErr = true, err
	p.cond.Broadcast()
}

func (p *prefetchedStream) roomLocked() int { return len(p.buf) - p.queuedLocked() }

func (p *prefetchedStream) queuedLocked() int {
	switch {
	case p.full:
		return len(p.buf)
	case p.w >= p.r:
		return p.w - p.r
	default:
		return len(p.buf) - (p.r - p.w)
	}
}

// Stream gives the speaker what is queued, and never waits for the decoder.
// When nothing is queued it gives silence, and after silence it waits for
// prefetchResume to be queued, so a slow network does not alternate between
// both every pull. The end and the error are the speaker's once it has had
// all that was decoded before them; a clean end returns a short count, as
// the decoder did, so a gapless next track fills the rest.
func (p *prefetchedStream) Stream(samples [][2]float64) (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, false
	}

	queued := p.queuedLocked()
	if p.silence && !p.ended && queued < p.resumeAt {
		clear(samples)
		p.faded = true
		return len(samples), true
	}
	if queued == 0 {
		if p.ended {
			p.err = p.endErr
			p.diag.endedWith(p.endErr)
			return 0, false
		}
		clear(samples)
		p.silence, p.faded = true, true
		return len(samples), true
	}
	p.silence = false
	p.diag.firstAudio()

	n := min(len(samples), queued)
	for i := range n {
		samples[i] = p.buf[p.r]
		p.r = (p.r + 1) % len(p.buf)
	}
	p.consumed += n
	p.full = false
	p.cond.Broadcast()

	if p.faded {
		fadeLen := min(n, prefetchFade)
		for i := range fadeLen {
			gain := float64(i) / float64(fadeLen)
			samples[i][0] *= gain
			samples[i][1] *= gain
		}
	}

	short := len(samples) - n
	if short > 0 && p.ended && p.endErr == nil {
		p.faded = false
		p.diag.endedWith(nil)
		return n, true // the clean end
	}
	if short > 0 {
		fadeLen := min(n, prefetchFade)
		for i := range fadeLen {
			gain := float64(fadeLen-1-i) / float64(fadeLen)
			samples[n-fadeLen+i][0] *= gain
			samples[n-fadeLen+i][1] *= gain
		}
		clear(samples[n:])
		if !p.ended {
			p.silence = true
		}
	}
	p.faded = short > 0
	return len(samples), true
}

// Err is the stream's error, once the speaker has had the audio before it.
func (p *prefetchedStream) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// Len is the track's length in samples.
func (p *prefetchedStream) Len() int { return p.src.Len() }

// Position is the position of the next sample the speaker gets: the last
// seek's target, or the start, and what has been played since.
func (p *prefetchedStream) Position() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.base + p.consumed
}

// Seek asks for sample pos and returns at once: nil means the seek was
// accepted, not that the decoder is there. What is queued, and what a read
// under way brings back, are dropped; the fill goroutine seeks the decoder
// to the latest target, and until it has, the speaker gets silence and
// Position is the target. A seek the decoder refuses is not the stream's
// error: it plays on from where the decoder is, which can be past audio that
// had been queued (rebase). pos is not clamped to Len here; Player.Seek
// clamps. A read error is the stream's end: a seek after it returns it, and
// retries nothing.
func (p *prefetchedStream) Seek(pos int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.closed:
		return net.ErrClosed
	case p.err != nil:
		return p.err
	case p.ended && p.endErr != nil:
		return p.endErr
	}
	pos = max(pos, 0)
	p.gen++
	p.seekTo, p.seekDue = pos, true
	p.base, p.consumed = pos, 0
	p.r, p.w, p.full = 0, 0, false
	p.ended, p.endErr = false, nil
	p.silence, p.faded = true, true
	p.cond.Broadcast()
	return nil
}

// Close ends the stream: the decoder's read is cancelled and waited for, the
// decoder released as the streamer releases it (once; an MP3 or FLAC
// decoder closed, a Vorbis one not), and the fill goroutine has ended when
// Close returns.
func (p *prefetchedStream) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	err := p.src.Close()
	<-p.fillDone
	p.mu.Lock()
	p.diag.close()
	p.mu.Unlock()
	return err
}

// Format is the decoder's format.
func (p *prefetchedStream) Format() beep.Format { return p.src.Format() }

// Duration is the track's length.
func (p *prefetchedStream) Duration() time.Duration { return p.src.Duration() }
