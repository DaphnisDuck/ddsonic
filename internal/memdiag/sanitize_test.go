package memdiag

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	sentinelDir = "/sentinel-dir-q7k2"
	sentinelExe = "secret-exe-z3w9"
)

func pbBytes(b []byte, num uint64, payload []byte) []byte {
	b = binary.AppendUvarint(b, num<<3|wireBytes)
	b = binary.AppendUvarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func pbVarint(b []byte, num, v uint64) []byte {
	b = binary.AppendUvarint(b, num<<3|wireVarint)
	return binary.AppendUvarint(b, v)
}

// syntheticProfile is a minimal profile.proto whose mappings and function
// name the sentinel directory and executable.
func syntheticProfile(t *testing.T) []byte {
	t.Helper()
	strs := []string{"", sentinelDir + "/" + sentinelExe, sentinelDir + "/lib/libvorbis.so.0", sentinelDir + "/src/decode.go", "main.decode", "[vdso]", "github.com/x/y/rel.go"}
	var p []byte
	p = pbBytes(p, fieldMapping, pbVarint(pbVarint(nil, 1, 1), mappingFilename, 1))
	p = pbBytes(p, fieldMapping, pbVarint(pbVarint(nil, 1, 2), mappingFilename, 2))
	p = pbBytes(p, fieldMapping, pbVarint(pbVarint(nil, 1, 3), mappingFilename, 5))
	p = pbBytes(p, fieldFunction, pbVarint(pbVarint(pbVarint(nil, 1, 1), 2, 4), functionFilename, 3))
	p = pbBytes(p, fieldFunction, pbVarint(pbVarint(pbVarint(nil, 1, 2), 2, 4), functionFilename, 6))
	for _, s := range strs {
		p = pbBytes(p, fieldStringTable, []byte(s))
	}
	p = pbVarint(p, 9, 12345) // time_nanos
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(p)
	zw.Close()
	return buf.Bytes()
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func strTable(t *testing.T, raw []byte) []string {
	t.Helper()
	fields, err := pbFields(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range fields {
		if f.num == fieldStringTable {
			out = append(out, string(f.payload))
		}
	}
	return out
}

// A profile naming a private directory and the executable keeps neither:
// the program's mapping becomes "executable", others and absolute source
// files their base names; function names, kernel mappings, relative files
// and every other field are kept.
func TestSanitizeProfileDropsPaths(t *testing.T) {
	in := syntheticProfile(t)
	out, err := sanitizeProfile(in, sentinelDir+"/"+sentinelExe)
	if err != nil {
		t.Fatal(err)
	}
	raw := gunzip(t, out)
	for _, s := range []string{"sentinel-dir-q7k2", sentinelExe} {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("the sanitized profile holds %q", s)
		}
	}
	got := strTable(t, raw)
	want := []string{"", "executable", "libvorbis.so.0", "decode.go", "main.decode", "[vdso]", "github.com/x/y/rel.go"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("string table = %q, want %q", got, want)
	}
	// Everything but the string table is byte-for-byte the same.
	strip := func(raw []byte) []byte {
		fields, _ := pbFields(raw)
		var b []byte
		for _, f := range fields {
			if f.num != fieldStringTable {
				b = append(b, f.raw...)
			}
		}
		return b
	}
	if !bytes.Equal(strip(raw), strip(gunzip(t, in))) {
		t.Error("a field other than the string table changed")
	}
	if _, err := sanitizeProfile([]byte("not gzip"), ""); err == nil {
		t.Error("garbage was accepted")
	}
}

func TestSanitizeStacks(t *testing.T) {
	in := "goroutine 1 [running]:\nmain.f(0xc000010000, 0x5)\n\t" + sentinelDir + "/src/main.go:12 +0x1d\ncreated by main.g in goroutine 2\n\t" + sentinelDir + "/x/y.go:7 +0x2\n\tnot/abs.go:3\n"
	want := "goroutine 1 [running]:\nmain.f(0xc000010000, 0x5)\n\tmain.go:12 +0x1d\ncreated by main.g in goroutine 2\n\ty.go:7 +0x2\n\tnot/abs.go:3\n"
	if got := string(sanitizeStacks([]byte(in))); got != want {
		t.Errorf("sanitizeStacks =\n%s\nwant\n%s", got, want)
	}
}

// A real snapshot's heap profile and goroutine dump name neither the test
// binary (its directory or name) nor the source directory, which an
// untrimmed test build records.
func TestSnapshotFilesHoldNoPaths(t *testing.T) {
	_, stop := start(t)
	run := Dir()
	if err := Snapshot("test"); err != nil {
		t.Fatal(err)
	}
	stop()
	exe, _ := os.Executable()
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	for _, name := range []string{"heap-001.pb.gz", "goroutines-001.txt", "heap-002.pb.gz", "goroutines-002.txt"} {
		data, err := os.ReadFile(filepath.Join(run, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".gz") {
			data = gunzip(t, data)
			if !bytes.Contains(data, []byte(executableToken)) {
				t.Errorf("%s: no %q mapping", name, executableToken)
			}
		}
		for _, s := range []string{filepath.Dir(exe), filepath.Base(exe), wd, home} {
			if s != "" && s != "/" && bytes.Contains(data, []byte(s)) {
				t.Errorf("%s holds %q", name, s)
			}
		}
	}
}

// A collection that fails is counted, marks the snapshots and the end lines
// incomplete, and ends with one fixed line on stderr naming no path.
func TestIncompleteCollectionIsSurfaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	_, stop := start(t)
	run := Dir()
	if err := os.Chmod(run, 0o500); err != nil { // the profiles cannot be created
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(run, 0o700) })
	if err := Snapshot("test"); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	stop()
	os.Stderr = orig
	w.Close()
	stderr, _ := io.ReadAll(r)
	if string(stderr) != "memdiag: collection incomplete\n" {
		t.Errorf("stderr = %q", stderr)
	}
	snaps := readLines(t, filepath.Join(run, "snapshots.jsonl"))
	last := snaps[len(snaps)-1]
	if snaps[1]["collection_incomplete"] != true || last["kind"] != "end" || last["collection_incomplete"] != true {
		t.Errorf("snapshots not marked incomplete: %v", snaps)
	}
	if c, _ := snaps[1]["counters"].(map[string]any); c["memdiag_snapshot_errors"] != float64(2) {
		t.Errorf("snapshot error counter = %v", c["memdiag_snapshot_errors"])
	}
	evs := readLines(t, filepath.Join(run, "events.jsonl"))
	if end := evs[len(evs)-1]; end["kind"] != "end" || end["collection_incomplete"] != true {
		t.Errorf("events end = %v", end)
	}
	if bytes.Contains(stderr, []byte("/")) {
		t.Error("stderr names a path")
	}
}
