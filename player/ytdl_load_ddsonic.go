package player

// ddsonic: a YouTube track is loading from the moment yt-dlp is started for
// it until its first audio arrives and it is handed to the speaker. Upstream
// has no handle on a track in that state: Stop and the next request reach
// only what is already playing or queued, so an unwanted load ran on until
// yt-dlp produced audio or the 30 s wait for it ended.
//
// Each load now has a context of its own. The player keeps the loads that
// are pending and discards the ones that have become unwanted, by kind: Stop
// discards all of them; a new playback request, the play loads before it; a
// dropped or replaced preload, the preload loads; a cancelled seek, the seek
// loads. Discarding is all those callers do, so they stay as quick as they
// were; the load's own goroutine then kills and reaps its yt-dlp and ffmpeg
// and removes yt-dlp's temporary directory (decodeYTDLPipe, Close). Nothing
// outside the player is touched: every other yt-dlp run has its own context.
//
// Three things keep this exact:
//
//   - A load is pending only until its pipeline is installed. Installing
//     takes it out of the pending ones in the same step (claimYTDLLoad), so
//     what is playing or queued is never discarded as a load.
//   - A load registers only if it is still wanted at that moment
//     (beginYTDLLoad, beginYTDLSeekLoad), judged under the lock the
//     discarding takes: its generation is current and no Stop has come since
//     its request was made. A Stop that came before the load's goroutine got
//     to run is therefore not missed. A seek notes the count of Stops and
//     the track it seeks in under lifecycleMu, which Stop holds from counting
//     itself to clearing the track, so it never pairs a Stop's count with
//     the track that Stop is ending.
//   - A context ends for two reasons, and they are told apart: the load was
//     discarded, or it is over and is being cleaned up. Only the first makes
//     a load "no longer wanted"; a load that failed keeps its own error.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SeekYTDL calls these two as it goes, and tests replace them to act in the
// windows they mark: ytdlSeekNoted once the seek has taken note of what it
// is seeking in, before it mutes it; ytdlSeekCaptured once it has muted it,
// before it registers its load.
var (
	ytdlSeekNoted    = func() {}
	ytdlSeekCaptured = func() {}
)

// errYTDLLoadCancelled is what a load returns when it was discarded because
// nobody wants its track any more. It is the cause its context ends with.
var errYTDLLoadCancelled = errors.New("the track stopped loading: it is no longer wanted")

// errYTDLLoadOver is the cause a load's context ends with when the load is
// finished with, whether it failed or its pipeline was closed.
var errYTDLLoadOver = errors.New("the load is over")

// ytdlLoadKind is what a load is for.
type ytdlLoadKind int

const (
	ytdlPlayLoad ytdlLoadKind = iota
	ytdlPreloadLoad
	ytdlSeekLoad
)

// ytdlLoad is one YouTube track being loaded.
type ytdlLoad struct {
	// ctx ends when the load is discarded, and at the latest when the
	// pipeline it became is closed. yt-dlp, and the duration probe, run on
	// it.
	ctx         context.Context
	cancelCause context.CancelCauseFunc
	kind        ytdlLoadKind
}

func newYTDLLoad(kind ytdlLoadKind) *ytdlLoad {
	ctx, end := context.WithCancelCause(context.Background())
	return &ytdlLoad{ctx: ctx, cancelCause: end, kind: kind}
}

// discard ends the load because its track is no longer wanted.
func (l *ytdlLoad) discard() { l.cancelCause(errYTDLLoadCancelled) }

// end ends the load's context because the load is over. A load that was
// discarded stays discarded: the first cause is the one that counts.
func (l *ytdlLoad) end() { l.cancelCause(errYTDLLoadOver) }

// unwanted reports whether the load was discarded. A nil load, which the
// pipelines of other sources have, never is; neither is one that only ended.
func (l *ytdlLoad) unwanted() bool {
	return l != nil && errors.Is(context.Cause(l.ctx), errYTDLLoadCancelled)
}

// err is the load's own error when it was discarded, and why otherwise.
func (l *ytdlLoad) err(why error) error {
	if l.unwanted() {
		return errYTDLLoadCancelled
	}
	return why
}

// ytdlLoads is a player's pending loads, and what is needed to tell whether
// a load about to register is still wanted.
type ytdlLoads struct {
	mu      sync.Mutex
	pending map[*ytdlLoad]struct{}
	// stops counts the player's Stops. playStops is its value when the
	// current playback generation was declared: a play load of that
	// generation is wanted only while the two are equal.
	stops, playStops uint64
}

// register adds l to the pending loads when wanted says it is still wanted,
// and discards it otherwise. wanted runs under the lock.
func (p *Player) registerYTDLLoad(l *ytdlLoad, wanted func() bool) *ytdlLoad {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	if !wanted() {
		l.discard()
		return l
	}
	if p.ytdlLoads.pending == nil {
		p.ytdlLoads.pending = make(map[*ytdlLoad]struct{})
	}
	p.ytdlLoads.pending[l] = struct{}{}
	return l
}

// beginYTDLLoad starts a play or preload load. With requireCurrent, the load
// is discarded before it begins unless its generation is still the player's
// and, for a play load, no Stop has come since that generation was declared:
// its request was superseded or stopped before its goroutine got here. A
// request without a generation is made by the call itself and is always
// wanted.
func (p *Player) beginYTDLLoad(kind ytdlLoadKind, generation uint64, requireCurrent bool) *ytdlLoad {
	return p.registerYTDLLoad(newYTDLLoad(kind), func() bool {
		switch {
		case !requireCurrent:
			return true
		case kind == ytdlPreloadLoad:
			return generation == p.preloadGen.Load()
		default:
			return generation == p.playGen.Load() && p.ytdlLoads.stops == p.ytdlLoads.playStops
		}
	})
}

