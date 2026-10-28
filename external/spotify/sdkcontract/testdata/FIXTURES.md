# Contract test fixtures

`tone.mp3.bin` is 0.3 s of a 440 Hz sine tone, stereo, 44.1 kHz, 64 kbit/s MP3.
It holds no recording of anything. It was generated once with:

    ffmpeg -f lavfi -i "sine=frequency=440:duration=0.3:sample_rate=44100" \
        -ac 2 -c:a libmp3lame -b:a 64k -f mp3 tone.mp3.bin

`ffmpeg` is not run by any test or make target. The `.bin` suffix keeps tools
from treating it as music; the `.txt` suffix on the Go files keeps anything in
this repository from compiling them. They are compiled only into a staged copy
of go-librespot, by the runner in this directory.
