package model

// ddsonic: each library view's key table drives the gate, the key bar and the
// keymap overlay, so the bar lists exactly the keys that work (M7.3).

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/library"
)

// libViews builds a model in each library view.
func libViews(t *testing.T) map[string]Model {
	t.Helper()
	browse := newLibraryModel(testLibraryRoot())
	browse.lib.refresh = func(string) {}
	deep := libPress(t, libClone(browse), "j")
	deep = libPress(t, deep, "enter")

	queue := libPress(t, newLibraryModel(testLibraryRoot()), "tab")

	input, _ := newSearchModel(t)
	input = libPress(t, input, "/")
	input = typeText(t, input, "x")
	results := libPress(t, libClone(input), "enter")

	ordered := newLibraryModel(library.Menu("Music", library.Entry{Title: "Albums", Open: orderedLevel{backward: new(bool)}}))
	ordered = libPress(t, ordered, "enter")

	return map[string]Model{"Library": browse, "Library below the root": deep, "Queue": queue,
		"Library Search": input, "Search Results": results, "ordered list": ordered}
}

// libClone copies m with its own navigation stack, so keys sent to the copy
// leave m's view as it was.
func libClone(m Model) Model {
	m.lib.stack = slices.Clone(m.lib.stack)
	return m
}

// candidateKeys is every key cliamp binds, plus the letters and digits.
func candidateKeys() []string {
	set := map[string]bool{}
	for _, c := range commandRegistry {
		for _, k := range c.Keys {
			set[k] = true
		}
	}
	for r := 'a'; r <= 'z'; r++ {
		set[string(r)] = true
		set[strings.ToUpper(string(r))] = true
	}
	for r := '0'; r <= '9'; r++ {
		set[string(r)] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// keyMsg builds a key press whose String() is s.
func keyMsg(t *testing.T, s string) tea.KeyPressMsg {
	t.Helper()
	named := map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
		"delete": tea.KeyDelete, "space": tea.KeySpace}
	var msg tea.KeyPressMsg
	mod, base, _ := strings.Cut(s, "+")
	switch {
	case base != "" && (mod == "ctrl" || mod == "shift"):
		msg = keyMsg(t, base)
		msg.Text = ""
		if mod == "ctrl" {
			msg.Mod = tea.ModCtrl
		} else {
			msg.Mod = tea.ModShift
		}
	case named[s] != 0:
		msg = tea.KeyPressMsg{Code: named[s]}
	default:
		msg = tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
	if msg.String() != s {
		t.Fatalf("key %q builds %q", s, msg.String())
	}
	return msg
}

// The gate passes exactly the view's pass keys to cliamp; own keys and every
// other key stay with the library. The keys cliamp handles before the gate
// work only when listed.
func TestLibraryGateReadsKeyTable(t *testing.T) {
	for name, m := range libViews(t) {
		t.Run(name, func(t *testing.T) {
			v := m.libraryKeyView()
			pass := libKeySet(v.pass)
			for k := range libKeySet(v.own) {
				if pass[k] {
					t.Errorf("%q is both the library's and passed", k)
				}
			}
			for _, k := range candidateKeys() {
				if strings.HasPrefix(k, "shift+") && k != "shift+left" && k != "shift+right" &&
					k != "shift+up" && k != "shift+down" && k != "shift+tab" {
					continue
				}
				msg := keyMsg(t, k)
				switch k {
				case "ctrl+c":
					continue // quits everywhere, before any view
				case "ctrl+k", "ctrl+z":
					if got := m.libraryDropsGlobalKey(k); got == v.has(k) {
						t.Errorf("global %q dropped = %v, listed = %v", k, got, v.has(k))
					}
					continue
				}
				c := libClone(m)
				if _, handled := c.handleLibraryKey(msg); handled == pass[k] {
					t.Errorf("key %q: library handles = %v, table passes = %v", k, handled, pass[k])
				}
			}
		})
	}
}

func TestLibraryKeyBarListsTable(t *testing.T) {
	tests := []struct {
		view      string
		want, not []string
	}{
		{"Library", []string{"Move", "Open", "Search", "Queue", "Sync", "Quit", "Stop", "Prev, next", "Volume", "Hide keys"},
			[]string{"Back", "Order", "Help", "Info", "Shuffle", "Seek far"}},
		{"Library below the root", []string{"Back", "Quit"}, nil},
		{"ordered list", []string{"Order"}, []string{"Sync"}},
		{"Queue", []string{"Library", "Play", "Filter", "Seek", "Shuffle", "Repeat", "Play next", "Up next", "Remove",
			"Reorder", "Undo", "EQ", "Speed", "Info", "Lyrics", "Jump", "Quit", "Stop", "Volume", "Hide keys", "Prev, next", "Seek far"},
			[]string{"Help", "Favorite", "Open", "Sync", "Mono"}},
		{"Library Search", []string{"Results", "Close", "Clear"}, []string{"Stop", "Volume", "Move", "Hide keys"}},
		{"Search Results", []string{"Query", "Move", "Open", "Stop", "Quit", "Prev, next"}, []string{"Search", "Sync", "Seek far"}},
	}
	views := libViews(t)
	for _, tt := range tests {
		t.Run(tt.view, func(t *testing.T) {
			m := views[tt.view]
			bar, ok := m.libKeyBar(200)
			if !ok {
				t.Fatal("no key bar")
			}
			plain := ansi.Strip(bar)
			for _, w := range tt.want {
				if !barHas(plain, w) {
					t.Errorf("bar lacks %q:\n%s", w, plain)
				}
			}
			for _, n := range tt.not {
				if barHas(plain, n) {
					t.Errorf("bar lists %q:\n%s", n, plain)
				}
			}
			// The keymap overlay lists the same rows, in the same order.
			v := m.libraryKeyView()
			var want, got []string
			for _, r := range slices.Concat(v.own, v.pass) {
				want = append(want, r.label)
			}
			for _, e := range m.libraryKeymapEntries() {
				if !e.divider {
					got = append(got, e.action)
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("keymap entries %q, want %q", got, want)
			}
		})
	}
}

// No label joins keys or words with "/": it reads as the search key. "/"
// appears only as a key of its own (search, or the queue's filter).
func TestLibraryLabelsKeepSlashForSearch(t *testing.T) {
	for name, m := range libViews(t) {
		v := m.libraryKeyView()
		for _, r := range slices.Concat(v.own, v.pass) {
			if strings.Contains(r.label, "/") {
				t.Errorf("%s: label %q contains /", name, r.label)
			}
			for _, tok := range strings.Fields(r.keyLabel) {
				if strings.Contains(tok, "/") && tok != "/" {
					t.Errorf("%s: key label %q joins keys with /", name, r.keyLabel)
				}
			}
			if slices.Contains(strings.Fields(r.keyLabel), "/") && !slices.Contains(r.keys, "/") {
				t.Errorf("%s: %q shows / but / is not its key", name, r.keyLabel)
			}
		}
	}
}

// barHas reports whether label appears in the plain bar as a whole label.
func barHas(plain, label string) bool {
	return regexp.MustCompile(`(^|\s)` + regexp.QuoteMeta(label) + `(\s|$)`).MatchString(plain)
}

// The bar wraps within the frame, and the frame still fits the terminal.
func TestLibraryKeyBarFits(t *testing.T) {
	views := libViews(t)
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {56, 16}, {40, 12}} {
		for name, view := range views {
			t.Run(fmt.Sprintf("%dx%d/%s", size.w, size.h, name), func(t *testing.T) {
				m := libClone(view)
				m.width, m.height = size.w, size.h
				m.recomputeLayout()
				out := m.View().Content
				if got := lipgloss.Height(out); got > size.h {
					t.Fatalf("view height = %d, want <= %d\n%s", got, size.h, out)
				}
				for _, line := range strings.Split(out, "\n") {
					if got := lipgloss.Width(line); got > size.w {
						t.Fatalf("line width = %d, want <= %d: %q", got, size.w, line)
					}
				}
				bar, _ := m.libKeyBar(m.layout.panelWidth)
				if shown, all := strings.Count(bar, "\n")+1, len(m.libKeyBarLines(m.layout.panelWidth)); size.w >= 80 && shown != all {
					t.Errorf("bar shows %d of its %d rows", shown, all)
				}
				for _, line := range strings.Split(bar, "\n") {
					if !strings.Contains(out, line) {
						t.Errorf("frame lacks key bar line %q:\n%s", ansi.Strip(line), ansi.Strip(out))
					}
				}
			})
		}
	}
}

// ? and Ctrl+K open nothing on a library view; Ctrl+K still opens the keymap
// over a cliamp overlay.
func TestLibraryViewsHaveNoKeymapOverlay(t *testing.T) {
	for name, m := range libViews(t) {
		for _, k := range []string{"?", "ctrl+k"} {
			updated, _ := m.Update(keyMsg(t, k))
			if updated.(Model).keymap.visible {
				t.Errorf("%s: %q opened the keymap", name, k)
			}
		}
	}
	m := libViews(t)["Queue"]
	m = queuePress(m, "A")
	updated, _ := m.Update(keyMsg(t, "ctrl+k"))
	if !updated.(Model).keymap.visible {
		t.Error("Ctrl+K over the queue manager did not open the keymap")
	}
}

// r syncs only where the bar lists it: not in search results.
func TestLibrarySyncKeyOnlyWhereListed(t *testing.T) {
	for name, m := range libViews(t) {
		synced := 0
		c := libClone(m)
		c.lib.refresh = func(string) { synced++ }
		c.handleLibraryKey(keyMsg(t, "r"))
		listed := c.libraryKeyView().has("r") && name != "Queue" // the queue's r is repeat
		if (synced == 1) != listed {
			t.Errorf("%s: r synced %d times, listed as Sync %v", name, synced, listed)
		}
	}
}
