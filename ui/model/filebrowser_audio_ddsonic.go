package model

// ddsonic: the file browser (Local → Folders, and o) lists directories and
// the files the player can play (player.SupportedExts) by default, and leaves
// out the rest: covers, logs, cue sheets. Those were listed but could not be
// played, selected or added. f shows every file again, and back. The choice
// lasts for the session. It changes only what is listed: nothing is read
// beyond the directory itself, and dotfiles stay hidden either way (P-42).

import (
	"fmt"
	"maps"
)

// fbLeavesOut reports whether the listing leaves out a file that is not a
// directory, and counts it when it does.
func (m *Model) fbLeavesOut(isAudio bool) bool {
	if isAudio || m.fileBrowser.allFiles {
		return false
	}
	m.fileBrowser.hidden++
	return true
}

// fbToggleAllFiles switches between the audio-only listing and all files. It
// relists the directory, as moving into one does, so a filter is cleared; it
// keeps the selection, and the cursor on the same entry or, when that entry
// is no longer listed, on the nearest one above it.
func (m *Model) fbToggleAllFiles() {
	var near []string // the cursor's entry, then the ones above it
	for i := min(m.fileBrowser.cursor, m.fbCount()-1); i >= 0; i-- {
		near = append(near, m.fbEntry(i).path)
	}
	selected := maps.Clone(m.fileBrowser.selected)

	m.fileBrowser.allFiles = !m.fileBrowser.allFiles
	m.loadFBDir()

	maps.Copy(m.fileBrowser.selected, selected)
	at := make(map[string]int, len(m.fileBrowser.entries))
	for i, e := range m.fileBrowser.entries {
		at[e.path] = i
	}
	for _, path := range near {
		if i, ok := at[path]; ok {
			m.fileBrowser.cursor = i
			break
		}
	}
	m.fbMaybeAdjustScroll(m.fbVisible())
}

// fbModeLabel is the header's note of what the listing shows.
func (m *Model) fbModeLabel() string {
	switch {
	case m.fileBrowser.allFiles:
		return " (all files)"
	case m.fileBrowser.hidden > 0:
		return fmt.Sprintf(" (audio only, %d hidden)", m.fileBrowser.hidden)
	default:
		return " (audio only)"
	}
}

// fbToggleLabel is the help's label for f: what pressing it does.
func fbToggleLabel(m Model) string {
	if m.fileBrowser.allFiles {
		return "Audio only"
	}
	return "All files"
}