// ytdlSeekRequest is what a seek notes when it begins: the seek generation,
// and how many Stops there had been.
type ytdlSeekRequest struct {
	gen   int64
	stops uint64
}

// noteYTDLSeek takes note of a seek that begins at seek generation gen. The
// caller holds lifecycleMu and reads what is playing in the same section, so
// that the count and the track are of the same moment with respect to Stop.
func (p *Player) noteYTDLSeek(gen int64) ytdlSeekRequest {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	return ytdlSeekRequest{gen: gen, stops: p.ytdlLoads.stops}
}

// ytdlSeekWantedLocked reports whether no newer seek, other track or Stop
// has come since the seek was noted.
func (p *Player) ytdlSeekWantedLocked(req ytdlSeekRequest) bool {
	return req.gen == p.seekGen.Load() && req.stops == p.ytdlLoads.stops
}

// ytdlSeekWanted is ytdlSeekWantedLocked for a caller that does not hold the
// loads' lock. SeekYTDL asks it under the speaker's and the player's locks,
// together with whether its track is still the playing one, before it mutes
// that track.
func (p *Player) ytdlSeekWanted(req ytdlSeekRequest) bool {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	return p.ytdlSeekWantedLocked(req)
}

// beginYTDLSeekLoad starts the load of a seek's replacement stream. It is
// discarded before it begins when the seek is no longer wanted.
func (p *Player) beginYTDLSeekLoad(req ytdlSeekRequest) *ytdlLoad {
	return p.registerYTDLLoad(newYTDLLoad(ytdlSeekLoad), func() bool { return p.ytdlSeekWantedLocked(req) })
}

// endYTDLLoad takes a load that did not become a pipeline out of the pending
// ones and ends its context, and the duration probe with it. A load that was
// installed has left the pending ones already and is not ended here: its
// pipeline ends it when it is closed.
func (p *Player) endYTDLLoad(l *ytdlLoad) {
	p.ytdlLoads.mu.Lock()
	_, pending := p.ytdlLoads.pending[l]
	delete(p.ytdlLoads.pending, l)
	p.ytdlLoads.mu.Unlock()
	if pending {
		l.end()
	}
}

// discardYTDLLoads discards the pending loads of the given kinds. It only
// ends their contexts: it does not wait for anything.
func (p *Player) discardYTDLLoads(kinds ...ytdlLoadKind) {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	p.discardYTDLLoadsLocked(kinds...)
}

func (p *Player) discardYTDLLoadsLocked(kinds ...ytdlLoadKind) {
	for l := range p.ytdlLoads.pending {
		for _, kind := range kinds {
			if l.kind == kind {
				l.discard()
				delete(p.ytdlLoads.pending, l)
				break
			}
		}
	}
}

// declareYTDLPlayback is for SetPlaybackGeneration: the play loads of the
// requests before the new one are discarded, and the new generation is
// wanted until the next Stop.
func (p *Player) declareYTDLPlayback() {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	p.ytdlLoads.playStops = p.ytdlLoads.stops
	p.discardYTDLLoadsLocked(ytdlPlayLoad)
}

// stopYTDLLoads is for Stop: every pending load is discarded, and so is
// every load whose request was made before this and that has yet to
// register.
func (p *Player) stopYTDLLoads() {
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	p.ytdlLoads.stops++
	p.discardYTDLLoadsLocked(ytdlPlayLoad, ytdlPreloadLoad, ytdlSeekLoad)
}

// claimYTDLLoad makes tp's load the pipeline's own, or reports false when
// the load was discarded and tp must not be installed (the caller closes
// it). The callers hold the lock under which they install tp. A claimed load
// has left the pending ones in the same step, so nothing discards it as a
// load any more: from here it is stopped as what is playing or queued. A
// pipeline of another source has no load and is always claimed.
func (p *Player) claimYTDLLoad(tp *trackPipeline) bool {
	if tp.load == nil {
		return true
	}
	p.ytdlLoads.mu.Lock()
	defer p.ytdlLoads.mu.Unlock()
	if tp.load.unwanted() {
		return false
	}
	delete(p.ytdlLoads.pending, tp.load)
	return true
}

// ytdlLoadResult is what a play or preload load that did not become a
// pipeline returns, decided before the load is ended. A discarded request
// with a generation returns nil, as it does when its pipeline is refused at
// the end: the caller's own generation check drops its result. Anything else
// returns an error: a discarded request without a generation its own, a load
// that failed the reason it failed.
func ytdlLoadResult(l *ytdlLoad, requireCurrent bool, why error) error {
	if l.unwanted() && requireCurrent {
		return nil
	}
	return l.err(why)
}

// preloadYTDL builds a yt-dlp pipeline for the next track and queues it.
func (p *Player) preloadYTDL(pageURL string, knownDuration time.Duration, generation uint64, requireCurrent bool) error {
	load := p.beginYTDLLoad(ytdlPreloadLoad, generation, requireCurrent)
	defer p.endYTDLLoad(load)
	tp, err := p.buildYTDLPipeline(load, pageURL, 0)
	if err != nil {
		return ytdlLoadResult(load, requireCurrent, err)
	}
	tp.knownDuration = knownDuration
	if requireCurrent {
		return p.preloadPipelineForGeneration(tp, generation)
	}
	return p.preloadPipeline(tp)
}

// seekYTDLFailed is what SeekYTDL returns when its replacement stream did
// not load: nil when the seek was discarded, which is not a failure to show.
func seekYTDLFailed(l *ytdlLoad, err error) error {
	if l.unwanted() {
		return nil
	}
	return fmt.Errorf("yt-dlp seek: %w", err)
}
