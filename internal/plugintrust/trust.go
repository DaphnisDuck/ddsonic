// Package plugintrust persists approvals for Lua plugin content.
package plugintrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/fileutil"
)

const manifestName = ".trust.json"

var (
	ErrUntrusted    = errors.New("plugin is not trusted")
	ErrHashMismatch = errors.New("plugin content changed since approval")
)

type Manifest struct {
	Version int               `json:"version"`
	Plugins map[string]string `json:"plugins"`
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open plugin: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash plugin: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func Load(dir string) (Manifest, error) {
	m := Manifest{Version: 1, Plugins: make(map[string]string)}
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, fmt.Errorf("read plugin trust manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse plugin trust manifest: %w", err)
	}
	if m.Version != 1 || m.Plugins == nil {
		return m, errors.New("unsupported plugin trust manifest")
	}
	return m, nil
}

func Save(dir string, m Manifest) error {
	m.Version = 1
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plugin trust manifest: %w", err)
	}
	data = append(data, '\n')
	return fileutil.WriteFileAtomic(filepath.Join(dir, manifestName), data, 0o600)
}

func Approve(dir, name, path string) (string, error) {
	hash, err := HashFile(path)
	if err != nil {
		return "", err
	}
	m, err := Load(dir)
	if err != nil {
		return "", err
	}
	m.Plugins[name] = hash
	if err := Save(dir, m); err != nil {
		return "", err
	}
	return hash, nil
}

func Verify(m Manifest, name, path string) error {
	want, ok := m.Plugins[name]
	if !ok {
		return ErrUntrusted
	}
	got, err := HashFile(path)
	if err != nil {
		return err
	}
	if got != want {
		return ErrHashMismatch
	}
	return nil
}
