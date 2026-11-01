package ipc

// ddsonic: a Unix socket path over the platform's limit is refused by name,
// in bytes, by the server and by every client.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// socketPathOfLen returns a socket path of exactly n bytes whose parent
// directory does not exist yet, and that parent.
func socketPathOfLen(t *testing.T, n int) (sock, parent string) {
	t.Helper()
	const name = "/d.sock"
	base := t.TempDir()
	if n-len(base)-1-len(name) < 1 {
		// A long TMPDIR leaves no room; /tmp is short everywhere we test.
		dir, err := os.MkdirTemp("/tmp", "s")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	}
	pad := n - len(base) - 1 - len(name)
	if pad < 1 {
		t.Skipf("no temporary directory short enough for a %d-byte socket path", n)
	}
	parent = filepath.Join(base, strings.Repeat("x", pad))
	sock = parent + name
	if len(sock) != n {
		t.Fatalf("built a %d-byte path, want %d", len(sock), n)
	}
	return sock, parent
}

func TestMaxSocketPathBytesIsThePlatforms(t *testing.T) {
	want := map[string]int{"linux": 107, "darwin": 103}[runtime.GOOS]
	if want == 0 {
		t.Skipf("no expectation recorded for %s", runtime.GOOS)
	}
	if MaxSocketPathBytes != want {
		t.Fatalf("MaxSocketPathBytes = %d on %s, want %d", MaxSocketPathBytes, runtime.GOOS, want)
	}
}

func TestValidateSocketPath(t *testing.T) {
	max := MaxSocketPathBytes
	accented := strings.Repeat("é", max/2) // two bytes each
	tests := []struct {
		name      string
		path      string
		wantBytes int // 0: accepted
	}{
		{"short", "/tmp/d.sock", 0},
		{"at the limit", "/" + strings.Repeat("x", max-1), 0},
		{"one byte over", "/" + strings.Repeat("x", max), max + 1},
		{"multi-byte at the limit", "/" + accented[:max-1-(max-1)%2] + strings.Repeat("x", (max-1)%2), 0},
		{"multi-byte over in bytes, under in characters", "/" + accented + "éé", 1 + len(accented) + 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSocketPath(tt.path)
			if tt.wantBytes == 0 {
				if len(tt.path) > max {
					t.Fatalf("test path is %d bytes, over the limit", len(tt.path))
				}
				if err != nil {
					t.Fatalf("ValidateSocketPath(%d bytes) = %v, want nil", len(tt.path), err)
				}
				return
			}
			var tooLong *SocketPathError
			if !errors.As(err, &tooLong) {
				t.Fatalf("ValidateSocketPath(%d bytes) = %v, want a *SocketPathError", len(tt.path), err)
			}
			if tooLong.Bytes != tt.wantBytes || tooLong.Max != max || tooLong.Path != tt.path {
				t.Errorf("error = %+v, want Bytes %d, Max %d and the path", tooLong, tt.wantBytes, max)
			}
			if !strings.Contains(err.Error(), tt.path) {
				t.Errorf("message %q does not name the path", err)
			}
		})
	}
	// The multi-byte rejection is about bytes: its character count fits.
	if over := "/" + accented + "éé"; utf8.RuneCountInString(over) > max {
		t.Fatalf("the multi-byte case has %d characters, which is over the limit too", utf8.RuneCountInString(over))
	}
}

