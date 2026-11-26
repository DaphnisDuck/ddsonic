package player

import "time"

// speakerSuspend, speakerResume and speakerDrop are the device calls
// suspendSpeaker and resumeSpeaker make, and drainAfter schedules a suspend
// once the device has played out what it holds. They are the output's own
// (speaker.go) and the runtime's in production; tests replace them with a
// recorded output device and a timer they fire themselves, which needs no
// speaker.Init (#83).
var (
	speakerSuspend = speaker.Suspend
	speakerResume  = speaker.Resume
	speakerDrop    = speaker.Drop
	drainAfter     = time.AfterFunc
)
