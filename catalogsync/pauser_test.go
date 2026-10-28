package catalogsync

import (
	"errors"
	"testing"
)

// A run asked for while one runs is not lost: the running one goes again,
// and a failed run ends without repeating.
func TestPauserRerunsARefusedRequest(t *testing.T) {
	var p pauser
	if !p.begin() {
		t.Fatal("first begin refused")
	}
	calls := 0
	err := p.repeat(func() error {
		calls++
		if calls == 1 && p.begin() { // a request arrives mid-run
			t.Error("second begin accepted while running")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Errorf("repeat: err %v, %d runs; want the refused request served by a second run", err, calls)
	}
	if !p.begin() {
		t.Fatal("begin refused after the run ended")
	}
	calls = 0
	boom := errors.New("boom")
	err = p.repeat(func() error {
		calls++
		p.begin() // refused, asks for a rerun
		return boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("failed run: err %v, %d runs; want one run and its error", err, calls)
	}
	if !p.begin() {
		t.Error("begin refused after a failed run")
	}
}
