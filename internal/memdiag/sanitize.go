package memdiag

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// The profile.proto fields a heap profile's paths live in: the string table
// (Profile.string_table = 6), a mapping's file (Profile.mapping = 3,
// Mapping.filename = 5) and a function's source file (Profile.function = 5,
// Function.filename = 4). Both filenames are indexes into the string table.
const (
	fieldMapping     = 3
	fieldFunction    = 5
	fieldStringTable = 6
	mappingFilename  = 5
	functionFilename = 4
	wireVarint       = 0
	wireFixed64      = 1
	wireBytes        = 2
	wireFixed32      = 5
	executableToken  = "executable"
	errProfileFormat = "memdiag: unreadable profile"
)

// pbField is one top-level field of a protobuf message: its number, wire
// type, payload (for a length-delimited field) and its raw bytes.
type pbField struct {
	num     uint64
	wire    uint64
	payload []byte
	val     uint64 // a varint field's value
	raw     []byte
}

func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	for len(b) > 0 {
		start := b
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return nil, errors.New(errProfileFormat)
		}
		b = b[n:]
		f := pbField{num: tag >> 3, wire: tag & 7}
		switch f.wire {
		case wireVarint:
			f.val, n = binary.Uvarint(b)
			if n <= 0 {
				return nil, errors.New(errProfileFormat)
			}
			b = b[n:]
		case wireFixed64, wireFixed32:
			size := 8
			if f.wire == wireFixed32 {
				size = 4
			}
			if len(b) < size {
				return nil, errors.New(errProfileFormat)
			}
			b = b[size:]
		case wireBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return nil, errors.New(errProfileFormat)
			}
			f.payload = b[n : n+int(l)]
			b = b[n+int(l):]
		default:
			return nil, errors.New(errProfileFormat)
		}
		f.raw = start[:len(start)-len(b)]
		out = append(out, f)
	}
	return out, nil
}

// pbVarintField is the value of varint field num in message b, if present.
func pbVarintField(b []byte, num uint64) (uint64, bool) {
	fields, err := pbFields(b)
	if err != nil {
		return 0, false
	}
	for _, f := range fields {
		if f.num == num && f.wire == wireVarint {
			return f.val, true
		}
	}
	return 0, false
}

// sanitizeProfile rewrites a gzipped pprof profile so that it names no
// directory: every mapping's file becomes its base name, or "executable"
// for the program itself (exe), and every absolute source file of a
// function becomes its base name. Function names, line numbers, addresses,
// build IDs and samples are kept, so the profile stays readable with the
// binary given explicitly.
func sanitizeProfile(gz []byte, exe string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	fields, err := pbFields(raw)
	if err != nil {
		return nil, err
	}
	mapFiles, funcFiles := map[uint64]bool{}, map[uint64]bool{}
	var strs []string
	for _, f := range fields {
		switch {
		case f.num == fieldMapping && f.wire == wireBytes:
			if i, ok := pbVarintField(f.payload, mappingFilename); ok {
				mapFiles[i] = true
			}
		case f.num == fieldFunction && f.wire == wireBytes:
			if i, ok := pbVarintField(f.payload, functionFilename); ok {
				funcFiles[i] = true
			}
		case f.num == fieldStringTable && f.wire == wireBytes:
			strs = append(strs, string(f.payload))
		}
	}
	for i, s := range strs {
		switch {
		case s == "":
		case mapFiles[uint64(i)]:
			strs[i] = mappingName(s, exe)
		case funcFiles[uint64(i)] && strings.HasPrefix(s, "/"):
			strs[i] = filepath.Base(s)
		}
	}
	var out []byte
	next := 0
	for _, f := range fields {
		if f.num == fieldStringTable && f.wire == wireBytes {
			out = binary.AppendUvarint(out, fieldStringTable<<3|wireBytes)
			out = binary.AppendUvarint(out, uint64(len(strs[next])))
			out = append(out, strs[next]...)
			next++
			continue
		}
		out = append(out, f.raw...)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(out); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mappingName(s, exe string) string {
	if exe != "" && (s == exe || filepath.Base(s) == filepath.Base(exe)) {
		return executableToken
	}
	if strings.HasPrefix(s, "[") { // [vdso], [stack]: kernel names, no path
		return s
	}
	return filepath.Base(s)
}

// sanitizeStacks rewrites a goroutine dump's source locations
// ("\t/dir/file.go:123 +0x1f") to their base names ("\tfile.go:123 +0x1f").
// Everything else is kept: goroutine numbers and states, wait times,
// function names (with their module path) and the hex argument words, which
// are raw register and stack values, not dereferenced data.
func sanitizeStacks(dump []byte) []byte {
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(dump))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, "\t"); ok && strings.HasPrefix(rest, "/") {
			loc, tail, _ := strings.Cut(rest, " ")
			if i := strings.LastIndex(loc, ":"); i > 0 {
				line = "\t" + filepath.Base(loc[:i]) + loc[i:]
			} else {
				line = "\t" + filepath.Base(loc)
			}
			if tail != "" {
				line += " " + tail
			}
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}
