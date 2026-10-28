package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestTextEditorEditsAtRuneBoundaries(t *testing.T) {
	m := Model{}
	value := "a界b"

	m.editText("query", &value, tea.KeyPressMsg{Code: tea.KeyLeft})
	m.editText("query", &value, tea.KeyPressMsg{Code: tea.KeyLeft})
	m.editText("query", &value, tea.KeyPressMsg{Text: "!"})
	if value != "a!界b" {
		t.Fatalf("value after insertion = %q, want %q", value, "a!界b")
	}

	m.editText("query", &value, tea.KeyPressMsg{Code: tea.KeyDelete})
	if value != "a!b" {
		t.Fatalf("value after delete = %q, want %q", value, "a!b")
	}
	m.editText("query", &value, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if value != "ab" {
		t.Fatalf("value after backspace = %q, want %q", value, "ab")
	}
}

func TestTextEditorWordAndLineDeletion(t *testing.T) {
	m := Model{}
	value := "one two three"

	m.editText("query", &value, tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	if value != "one two " {
		t.Fatalf("value after Ctrl+W = %q, want %q", value, "one two ")
	}
	m.editText("query", &value, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if value != "" {
		t.Fatalf("value after Ctrl+U = %q, want empty", value)
	}
}
