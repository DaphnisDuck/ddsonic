package artwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // decoders for the formats artwork comes in
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dhowden/tag"

	"github.com/bjarneo/cliamp/internal/fileutil"
)

const (
	// maxBytes caps one image file, downloaded or local; covers are tens
	// of kilobytes.
	maxBytes = 10 << 20
	// maxPixels caps an image's sides before it is decoded, so a small file
	// declaring a huge image cannot take gigabytes to decode.
	maxPixels = 8000
	// cacheMaxBytes caps the remote cache; the oldest files go first.
	cacheMaxBytes = 100 << 20
	// maxSide is the longest side an image is scaled down to: enough for
	// a large terminal, small enough to send quickly.
	maxSide = 512
)

// coverNames are the folder cover files looked for, in order.
var coverNames = []string{"cover", "folder", "front", "album", "albumart"}

// Loader loads artwork. Remote images are cached in Dir.
type Loader struct {
	Dir    string
	Client *http.Client
}

// Load returns r's image, scaled to at most maxSide, or ErrNone when there
// is none. A remote image comes from the cache when it is there.
func (l *Loader) Load(ctx context.Context, r Ref) (image.Image, error) {
	var (
		data   []byte
		err    error
		cached string // the cache file data came from
	)
	switch {
	case r.URL != "":
		data, cached, err = l.remote(ctx, r.URL)
	case r.File != "":
		data, err = readCapped(r.File)
	case r.Audio != "":
		data, err = local(r.Audio)
	default:
		return nil, ErrNone
	}
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if cached != "" {
			_ = os.Remove(cached) // damaged on disk: fetched again next time
		}
		return nil, fmt.Errorf("artwork: decode: %w", err)
	}
	if cfg.Width > maxPixels || cfg.Height > maxPixels {
		return nil, fmt.Errorf("artwork: %dx%d image is too large", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("artwork: decode: %w", err)
	}
	return scale(img, maxSide), nil
}

// readCapped reads a regular file (a symlink to one included) of at most
// maxBytes. Anything else, a FIFO say, is refused rather than opened.
func readCapped(path string) ([]byte, error) {
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("artwork: %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("artwork: %s: over %d MB", path, maxBytes>>20)
	}
	return data, nil
}

// cachePath is where the remote image at rawURL is cached.
func (l *Loader) cachePath(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return filepath.Join(l.Dir, hex.EncodeToString(sum[:]))
}

// remote returns the image at rawURL, from the cache when it is there (and
// the cache file's path), else downloaded and cached.
func (l *Loader) remote(ctx context.Context, rawURL string) (data []byte, cached string, err error) {
	path := l.cachePath(rawURL)
	if l.Dir != "" {
		if data, err := readCapped(path); err == nil {
			now := time.Now()
			_ = os.Chtimes(path, now, now) // recently used: trimmed last
			return data, path, nil
		}
	}
	data, err = l.fetch(ctx, rawURL)
	if err != nil {
		return nil, "", err
	}
	if l.Dir != "" && os.MkdirAll(l.Dir, 0o700) == nil && fileutil.WriteFileAtomic(path, data, 0o600) == nil {
		trim(l.Dir, cacheMaxBytes)
	}
	return data, "", nil
}

// fetch downloads the image at rawURL.
func (l *Loader) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("artwork: %w", err)
	}
	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("artwork: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artwork: fetch: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("artwork: fetch: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("artwork: fetch: over %d MB", maxBytes>>20)
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("artwork: fetch: not an image: %w", err)
	}
	return data, nil
}

// NoDowngrade is an http.Client CheckRedirect that refuses a redirect from
// https to plain http.
func NoDowngrade(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("artwork: too many redirects")
	}
	if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("artwork: redirect from https to %s refused", req.URL.Scheme)
	}
	return nil
}

// local returns an audio file's embedded picture, else its folder's cover
// file, else ErrNone.
func local(audio string) ([]byte, error) {
	if info, err := os.Stat(audio); err != nil || !info.Mode().IsRegular() {
		return nil, ErrNone
	}
	if f, err := os.Open(audio); err == nil {
		m, err := tag.ReadFrom(f)
		f.Close()
		if err == nil && m.Picture() != nil && len(m.Picture().Data) > 0 && len(m.Picture().Data) <= maxBytes {
			return m.Picture().Data, nil
		}
	}
	entries, err := os.ReadDir(filepath.Dir(audio))
	if err != nil {
		return nil, ErrNone
	}
	for _, want := range coverNames {
		for _, e := range entries {
			name := strings.ToLower(e.Name())
			ext := filepath.Ext(name)
			if strings.TrimSuffix(name, ext) == want && slices.Contains([]string{".jpg", ".jpeg", ".png"}, ext) {
				if data, err := readCapped(filepath.Join(filepath.Dir(audio), e.Name())); err == nil {
					return data, nil // a symlinked cover too; a directory so named is skipped
				}
			}
		}
	}
	return nil, ErrNone
}

// trim removes the oldest files in dir until it holds at most max bytes.
func trim(dir string, max int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		size int64
		mod  time.Time
	}
	var files []file
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, file{filepath.Join(dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	slices.SortFunc(files, func(a, b file) int { return a.mod.Compare(b.mod) })
	for _, f := range files {
		if total <= max {
			return
		}
		if err := os.Remove(f.path); err == nil || errors.Is(err, fs.ErrNotExist) {
			total -= f.size
		}
	}
}
