package qobuz

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bjarneo/cliamp/internal/appdir"
)

// storedCreds holds persisted Qobuz credentials so the user only signs in once.
// The app_id, secrets and private key are scraped from the Qobuz web player and
// cached here alongside the OAuth user token.
type storedCreds struct {
	AppID         string   `json:"app_id"`
	Secrets       []string `json:"secrets"`
	Secret        string   `json:"secret"` // validated signing secret
	PrivateKey    string   `json:"private_key"`
	UserAuthToken string   `json:"user_auth_token"`
	UserID        string   `json:"user_id"`
	Label         string   `json:"label"`
}

// CredsPath returns the absolute path to the stored Qobuz credentials file.
func CredsPath() (string, error) {
	dir, err := appdir.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "qobuz_credentials.json"), nil
}

// DeleteCreds removes the stored Qobuz credentials file. Returns true if a file
// was removed, false if it did not exist.
func DeleteCreds() (bool, error) {
	path, err := CredsPath()
	if err != nil {
		return false, err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func loadCreds() (*storedCreds, error) {
	path, err := CredsPath()
	if err != nil {
		return nil, fmt.Errorf("qobuz: credentials path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("qobuz: read credentials: %w", err)
	}
	var creds storedCreds
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("qobuz: parse credentials: %w", err)
	}
	return &creds, nil
}

func saveCreds(creds *storedCreds) error {
	path, err := CredsPath()
	if err != nil {
		return fmt.Errorf("qobuz: credentials path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("qobuz: create credentials dir: %w", err)
	}
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("qobuz: encode credentials: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("qobuz: write credentials: %w", err)
	}
	return nil
}