// A path of exactly the limit serves requests: on Linux, 107 bytes.
func TestSocketPathAtTheLimitServes(t *testing.T) {
	sock, _ := socketPathOfLen(t, MaxSocketPathBytes)
	server, err := NewServer(sock)
	if err != nil {
		t.Fatalf("NewServer(%d bytes) = %v", len(sock), err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if _, err := SendV2(sock, V2Request{Method: "capabilities"}); err != nil {
		t.Fatalf("SendV2 over a %d-byte path: %v", len(sock), err)
	}
	stream, err := SubscribeV2(sock, json.RawMessage(`"events"`), []string{"runtime.state"})
	if err != nil {
		t.Fatalf("SubscribeV2 over a %d-byte path: %v", len(sock), err)
	}
	_ = stream.Close()
}

// One byte more (108 on Linux) is refused by the server and by each client
// entry point, as a path that is too long and never as "not running".
func TestOverlongSocketPathIsRefusedEverywhere(t *testing.T) {
	sock, parent := socketPathOfLen(t, MaxSocketPathBytes+1)
	users := map[string]func() error{
		"NewServer": func() error {
			server, err := NewServer(sock)
			if err == nil {
				_ = server.Close()
			}
			return err
		},
		"NewServerWithBroker": func() error {
			server, err := NewServerWithBroker(sock, nil)
			if err == nil {
				_ = server.Close()
			}
			return err
		},
		"SendV2": func() error {
			_, err := SendV2(sock, V2Request{Method: "state.get"})
			return err
		},
		"SendV2WithDeadline": func() error {
			_, err := SendV2WithDeadline(sock, V2Request{Method: "state.get"}, time.Second)
			return err
		},
		"SubscribeV2": func() error {
			stream, err := SubscribeV2(sock, json.RawMessage(`"events"`), []string{"runtime.state"})
			if err == nil {
				_ = stream.Close()
			}
			return err
		},
		"StreamBands": func() error {
			return StreamBands(context.Background(), sock, time.Millisecond, io.Discard)
		},
	}
	for name, use := range users {
		t.Run(name, func(t *testing.T) {
			err := use()
			var tooLong *SocketPathError
			if !errors.As(err, &tooLong) {
				t.Fatalf("error = %v, want a *SocketPathError", err)
			}
			if tooLong.Bytes != len(sock) || tooLong.Max != MaxSocketPathBytes {
				t.Errorf("error = %+v, want Bytes %d and Max %d", tooLong, len(sock), MaxSocketPathBytes)
			}
			if errors.Is(err, ErrNotRunning) {
				t.Errorf("error %v reads as not running", err)
			}
		})
	}
	// The server refuses before it creates anything.
	if _, err := os.Stat(parent); !os.IsNotExist(err) {
		t.Errorf("the socket's directory exists after the refusals (stat: %v)", err)
	}
}

// isSocketUnavailable falls back to reading an error's text, and this error's
// text holds the path: a directory named like a dial failure must not turn
// the refusal into "not running".
func TestOverlongSocketPathNamedLikeADialFailure(t *testing.T) {
	sock := "/connection refused/no such file/" + strings.Repeat("x", MaxSocketPathBytes) + ".sock"
	err := ValidateSocketPath(sock)
	if isSocketUnavailable(err) {
		t.Fatalf("isSocketUnavailable(%v) = true", err)
	}
	_, err = SendV2(sock, V2Request{Method: "state.get"})
	var tooLong *SocketPathError
	if !errors.As(err, &tooLong) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("SendV2 = %v, want a *SocketPathError", err)
	}
}

// Linux (and Go's AF_UNIX on Windows) also take abstract addresses, which
// start with "@" or a NUL, name no file and need no closing NUL: they may
// fill sun_path. ddsonic never computes one, but the ipc package took them
// before the check existed and still does.
func TestValidateSocketPathAbstractAddresses(t *testing.T) {
	abstract := runtime.GOOS == "linux" || runtime.GOOS == "android" || runtime.GOOS == "windows"
	full := MaxSocketPathBytes + 1 // all of sun_path
	tests := []struct{ name, path string }{
		{"@, one under sun_path", "@" + strings.Repeat("x", full-2)},
		{"@, all of sun_path", "@" + strings.Repeat("x", full-1)},
		{"NUL, all of sun_path", "\x00" + strings.Repeat("x", full-1)},
		{"@, one over sun_path", "@" + strings.Repeat("x", full)},
		{"NUL, one over sun_path", "\x00" + strings.Repeat("x", full)},
		{"a file, all of sun_path", "/" + strings.Repeat("x", full-1)},
		{"a file with @ later, all of sun_path", "x@" + strings.Repeat("x", full-2)},
		{"a relative file, all of sun_path", strings.Repeat("x", full)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Where there are no abstract addresses, "@x" is a file like any other.
			limit := MaxSocketPathBytes
			if abstract && (tt.path[0] == '@' || tt.path[0] == 0) {
				limit = full
			}
			err := ValidateSocketPath(tt.path)
			if len(tt.path) <= limit {
				if err != nil {
					t.Fatalf("ValidateSocketPath(%d bytes) = %v, want nil", len(tt.path), err)
				}
				return
			}
			var tooLong *SocketPathError
			if !errors.As(err, &tooLong) {
				t.Fatalf("ValidateSocketPath(%d bytes) = %v, want a *SocketPathError", len(tt.path), err)
			}
			if tooLong.Max != limit || tooLong.Bytes != len(tt.path) {
				t.Errorf("error = %+v, want Max %d and Bytes %d", tooLong, limit, len(tt.path))
			}
		})
	}
}

// A real exchange over an abstract address that fills sun_path (108 bytes on
// Linux), as worked before the check, while a file path of the same length
// is refused.
func TestAbstractSocketAddressAtSunPathStillConnects(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("abstract socket addresses are exercised on Linux")
	}
	prefix := fmt.Sprintf("@ddsonic-test-%d-%d-", os.Getpid(), time.Now().UnixNano())
	addr := prefix + strings.Repeat("x", MaxSocketPathBytes+1-len(prefix))
	if len(addr) != 108 {
		t.Fatalf("built a %d-byte address, want 108", len(addr))
	}
	listener, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatalf("listen on a 108-byte abstract address: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := newFrameScanner(conn)
		if !scanner.Scan() {
			return
		}
		var request V2Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		reply, _ := json.Marshal(V2Response{ID: request.ID, OK: true})
		_, _ = conn.Write(append(reply, '\n'))
	}()

	response, err := SendV2(addr, V2Request{ID: json.RawMessage(`"abstract"`), Method: "capabilities"})
	if err != nil || !response.OK {
		t.Fatalf("SendV2 over a 108-byte abstract address = %+v, %v; want OK", response, err)
	}

	var tooLong *SocketPathError
	if _, err := SendV2("/"+addr[1:], V2Request{Method: "capabilities"}); !errors.As(err, &tooLong) || tooLong.Max != 107 {
		t.Fatalf("SendV2 over a 108-byte file path = %v, want refused at 107", err)
	}
	if _, err := SendV2(addr+"x", V2Request{Method: "capabilities"}); !errors.As(err, &tooLong) || tooLong.Max != 108 {
		t.Fatalf("SendV2 over a 109-byte abstract address = %v, want refused at 108", err)
	}
}
