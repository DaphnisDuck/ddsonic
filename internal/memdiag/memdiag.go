// Package memdiag is ddsonic's opt-in memory diagnostics for a non-release
// investigation (#168). It is off unless DDSONIC_MEMDIAG names a private
// output directory; off, every function here returns at once, nothing is
// created, and no goroutine or signal handler is started.
//
// On, it keeps counters and a log of events, and on SIGUSR1 (and once at
// exit) appends a snapshot of the counters and of the process's memory to
// snapshots.jsonl, with a goroutine dump and a heap profile beside it. It
// records no account, track, URI, file name, token or credential: events
// carry a process-local sequence number, fixed names and codec or format
// names, and never an error's text. It changes nothing it observes: no GC
// setting, no profiling rate, no timing.
package memdiag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bjarneo/cliamp/internal/appdir"
	"github.com/bjarneo/cliamp/internal/appmeta"
)

// Env, DDSONIC_MEMDIAG, names the private output directory. Unset or empty,
// diagnostics are off.
var Env = strings.ToUpper(appdir.Name) + "_MEMDIAG"

// codecLibs is set at link time by the diagnostic build recipe
// (-X github.com/bjarneo/cliamp/internal/memdiag.codecLibs=vorbis=1.3.7,...).
var codecLibs string

var cur atomic.Pointer[recorder]

// On reports whether diagnostics are on.
func On() bool { return cur.Load() != nil }

type event struct {
	Kind   string `json:"kind"`
	T      string `json:"t"`
	Ms     int64  `json:"ms"`
	Seq    uint64 `json:"seq,omitempty"`
	Ev     string `json:"ev"`
	Codec  string `json:"codec,omitempty"`
	Format string `json:"format,omitempty"`
}

type recorder struct {
	dir   string
	start time.Time

	mu       sync.Mutex
	counters map[string]int64

	seq    atomic.Uint64
	evMu   sync.RWMutex // held to send an event; stop takes it to close events
	evDone bool
	events chan event
	wrote  chan struct{} // closed when the event writer has ended

	snapMu sync.Mutex // one snapshot at a time
	snaps  int
	snapF  *os.File

	stopSig func()
	sigDone chan struct{}
}

// Start turns diagnostics on, with dir as the output directory. dir is
// created with mode 700 if it does not exist; an existing one must be a
// directory (not a symlink) that only its owner can open. Each run gets a
// subdirectory of its own. stop writes an exit snapshot, ends the event log
// and turns diagnostics off. Start fails if diagnostics are already on.
func Start(dir string) (stop func(), err error) {
	if dir == "" {
		return func() {}, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := privateDir(abs); err != nil {
		return nil, err
	}
	now := time.Now()
	run := filepath.Join(abs, fmt.Sprintf("run-%s-%d", now.UTC().Format("20060102T150405Z"), os.Getpid()))
	if err := os.Mkdir(run, 0o700); err != nil {
		return nil, err
	}
	r := &recorder{dir: run, start: now, counters: map[string]int64{}, events: make(chan event, 4096), wrote: make(chan struct{})}
	evF, err := r.create("events.jsonl")
	if err != nil {
		return nil, err
	}
	if r.snapF, err = r.create("snapshots.jsonl"); err != nil {
		evF.Close()
		return nil, err
	}
	if err := json.NewEncoder(r.snapF).Encode(buildInfo(now)); err != nil {
		evF.Close()
		r.snapF.Close()
		return nil, err
	}
	if !cur.CompareAndSwap(nil, r) {
		evF.Close()
		r.snapF.Close()
		return nil, errors.New("memdiag: already started")
	}
	go r.writeEvents(evF)
	r.stopSig, r.sigDone = notifySnapshots(r)
	var once sync.Once
	return func() { once.Do(r.stop) }, nil
}

// Counters is a copy of the counters, or nil when off.
func Counters() map[string]int64 {
	r := cur.Load()
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.counters)
}

