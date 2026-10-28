package ipc

// ddsonic: the names the shuffle, repeat and mono operations accept. Upstream
// toggled or cycled on any other name, so a typo changed the setting.

import "slices"

var modeNames = map[string][]string{
	"shuffle": {"", "on", "off", "toggle"},
	"repeat":  {"", "off", "all", "one", "cycle"},
	"mono":    {"", "on", "off", "toggle"},
}

// ModeNames lists the names op accepts besides the empty one, which toggles
// or cycles.
func ModeNames(op string) []string {
	return modeNames[op][1:]
}

// ValidModeName reports whether op accepts name. Operations without a fixed
// set of names accept any.
func ValidModeName(op, name string) bool {
	names, ok := modeNames[op]
	return !ok || slices.Contains(names, name)
}
