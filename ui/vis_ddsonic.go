package ui

// ddsonic: the visualizer modes ddsonic offers. cliamp's modes stay in visModes,
// whole and in upstream's order; a hidden one is only kept out of reach.

// hiddenVisModes are built-in modes ddsonic does not offer: Logo draws cliamp's
// name.
var hiddenVisModes = map[VisMode]bool{VisLogo: true}

func visModeHidden(mode VisMode) bool { return hiddenVisModes[mode] }

// PublicVisModeNames returns the names of the built-in modes ddsonic offers, in
// cycle order. Unlike VisModeNames, a name's index is not its VisMode.
func PublicVisModeNames() []string {
	names := make([]string, 0, VisCount)
	for i := range VisCount {
		if !visModeHidden(i) {
			names = append(names, visModes[i].name)
		}
	}
	return names
}
