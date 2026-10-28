package model

// ddsonic: the info view's artwork stays in step with the terminal when
// writes are slow and the selection changes underneath it (review R8).

import (
	"fmt"
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// split separates a command's messages into the raw output and the write
// acknowledgements, so a test can hold the acknowledgements back.
func split(cmd tea.Cmd) (raw string, acks []tea.Msg, other []tea.Msg) {
	for _, msg := range msgsOf(cmd) {
		switch r := msg.(type) {
		case tea.RawMsg:
			raw += r.Msg.(string)
		case artworkWrittenMsg:
			acks = append(acks, msg)
		default:
			other = append(other, msg)
		}
	}
	return raw, acks, other
}

// While a write is on its way, nothing else is written: two writes in flight
// could reach the terminal in either order. Its acknowledgement brings the
// terminal to the latest state with one write.
func TestArtworkWritesOneAtATime(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	updated, cmd := m.Update(queueKey("i"))
	m = updated.(Model)
	_, _, loads := split(cmd)
	updated, cmd = m.Update(loads[len(loads)-1]) // the load finishes
	m = updated.(Model)
	raw, acks, _ := split(cmd)
	if !strings.Contains(raw, "f=100") || len(acks) != 1 {
		t.Fatalf("first write %q with %d acknowledgements", raw, len(acks))
	}
	held := acks[0]
	for _, size := range []struct{ w, h int }{{200, 60}, {160, 50}} { // rapid resizes
		updated, cmd = m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = updated.(Model)
		if raw, _, _ := split(cmd); strings.Contains(raw, "\x1b_G") {
			t.Errorf("resize to %dx%d wrote %q while a write was in flight", size.w, size.h, raw)
		}
	}
	updated, cmd = m.Update(held)
	m = updated.(Model)
	raw, acks, _ = split(cmd)
	_, _, cols, rows, _ := m.libInfoArtBox()
	if strings.Count(raw, "a=p") != 1 || !strings.Contains(raw, fmt.Sprintf("c=%d,r=%d", cols, rows)) || len(acks) != 1 {
		t.Errorf("after the acknowledgement wrote %q, want one placement at the final %dx%d", raw, cols, rows)
	}
}

// A queue replaced under the open view (an album that finished loading)
// shows the new track's artwork: sent at once when loaded, loaded when not.
func TestArtworkFollowsAQueueReplacedUnderTheView(t *testing.T) {
	for _, cached := range []bool{true, false} {
		t.Run(fmt.Sprintf("cached=%v", cached), func(t *testing.T) {
			f := &fakeArt{have: map[string]bool{"audio:I": true, "audio:new": true}}
			m := artModel(t, 120, 40, f)
			if cached { // seen in an earlier session of the view
				m.lib.art.keep("audio:new", &libArtImage{img: image.NewRGBA(image.Rect(0, 0, 300, 300)), send: "NEW-IMAGE"}, "")
			}
			m, _ = press(t, m, "i")
			if m.lib.art.sent != "audio:I" {
				t.Fatalf("setup: terminal holds %q", m.lib.art.sent)
			}
			m.replacePlaylist(tracksOf("new"))
			m.plCursor = 0
			updated, cmd := m.Update(tickMsg{}) // nothing a key or a load: the queue just changed
			m, raw := deliver(t, updated.(Model), cmd)
			if m.lib.art.sent != "audio:new" {
				t.Errorf("terminal holds %q, view shows audio:new (raw %q)", m.lib.art.sent, raw)
			}
			if cached && !strings.Contains(raw, "NEW-IMAGE") {
				t.Errorf("cached artwork not sent: %q", raw)
			}
		})
	}
}

// Quitting with a write in flight frees the image, and leaves the cleanup
// main runs after the program ends, in case the write lands after the free.
func TestQuitWithAWriteInFlight(t *testing.T) {
	f := &fakeArt{have: map[string]bool{"audio:I": true}}
	m := artModel(t, 120, 40, f)
	updated, cmd := m.Update(queueKey("i"))
	m = updated.(Model)
	_, _, loads := split(cmd)
	updated, _ = m.Update(loads[len(loads)-1]) // written; acknowledgement held
	m = updated.(Model)
	updated, cmd = m.Update(queueKey("q"))
	m = updated.(Model)
	if raw, _, _ := split(cmd); !strings.Contains(raw, "a=d") {
		t.Errorf("quitting wrote %q, want the image freed", raw)
	}
	if !strings.Contains(m.ArtworkCleanup(), "a=d") {
		t.Error("no cleanup left for after the program ends")
	}
}

// The decoded artworks kept are bounded; the one shown is never dropped.
func TestArtworkCacheIsBounded(t *testing.T) {
	a := &libArt{images: map[string]*libArtImage{}}
	shown := "key0"
	for i := range artCacheSize + 10 {
		a.keep(fmt.Sprint("key", i), &libArtImage{}, shown)
	}
	if len(a.images) != artCacheSize || len(a.loaded) != artCacheSize {
		t.Errorf("kept %d images (%d listed), want %d", len(a.images), len(a.loaded), artCacheSize)
	}
	if a.images[shown] == nil {
		t.Error("the artwork shown was dropped")
	}
	if a.images[fmt.Sprint("key", artCacheSize+9)] == nil {
		t.Error("the newest artwork was dropped")
	}
}
