# Decoder test fixtures

Each file is 0.3 s of a 440 Hz sine tone, stereo, 44.1 kHz: no recording of
anything. They were generated once with:

    ffmpeg -f lavfi -i "sine=frequency=440:duration=0.3:sample_rate=44100" -ac 2 -c:a libmp3lame -b:a 64k -f mp3 tone.mp3.bin
    ffmpeg -f lavfi -i "sine=frequency=440:duration=0.3:sample_rate=44100" -ac 2 -c:a flac -f flac tone.flac.bin
    ffmpeg -f lavfi -i "sine=frequency=440:duration=0.3:sample_rate=44100" -ac 2 -c:a libvorbis -q:a 3 -f ogg tone.ogg.bin

`tone.mp3.bin` is the same file as `../sdkcontract/testdata/tone.mp3.bin`.
`ffmpeg` is not run by any test or make target. The `.bin` suffix keeps tools
from treating them as music.
