package model

import "testing"

// testTitleIntro stands in for the intro cliamp scrolled through the terminal
// title, so upstream's tests of that code still run. It is 35 characters, the
// length their expectations were written for.
const testTitleIntro = "An intro, scrolling for the tests..."

func withTitleIntro(t *testing.T, intro string) {
	t.Helper()
	saved := defaultTerminalTitleIntroRunes
	defaultTerminalTitleIntroRunes = []rune(intro)
	t.Cleanup(func() { defaultTerminalTitleIntroRunes = saved })
}

// The terminal title reads ddsonic from the first frame: nothing scrolls
// through it at startup.
func TestTerminalTitleHasNoIntro(t *testing.T) {
	state := initialTerminalTitleState()
	if state.introActive {
		t.Error("the title starts with an intro")
	}
	if got := currentTerminalTitle(state, 80, terminalTitleStateValues(false, false)); got != "ddsonic" {
		t.Errorf("the first title = %q, want %q", got, "ddsonic")
	}
	advanceTerminalTitleState(&state, 80)
	values := terminalTitleValues{stateIcon: "▶", metadata: "Song - Artist"}
	if got, want := currentTerminalTitle(state, 80, values), "▶ Song - Artist | ddsonic"; got != want {
		t.Errorf("the playing title = %q, want %q", got, want)
	}
}
