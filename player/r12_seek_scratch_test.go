package player

// Scratch (not committed): pause, seek forward, resume, on the output model.

import (
	"fmt"
	"testing"
	"time"
)

func TestR12SeekWhilePausedScratch(t *testing.T) {
	a := writeFixture(t, "a.wav", 1, 6)
	total, stale := 0, 0
	for _, refill := range []bool{false, true} {
		for _, dev := range []struct {
			name    string
			periods int
		}{{"linux", 2}, {"macos", 4}} {
			for lag := range 3 {
				for before := 6; before <= 6+dev.periods; before++ {
					name := fmt.Sprintf("late%v/%s/lag%d/before%d", refill, dev.name, lag, before)
					t.Run(name, func(t *testing.T) {
						p, m := newModelPlayer(t, 250, dev.periods, lag, refill)
						if err := p.PlayAt(a, 0, 0); err != nil {
							t.Fatal(err)
						}
						m.steps(before)
						p.TogglePause()
						m.steps(10)
						at := len(m.out)
						if err := p.Seek(2500 * time.Millisecond); err != nil {
							t.Fatal(err)
						}
						m.mark("seek+resume")
						p.TogglePause()
						m.steps(10)
						// Frames after the resume that continue the pre-pause
						// position: the first A run after the mark, if it
						// continues the last frame before it.
						pre := segments(m.out[:at])
						last := pre[len(pre)-1]
						lastC := (last.first-1+last.len-1)%counterWrap + 1
						after := segments(m.out[at:])
						old := 0
						if after[0].track == 'A' && after[0].first == lastC%counterWrap+1 {
							old = after[0].len
						}
						total++
						if old > 0 {
							stale++
						}
						t.Logf("%s: old-position after resume %s |%s", name, ms(old), describe(m))
					})
				}
			}
		}
	}
	t.Logf("SUMMARY: %d/%d with old-position audio after the resume", stale, total)
}
