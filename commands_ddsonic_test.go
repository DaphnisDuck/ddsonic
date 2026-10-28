package main

// ddsonic: tests for ddsonic' own subcommands.

import (
	"context"
	"strings"
	"testing"
)

// Signing in to YouTube needs an OAuth client configured first.
func TestYouTubeSignInNeedsClient(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir()) // an empty config
	err := youtubeCommand().Run(context.Background(), []string{"youtube", "signin"})
	if err == nil || !strings.Contains(err.Error(), "client_id and client_secret") {
		t.Errorf("signin without a client = %v, want a pointer to the setup", err)
	}
}
