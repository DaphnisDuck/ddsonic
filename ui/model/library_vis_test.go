package model

// ddsonic: v cycles the visualizer in the queue, as in cliamp, and every mode
// keeps the frame exactly the terminal's size.

import (
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestVCyclesTheVisualizerInTheQueue(t *testing.T) {
	for _, o := range []frameOpts{{vis: true}, {border: true, vis: true}} {
		for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {56, 16}} {
			m := layoutScreensWith(t, size.w, size.h, o, "Queue")["Queue"].m
			saver := &recordingSaver{}
			m.configSaver = saver
			seen := map[string]bool{}
			start := m.vis.ModeName()
			for i := range 80 {
				m = queuePress(m, "v")
				name := m.vis.ModeName()
				seen[name] = true
				t.Run(fmt.Sprintf("%+v/%dx%d/%d-%s", o, size.w, size.h, i, name), func(t *testing.T) {
					if !o.border && name == "None" {
						// cliamp's layout without a visualizer lets the status
						// line push the frame's blank bottom row off (layout.go).
						if got := lipgloss.Height(m.View().Content); got != size.h {
							t.Errorf("view height = %d, want %d", got, size.h)
						}
						return
					}
					checkFrame(t, m, size.w, size.h)
				})
				if name == start {
					break
				}
			}
			if len(seen) < 2 || !seen[start] {
				t.Errorf("%dx%d: v went through %v from %q; want the modes and back", size.w, size.h, seen, start)
			}
			if got := saver.saved["visualizer"]; got != fmt.Sprintf("%q", start) {
				t.Errorf("saved visualizer = %s, want the last mode %q", got, start)
			}
		}
	}
}
