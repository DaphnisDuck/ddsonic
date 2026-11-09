package player

import (
	"time"

	"github.com/gopxl/beep/v2/speaker"
)

// speakerSuspend and speakerResume are the device calls suspendSpeaker and
// resumeSpeaker make, and drainAfter schedules a suspend once the device has
// played out what it holds. They are Beep's own and the runtime's in
// production; tests replace them with a recorded output device and a timer
// they fire themselves, which needs no speaker.Init (#83).
var (
	speakerSuspend = speaker.Suspend
	speakerResume  = speaker.Resume
	drainAfter     = time.AfterFunc
)
