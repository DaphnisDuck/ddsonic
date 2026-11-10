// ddsonic: the self-updater must refuse, since it would install cliamp.

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestUpgradeDisabled(t *testing.T) {
	cmd := upgradeCommand()
	if !cmd.Hidden {
		t.Error("upgrade should be hidden from help")
	}
	if err := cmd.Action(context.Background(), cmd); !errors.Is(err, errUpgradeDisabled) {
		t.Fatalf("upgrade Action error = %v, want errUpgradeDisabled", err)
	}
}

// The advice names only ways that exist: the releases page and a source
// build. No ddsonic package is in the AUR (checked 2026-11-09, #102).
func TestUpgradeAdviceNamesNoPackage(t *testing.T) {
	msg := errUpgradeDisabled.Error()
	for _, bad := range []string{"AUR", "-bin", "package manager"} {
		if strings.Contains(msg, bad) {
			t.Errorf("%q names %q", msg, bad)
		}
	}
	for _, want := range []string{"https://github.com/DaphnisDuck/ddsonic/releases", "source"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
}
