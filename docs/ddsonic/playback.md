# Playback: known limitations

## macOS: a moment of old audio after a change made while paused

On macOS, three things done while playback is paused can begin with a moment of the old audio before the new track or position:

- starting another track (from the library or the queue);
- stopping, then playing a track;
- seeking, then resuming.

What has been heard: in testing on one Apple Silicon Mac, all three began with audible old audio, with local files (native and ffmpeg-decoded), a public YouTube URL, and a track change while paused on Spotify and YouTube Music. A plain pause and resume (radio included), a skip or a track change while playing, and a stop then play while playing were clean. On Linux, the same checks were clean.

What is inferred from the source and Apple's documentation, and not measured: macOS keeps the audio it has already queued for the output (about half of `buffer_ms`) while playback is paused, and ddsonic cannot clear it. So the old audio should last up to about half of `buffer_ms` (about 0.1 s at the default 250 ms), followed by a short silence of about the same length, then the new track or position. ([#83](https://github.com/DaphnisDuck/ddsonic/issues/83))

A lower `buffer_ms` in `config.toml` (or `--buffer-ms` for one run) should shorten it, at the risk of pops and dropouts on a busy machine; a higher one lengthens it. For example:

```toml
buffer_ms = 150
```

To avoid it without changing the buffer, resume first and then start the other track: a track change while playing was clean.
