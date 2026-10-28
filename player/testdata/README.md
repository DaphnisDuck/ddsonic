# Test fixtures

## tone-440hz.mp3

A two-second 440 Hz sine tone, stereo, 44.1 kHz, 64 kbit/s MP3, 16,508 bytes. It was generated specifically for ddsonic's tests, with the command below. It is synthesized, not recorded, and contains no third-party sampled, musical or spoken material.

`nav_length_test.go` serves it over HTTP as "a real encoded MP3 both decoders accept": the built-in MP3 decoder and ffmpeg.

It was generated with ffmpeg n9.0.1 (libmp3lame):

```sh
ffmpeg -f lavfi -i "sine=frequency=440:duration=2:sample_rate=44100" \
    -ac 2 -c:a libmp3lame -b:a 64k \
    -map_metadata -1 -fflags +bitexact -flags:a +bitexact \
    -id3v2_version 0 -write_id3v1 0 \
    tone-440hz.mp3
```

SHA-256: `c670384b0bd9759b1d47dd2289958216c9e712084579458dbf450e6d0ceef74e`. The same command gives the same bytes with that ffmpeg; another version may give a different, equally valid file.

It replaces `cliamp_whips_terminal_ass.mp3`, which cliamp added in May 2026 (commit `bd8d5d07`) with no record of where the audio came from or of a license for it. ddsonic does not redistribute it.
