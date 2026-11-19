//go:build r12probe

package player

import (
	"testing"
	_ "unsafe" // go:linkname

	"github.com/ebitengine/oto/v3"
	_ "github.com/gopxl/beep/v2/speaker"
)

// SCRATCH: does beep's unexported oto player link from here?
//
//go:linkname beepOtoPlayer github.com/gopxl/beep/v2/speaker.player
var beepOtoPlayer *oto.Player

func TestR12LinknameScratch(t *testing.T) {
	t.Logf("beep speaker.player linked; nil before Init: %v", beepOtoPlayer == nil)
}
