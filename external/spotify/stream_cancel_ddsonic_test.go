package spotify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	librespot "github.com/devgianlu/go-librespot"
	"github.com/devgianlu/go-librespot/audio"
)

// chunkCDN answers a track's first chunk, as the CDN does, and hands every
// other chunk request to rest. The track is four chunks long.
func chunkCDN(rest func(*http.Request) (*http.Response, error)) roundTripFunc {
	return func(req *http.Request) (*http.Response, error) {
		if !strings.HasPrefix(req.Header.Get("Range"), "bytes=0-") {
			return rest(req)
		}
		return &http.Response{
			StatusCode: http.StatusPartialContent,
			Status:     "206 Partial Content",
			Header: http.Header{"Content-Range": {
				fmt.Sprintf("bytes 0-%d/%d", audio.DefaultChunkSize-1, 4*audio.DefaultChunkSize),
			}},
			Body:    io.NopCloser(bytes.NewReader(make([]byte, audio.DefaultChunkSize))),
			Request: req,
		}, nil
	}
}

// A stream that is closed ends its chunk reads at once: the chunk reader
// retries a failed request for about three seconds, and stops only for
// net.ErrClosed, which a cancelled stream's requests now fail with. No chunk
// is requested twice: nothing is retried.
func TestCancelledStreamEndsItsChunkReads(t *testing.T) {
	streamCtx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	requests := map[string]int{} // by range
	inFlight := make(chan struct{}, 8)
	cdn := chunkCDN(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		requests[req.Header.Get("Range")]++
		mu.Unlock()
		select {
		case inFlight <- struct{}{}:
		default:
		}
		<-req.Context().Done() // the network is gone: nothing answers
		return nil, req.Context().Err()
	})
	r, err := audio.NewHttpChunkedReader(&librespot.NullLogger{}, newSpotifyStreamHTTPClient(streamCtx, cdn), "https://cdn.example/audio")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = r.Close()
	})

	read := make(chan error, 1)
	go func() {
		_, err := r.ReadAt(make([]byte, 16), audio.DefaultChunkSize)
		read <- err
	}()
	select {
	case <-inFlight:
	case <-time.After(5 * time.Second):
		t.Fatal("no chunk request was sent")
	}
	cancel()
	start := time.Now()
	select {
	case err := <-read:
		if took := time.Since(start); took > promptly {
			t.Errorf("the read ended %v after the cancel, want at once", took)
		}
		if !errors.Is(err, net.ErrClosed) {
			t.Errorf("read error = %v, want net.ErrClosed", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the read did not end")
	}
	_ = r.Close() // waits for the prefetches
	mu.Lock()
	defer mu.Unlock()
	for rng, n := range requests {
		if n > 1 {
			t.Errorf("range %s was requested %d times, want once: a closed stream's request was retried", rng, n)
		}
	}
}

// A stream that is not cancelled keeps its network errors as they are, and
// the chunk reader its retries: the cancel's mapping is for the cancel only.
func TestLiveStreamKeepsItsNetworkErrors(t *testing.T) {
	errUnreachable := errors.New("dial tcp: connect: network is unreachable")
	var chunk1 atomic.Int32
	cdn := chunkCDN(func(req *http.Request) (*http.Response, error) {
		if strings.HasPrefix(req.Header.Get("Range"), fmt.Sprintf("bytes=%d-", audio.DefaultChunkSize)) {
			chunk1.Add(1)
		}
		return nil, errUnreachable
	})
	streamCtx, cancel := context.WithCancel(context.Background())
	r, err := audio.NewHttpChunkedReader(&librespot.NullLogger{}, newSpotifyStreamHTTPClient(streamCtx, cdn), "https://cdn.example/audio")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = r.Close()
	})

	_, err = r.ReadAt(make([]byte, 16), audio.DefaultChunkSize)
	if !errors.Is(err, errUnreachable) || errors.Is(err, net.ErrClosed) {
		t.Errorf("read error = %v, want the network's own error", err)
	}
	if n := chunk1.Load(); n != 4 {
		t.Errorf("chunk 1 was requested %d times, want 4 (one and three retries)", n)
	}
}
