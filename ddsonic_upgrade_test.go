// ddsonic: the self-updater must refuse, since it would install cliamp.

package main

import (
	"context"
	"errors"
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