// Dir is the current run's output directory, or "" when off.
func Dir() string {
	if r := cur.Load(); r != nil {
		return r.dir
	}
	return ""
}

func (r *recorder) stop() {
	if r.stopSig != nil {
		r.stopSig()
		<-r.sigDone
	}
	_ = r.snapshot("exit")
	cur.CompareAndSwap(r, nil)
	r.evMu.Lock()
	r.evDone = true
	close(r.events)
	r.evMu.Unlock()
	<-r.wrote
	r.snapMu.Lock()
	r.snapF.Close()
	r.snapMu.Unlock()
}

// privateDir makes dir (mode 700) or checks that an existing one is private.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	switch {
	case !fi.IsDir():
		return fmt.Errorf("memdiag: %s is not a directory", dir)
	case fi.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("memdiag: %s is mode %o; it must be private (700)", dir, fi.Mode().Perm())
	}
	return ownedByMe(fi, dir)
}

func (r *recorder) create(name string) (*os.File, error) {
	return os.OpenFile(filepath.Join(r.dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func key(name, label string) string {
	if label == "" {
		return name
	}
	return name + "." + label
}

// Count adds one to counter name, or name.label when label is set.
func Count(name, label string) { Add(name, label, 1) }

// Add adds d to counter name (or name.label); a negative d makes a gauge.
func Add(name, label string, d int64) {
	r := cur.Load()
	if r == nil {
		return
	}
	k := key(name, label)
	r.mu.Lock()
	r.counters[k] += d
	r.mu.Unlock()
}

// Event logs ev for sequence number seq (0 for none), with an optional codec
// and format name. It never blocks: when the log falls behind, the event is
// dropped and counted.
func Event(seq uint64, ev, codec, format string) {
	r := cur.Load()
	if r == nil {
		return
	}
	now := time.Now()
	e := event{Kind: "event", T: now.UTC().Format(time.RFC3339Nano), Ms: now.Sub(r.start).Milliseconds(), Seq: seq, Ev: ev, Codec: codec, Format: format}
	r.evMu.RLock()
	defer r.evMu.RUnlock()
	if r.evDone {
		return
	}
	select {
	case r.events <- e:
	default:
		Add("memdiag_events_dropped", "", 1)
	}
}

func (r *recorder) writeEvents(f *os.File) {
	defer close(r.wrote)
	defer f.Close()
	enc := json.NewEncoder(f)
	for e := range r.events {
		_ = enc.Encode(e)
	}
}

type seqKey struct{}

// WithOpen gives ctx a new sequence number for one track open, when on;
// off, it returns ctx itself.
func WithOpen(ctx context.Context) context.Context {
	r := cur.Load()
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, seqKey{}, r.seq.Add(1))
}

// OpenSeq is ctx's open sequence number, or 0.
func OpenSeq(ctx context.Context) uint64 {
	if cur.Load() == nil || ctx == nil {
		return 0
	}
	n, _ := ctx.Value(seqKey{}).(uint64)
	return n
}

// Codec names the codec of a go-librespot audio format name.
func Codec(format string) string {
	switch {
	case strings.HasPrefix(format, "OGG_VORBIS"):
		return "vorbis"
	case strings.HasPrefix(format, "MP3"):
		return "mp3"
	case strings.HasPrefix(format, "FLAC"):
		return "flac"
	case strings.HasPrefix(format, "AAC"), strings.HasPrefix(format, "MP4"):
		return "aac"
	}
	return "other"
}

func buildInfo(now time.Time) map[string]any {
	info := map[string]any{
		"kind":       "build",
		"t":          now.UTC().Format(time.RFC3339Nano),
		"go":         runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"codec_libs": codecLibs,
		"version":    appmeta.Version(),
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision", "vcs.modified", "vcs.time", "CGO_ENABLED", "-tags", "-trimpath":
			info[s.Key] = s.Value
		}
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/devgianlu/go-librespot" {
			v := d.Version
			if d.Replace != nil {
				v += " => " + d.Replace.Path
			}
			info["go-librespot"] = v
		}
	}
	return info
}
