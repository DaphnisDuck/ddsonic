package memdiag

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

// goMetrics are the runtime/metrics a snapshot records: every memory class,
// the live heap, GC cycles, goroutines and cgo calls.
var goMetrics = func() []string {
	names := []string{
		"/gc/heap/live:bytes",
		"/gc/heap/goal:bytes",
		"/gc/cycles/total:gc-cycles",
		"/sched/goroutines:goroutines",
		"/cgo/go-to-c-calls:calls",
	}
	for _, d := range metrics.All() {
		if strings.HasPrefix(d.Name, "/memory/classes/") {
			names = append(names, d.Name)
		}
	}
	return names
}()

type snapshot struct {
	Kind     string            `json:"kind"`
	N        int               `json:"n"`
	Trigger  string            `json:"trigger"`
	T        string            `json:"t"`
	Ms       int64             `json:"ms"`
	Counters map[string]int64  `json:"counters"`
	Go       map[string]any    `json:"go"`
	Status   map[string]int64  `json:"proc_status_kB,omitempty"`
	Rollup   map[string]int64  `json:"proc_smaps_rollup_kB,omitempty"`
	Threads  int               `json:"threads,omitempty"`
	FDs      map[string]int    `json:"fds_by_type,omitempty"`
	Files    map[string]string `json:"files"`
	Errors   []string          `json:"errors,omitempty"`
	// Incomplete: this snapshot, or anything before it, failed to collect
	// or write something (the memdiag_* counters say what).
	Incomplete bool `json:"collection_incomplete"`
}

// Snapshot appends a snapshot to the run's snapshots.jsonl, with its
// goroutine dump and heap profile beside it. Off, it does nothing. It does
// not run the GC: the heap profile is as of the last cycle, whose number the
// snapshot records.
func Snapshot(trigger string) error {
	r := cur.Load()
	if r == nil {
		return nil
	}
	return r.snapshot(trigger)
}

func (r *recorder) snapshot(trigger string) error {
	r.snapMu.Lock()
	defer r.snapMu.Unlock()
	r.snaps++
	now := time.Now()
	s := snapshot{
		Kind: "snapshot", N: r.snaps, Trigger: trigger,
		T: now.UTC().Format(time.RFC3339Nano), Ms: now.Sub(r.start).Milliseconds(),
		Go: goSample(), Files: map[string]string{},
	}
	r.mu.Lock()
	s.Counters = maps.Clone(r.counters)
	r.mu.Unlock()
	fail := func(what string) { s.Errors = append(s.Errors, what) }

	if runtime.GOOS == "linux" {
		if st, err := procKB("/proc/self/status", func(k string) bool {
			return strings.HasPrefix(k, "Vm") || strings.HasPrefix(k, "Rss")
		}); err == nil {
			s.Status = st
		} else {
			fail("proc status unavailable")
		}
		if ro, err := procKB("/proc/self/smaps_rollup", func(string) bool { return true }); err == nil {
			s.Rollup = ro
		} else {
			fail("smaps_rollup unavailable")
		}
		if tasks, err := os.ReadDir("/proc/self/task"); err == nil {
			s.Threads = len(tasks)
		} else {
			fail("thread count unavailable")
		}
		if fds, err := fdTypes(); err == nil {
			s.FDs = fds
		} else {
			fail("fd count unavailable")
		}
	}

	base := fmt.Sprintf("%03d", r.snaps)
	if name, err := r.profile("goroutines-"+base+".txt", "goroutine", 2); err == nil {
		s.Files["goroutines"] = name
	} else {
		fail("goroutine dump failed")
	}
	if name, err := r.profile("heap-"+base+".pb.gz", "heap", 0); err == nil {
		s.Files["heap"] = name
	} else {
		fail("heap profile failed")
	}
	if len(s.Errors) > 0 {
		r.add("memdiag_snapshot_errors", "", int64(len(s.Errors)))
		s.Counters["memdiag_snapshot_errors"] += int64(len(s.Errors))
	}
	s.Incomplete = r.incomplete()
	if err := json.NewEncoder(r.snapF).Encode(s); err != nil {
		r.add("memdiag_write_errors", "snapshot", 1)
		return err
	}
	return nil
}

// profile writes pprof profile which, with its paths removed (sanitize.go).
func (r *recorder) profile(name, which string, debug int) (string, error) {
	var buf bytes.Buffer
	if err := pprof.Lookup(which).WriteTo(&buf, debug); err != nil {
		return "", err
	}
	data := buf.Bytes()
	if debug == 0 {
		var err error
		if data, err = sanitizeProfile(data, r.exe); err != nil {
			return "", err
		}
	} else {
		data = sanitizeStacks(data)
	}
	f, err := r.create(name)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return name, err
}

func goSample() map[string]any {
	samples := make([]metrics.Sample, len(goMetrics))
	for i, n := range goMetrics {
		samples[i].Name = n
	}
	metrics.Read(samples)
	out := map[string]any{}
	var total, released uint64
	for _, s := range samples {
		if s.Value.Kind() != metrics.KindUint64 {
			continue
		}
		v := s.Value.Uint64()
		out[s.Name] = v
		switch s.Name {
		case "/memory/classes/total:bytes":
			total = v
		case "/memory/classes/heap/released:bytes":
			released = v
		}
	}
	// Mapped by the Go runtime and not given back to the OS.
	out["committed_bytes"] = total - released
	out["cgo_calls"] = runtime.NumCgoCall()
	return out
}

// procKB reads "Key:   123 kB" lines of a /proc file whose key keep accepts.
// A line without a number after its key (smaps_rollup's header) is skipped.
func procKB(path string, keep func(string) bool) (map[string]int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.ContainsAny(k, " \t") || !keep(k) {
			continue
		}
		fields := strings.Fields(v)
		if len(fields) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
			out[k] = n
		}
	}
	return out, sc.Err()
}

// fdTypes counts the process's open descriptors by kind, never by path: the
// directory read to count them is left out.
func fdTypes() (map[string]int, error) {
	const dir = "/proc/self/fd"
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	self, _ := os.Readlink("/proc/self")
	scan := filepath.Join("/proc", self, "fd")
	out := map[string]int{}
	for _, e := range ents {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // closed since the listing: the listing's own descriptor
		}
		if target == scan {
			continue
		}
		out[fdKind(target)]++
	}
	return out, nil
}

// fdKind names a descriptor's kind from its /proc link without keeping the
// link: "socket", "pipe", "anon_inode:<kernel name>", "memfd", "dev", "file".
func fdKind(target string) string {
	switch {
	case strings.HasPrefix(target, "socket:"):
		return "socket"
	case strings.HasPrefix(target, "pipe:"):
		return "pipe"
	case strings.HasPrefix(target, "anon_inode:"):
		name := strings.Trim(strings.TrimPrefix(target, "anon_inode:"), "[]")
		if name == "" || strings.ContainsAny(name, "/ ") {
			return "anon_inode"
		}
		return "anon_inode:" + name
	case strings.HasPrefix(target, "/memfd:"):
		return "memfd"
	case strings.HasPrefix(target, "/dev/"):
		return "dev"
	case strings.HasPrefix(target, "/"):
		return "file"
	}
	return "other"
}
