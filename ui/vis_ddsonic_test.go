package ui

// ddsonic: the Logo visualizer draws cliamp's name, so ddsonic keeps it out of
// reach: not listed, not cycled to, not selectable by name or by mode.

import (
	"slices"
	"testing"
)

func TestPublicVisModeNamesHideLogo(t *testing.T) {
	names := PublicVisModeNames()
	if slices.Contains(names, "Logo") {
		t.Fatalf("PublicVisModeNames() lists Logo: %v", names)
	}
	if len(names) != int(VisCount)-len(hiddenVisModes) {
		t.Fatalf("PublicVisModeNames() has %d names, want every mode but the hidden ones (%d)", len(names), int(VisCount)-len(hiddenVisModes))
	}
	// The inherited table is intact: upstream's list still has the mode.
	if !slices.Contains(VisModeNames(), "Logo") {
		t.Fatal("VisModeNames() lost Logo; the upstream table must stay whole")
	}
	for _, name := range names {
		if _, ok := StringToVisModeExact(name); !ok {
			t.Errorf("listed mode %q cannot be selected by name", name)
		}
	}
}

func TestHiddenVisModeCannotBeSelected(t *testing.T) {
	for _, name := range []string{"Logo", "logo", "LOGO"} {
		if mode, ok := StringToVisModeExact(name); ok {
			t.Errorf("StringToVisModeExact(%q) = (%v, true), want not found", name, mode)
		}
	}
	v := NewVisualizer(44100)
	v.Mode = VisBars
	v.SetMode(VisLogo)
	if v.Mode != VisBars {
		t.Fatalf("SetMode(VisLogo) changed the mode to %v", v.Mode)
	}
}

func TestCycleModeSkipsHiddenModes(t *testing.T) {
	v := NewVisualizer(44100)
	v.Mode = VisLogo - 1
	v.CycleMode()
	if v.Mode != VisLogo+1 {
		t.Fatalf("cycling from the mode before Logo gave %v, want the mode after it (%v)", v.Mode, VisLogo+1)
	}
	// A whole lap visits every offered mode once, and never a hidden one.
	v.Mode = VisBars
	seen := map[VisMode]bool{}
	for range int(VisCount) - len(hiddenVisModes) {
		v.CycleMode()
		if visModeHidden(v.Mode) {
			t.Fatalf("cycled to hidden mode %v", v.Mode)
		}
		seen[v.Mode] = true
	}
	if len(seen) != int(VisCount)-len(hiddenVisModes) || v.Mode != VisBars {
		t.Fatalf("a lap visited %d modes and ended on %v, want %d and Bars", len(seen), v.Mode, int(VisCount)-len(hiddenVisModes))
	}
}
