package model

// ddsonic: a config or a remote command naming the hidden Logo visualizer is
// refused, and the visualizer stays as it was (the default, at startup).

import (
	"testing"

	"github.com/bjarneo/cliamp/ui"
)

func TestSetVisualizerRefusesLogo(t *testing.T) {
	m := Model{vis: ui.NewVisualizer(44100)}
	if !m.SetVisualizer("Bars") {
		t.Fatal("SetVisualizer(Bars) = false")
	}
	before := m.vis.Mode
	for _, name := range []string{"Logo", "logo"} {
		if m.SetVisualizer(name) {
			t.Fatalf("SetVisualizer(%q) = true, want it refused", name)
		}
		if m.vis.Mode != before || m.vis.ModeName() != "Bars" {
			t.Fatalf("after SetVisualizer(%q) the mode is %s, want Bars kept", name, m.vis.ModeName())
		}
	}
}
