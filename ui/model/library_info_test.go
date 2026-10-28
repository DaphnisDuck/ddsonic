package model

// ddsonic: album artwork in the track info view (M10).

import (
	"context"
	"errors"
	"image"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/bjarneo/cliamp/artwork"
)

// fakeArt loads a square image for the keys in have, ErrNone for the rest,
// or err when set, counting its calls.
type fakeArt struct {
	have  map[string]bool
	err   error
	calls int
}

func (f *fakeArt) load(_ context.Context, r artwork.Ref) (image.Image, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.have[r.Key] {
		return image.NewRGBA(image.Rect(0, 0, 300, 300)), nil
	}
	return nil, artwork.ErrNone
}

// msgsOf runs cmd and returns the messages it and its batches and sequences
// produce.
func msgsOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		var out []tea.Msg
		for i := range v.Len() {
			out = append(out, msgsOf(v.Index(i).Interface().(tea.Cmd))...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// rawOf is the raw terminal output among msgs.
func rawOf(msgs []tea.Msg) string {
	var sb strings.Builder
	for _, m := range msgs {
		if r, ok := m.(tea.RawMsg); ok {
			sb.WriteString(r.Msg.(string))
		}
	}
	return sb.String()
}

// artModel is the queue at w×h with artwork on, loaded by f.
func artModel(t *testing.T, w, h int, f *fakeArt) Model {
	t.Helper()
	m, _ := newQueueModel(t)
	m.SetFrameBorder(true)
	m.SetArtwork(f.load)
	return resized(t, m, w, h)
}

// press sends key and runs the command it returns, delivering the messages
// the model handles, and returns the model and the raw output.
func press(t *testing.T, m Model, key string) (Model, string) {
	t.Helper()
	updated, cmd := m.Update(queueKey(key))
	return deliver(t, updated.(Model), cmd)
}

// deliver runs cmd, hands the model the artwork messages it produces (loads
// and write acknowledgements, as the program would), and returns the model
// and all the raw output, in order.
func deliver(t *testing.T, m Model, cmd tea.Cmd) (Model, string) {
	t.Helper()
	msgs := msgsOf(cmd)
	raw := rawOf(msgs)
	for _, msg := range msgs {
		switch msg.(type) {
		case artworkLoadedMsg, artworkWrittenMsg:
			updated, next := m.Update(msg)
			var more string
			m, more = deliver(t, updated.(Model), next)
			raw += more
		}
	}
	return m, raw
}

func hasArt(m Model) bool { return strings.Contains(m.View().Content, "\U0010EEEE") }

func TestInfoShowsArtwork(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	if hasArt(m) {
		t.Fatal("the queue shows artwork")
	}
	m, raw := press(t, m, "i")
	if !hasArt(m) {
		t.Fatalf("no artwork in the info view:\n%s", stripAnsi(m.View().Content))
	}
	if !strings.Contains(raw, "\x1b[16t") || !strings.Contains(raw, "f=100") || !strings.Contains(raw, "a=p") {
		t.Errorf("opening sent %q, want the cell size query and the image", raw)
	}
	out := stripAnsi(m.View().Content)
	if !strings.Contains(out, "Title: I") {
		t.Errorf("the metadata is gone:\n%s", out)
	}
	checkFrame(t, m, 120, 40)

	// Closing and reopening sends nothing again, and loads nothing again.
	m, _ = press(t, m, "esc")
	if hasArt(m) {
		t.Error("the artwork outlived the info view")
	}
	m, raw = press(t, m, "i")
	if raw != "" || f.calls != 1 || !hasArt(m) {
		t.Errorf("reopening sent %q after %d loads", raw, f.calls)
	}
}

func TestInfoWithoutArtwork(t *testing.T) {
	f := &fakeArt{}
	m := artModel(t, 120, 40, f)
	plain := artModel(t, 120, 40, f)
	plain.SetArtwork(nil)
	m, raw := press(t, m, "i")
	plain, _ = press(t, plain, "i")
	if hasArt(m) || strings.Contains(raw, "f=100") {
		t.Errorf("a track without artwork shows some (sent %q)", raw)
	}
	if got, want := stripAnsi(m.View().Content), stripAnsi(plain.View().Content); got != want {
		t.Errorf("the view differs from cliamp's:\n%s\nwant\n%s", got, want)
	}
	// Known to have none: not asked for again.
	m, _ = press(t, m, "esc")
	press(t, m, "i")
	if f.calls != 1 {
		t.Errorf("%d loads, want 1", f.calls)
	}
}

// A failed download shows the metadata alone and is tried again at the
// next open.
func TestInfoArtworkFailureRetries(t *testing.T) {
	f := &fakeArt{err: errors.New("network down")}
	m := artModel(t, 120, 40, f)
	m, _ = press(t, m, "i")
	if hasArt(m) {
		t.Error("a failed load shows artwork")
	}
	m, _ = press(t, m, "esc")
	f.err, f.have = nil, map[string]bool{"audio:I": true}
	m, _ = press(t, m, "i")
	if f.calls != 2 || !hasArt(m) {
		t.Errorf("after a retry: %d loads, artwork %v", f.calls, hasArt(m))
	}
}

// A load that lands after the view moved to another track does not show on
// it; each track shows its own.
func TestInfoArtworkNeverStale(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	updated, cmd := m.Update(queueKey("i")) // track I's load, held back
	m = updated.(Model)
	late := msgsOf(cmd)
	m, _ = press(t, m, "esc")
	m, _ = press(t, m, "j")
	m, _ = press(t, m, "i") // track II: none
	for _, msg := range late {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	if hasArt(m) {
		t.Error("track I's artwork shows on track II")
	}
	m, _ = press(t, m, "esc")
	m, _ = press(t, m, "k")
	m, _ = press(t, m, "i")
	if !hasArt(m) || f.calls != 2 {
		t.Errorf("back on track I: artwork %v after %d loads, want the late result kept", hasArt(m), f.calls)
	}
}

// The box follows the terminal: a resize places the image anew, and a
// terminal too small for it and the metadata shows the metadata alone.
func TestInfoArtworkFollowsTheTerminal(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	m, _ = press(t, m, "i")
	placed := m.lib.art.rows
	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 200, Height: 60})
	m, raw := deliver(t, updated.(Model), cmd)
	if !strings.Contains(raw, "d=i") || !strings.Contains(raw, "a=p") || m.lib.art.rows <= placed {
		t.Errorf("a taller terminal placed %q (%d rows, was %d)", raw, m.lib.art.rows, placed)
	}
	if !strings.Contains(raw, "\x1b[16t") || strings.Contains(raw, "f=100") {
		t.Errorf("a resize sent %q, want the cell size asked again and no image resent", raw)
	}
	checkFrame(t, m, 200, 60)
	for _, size := range []struct{ w, h int }{{56, 16}, {50, 40}} {
		updated, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		small := updated.(Model)
		if hasArt(small) {
			t.Errorf("%dx%d: artwork crowds the metadata", size.w, size.h)
		}
		checkFrame(t, small, size.w, size.h)
	}
	// The terminal's real cell size changes the box.
	updated, cmd = m.Update(uv.CellSizeEvent{Width: 10, Height: 20})
	m, _ = deliver(t, updated.(Model), cmd)
	c1 := m.lib.art.cols
	updated, cmd = m.Update(uv.CellSizeEvent{Width: 10, Height: 10})
	m, _ = deliver(t, updated.(Model), cmd)
	if m.lib.art.cols >= c1 {
		t.Errorf("square cells: %d columns, want fewer than %d", m.lib.art.cols, c1)
	}
}

func TestQuitFreesTheImage(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	m, _ = press(t, m, "i")
	updated, cmd := m.Update(queueKey("q"))
	msgs := msgsOf(cmd)
	if raw := rawOf(msgs); !strings.Contains(raw, "a=d") || !strings.Contains(raw, "d=I") {
		t.Errorf("quitting sent %q, want the image freed", raw)
	}
	if _, ok := msgs[len(msgs)-1].(tea.QuitMsg); !ok || !updated.(Model).quitting {
		t.Errorf("quitting ended with %T", msgs[len(msgs)-1])
	}
}

func TestArtBox(t *testing.T) {
	sq := image.Pt(300, 300)
	for _, tt := range []struct {
		name               string
		width, rows        int
		size               image.Point
		cellW, cellH       int
		wantCols, wantRows int
		ok                 bool
	}{
		{"square in 2:1 cells", 114, 20, sq, 10, 20, 40, 20, true},
		{"width caps it", 80, 40, sq, 10, 20, 40, 20, true},
		{"unknown cells assume 1:2", 114, 20, sq, 0, 0, 40, 20, true},
		{"square cells", 114, 20, sq, 10, 10, 20, 20, true},
		{"wide image", 114, 20, image.Pt(600, 300), 10, 20, 57, 14, true},
		{"too short", 114, 7, sq, 10, 20, 0, 0, false},
		{"too narrow", 50, 20, sq, 10, 20, 0, 0, false},
	} {
		c, r, ok := artBox(tt.width, tt.rows, tt.size, tt.cellW, tt.cellH)
		if c != tt.wantCols || r != tt.wantRows || ok != tt.ok {
			t.Errorf("%s: artBox = %d×%d %v, want %d×%d %v", tt.name, c, r, ok, tt.wantCols, tt.wantRows, tt.ok)
		}
	}
}

// Ticks and other messages that cannot change the box send nothing.
func TestArtworkSyncIgnoresTicks(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	m, _ = press(t, m, "i")
	for _, msg := range []tea.Msg{tickMsg{}, seekTickMsg{}} {
		updated, cmd := m.Update(msg)
		m = updated.(Model)
		if raw := rawOf(msgsOf(cmd)); strings.Contains(raw, "\x1b_G") {
			t.Errorf("%T sent %q", msg, raw)
		}
	}
}

// A signal that never reaches Update leaves the image; main frees it.
func TestArtworkCleanup(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	if m.ArtworkCleanup() != "" {
		t.Error("cleanup before anything was sent")
	}
	m, _ = press(t, m, "i")
	if seq := m.ArtworkCleanup(); !strings.Contains(seq, "d=I") {
		t.Errorf("cleanup = %q", seq)
	}
}
