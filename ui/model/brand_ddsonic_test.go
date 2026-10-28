// ddsonic: tests for the header's title.

package model

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/internal/appmeta"
	"github.com/bjarneo/cliamp/ui"
)

// The header reads "🦆 ddsonic <version>": the name in the title style, the
// version, as the build reports it, dimmed after one space.
func TestBrandTitle(t *testing.T) {
	old := appmeta.Version()
	t.Cleanup(func() { appmeta.SetVersion(old) })
	for _, version := range []string{"v1.0.0", "v1.0.0-rc.1", "v1.0.0-rc.1-8-g89954dc", "dev"} {
		appmeta.SetVersion(version)
		got := brandTitle(80)
		if plain, want := ansi.Strip(got), "🦆 ddsonic "+version; plain != want {
			t.Errorf("version %q: title reads %q, want %q", version, plain, want)
		}
		if want := titleStyle.Render("🦆 ddsonic") + " " + dimStyle.Render(version); got != want {
			t.Errorf("version %q: title is styled %q, want the name in titleStyle and the version in dimStyle, %q", version, got, want)
		}
	}
}

// When the row is too narrow for both, the version goes and the name stays.
func TestBrandTitleDropsVersionWhenNarrow(t *testing.T) {
	old := appmeta.Version()
	t.Cleanup(func() { appmeta.SetVersion(old) })
	appmeta.SetVersion("v1.0.0")
	full := lipgloss.Width(brandTitle(80)) // "🦆 ddsonic v1.0.0"
	for _, tt := range []struct {
		room int
		want string
	}{
		{full, "🦆 ddsonic v1.0.0"},
		{full - 1, "🦆 ddsonic"},
		{4, "🦆 ddsonic"}, // the name is never cut here
	} {
		if got := ansi.Strip(brandTitle(tt.room)); got != tt.want {
			t.Errorf("room %d: title reads %q, want %q", tt.room, got, tt.want)
		}
	}
}

// The header row: the title on the left, the screen's label on the right,
// and the version dropped first when the two would not fit.
func TestRenderTitleKeepsNameAndLabel(t *testing.T) {
	old, oldWidth := appmeta.Version(), ui.PanelWidth
	t.Cleanup(func() { appmeta.SetVersion(old); ui.PanelWidth = oldWidth })
	appmeta.SetVersion("v1.0.0-rc.1-8-g89954dc")
	m := newLibraryModel(testLibraryRoot())

	ui.PanelWidth = 100
	wide := ansi.Strip(m.renderTitle())
	if !strings.HasPrefix(wide, "🦆 ddsonic v1.0.0-rc.1-8-g89954dc ") || !strings.HasSuffix(wide, "]") || lipgloss.Width(wide) != 100 {
		t.Errorf("wide header = %q (width %d)", wide, lipgloss.Width(wide))
	}

	ui.PanelWidth = 30
	narrow := ansi.Strip(m.renderTitle())
	if !strings.HasPrefix(narrow, "🦆 ddsonic ") || strings.Contains(narrow, "v1.0.0") || !strings.HasSuffix(narrow, "]") || lipgloss.Width(narrow) != 30 {
		t.Errorf("narrow header = %q (width %d)", narrow, lipgloss.Width(narrow))
	}
}
