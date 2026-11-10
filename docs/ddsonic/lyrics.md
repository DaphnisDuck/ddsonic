# Lyrics: the NetEase lookup

ddsonic looks lyrics up on LRCLIB, over HTTPS (see [docs/lyrics.md](../lyrics.md) for the rest). Upstream cliamp also tries NetEase Cloud Music whenever the LRCLIB lookup fails, with an error or with no result. ddsonic does not, unless you turn it on in `config.toml`:

```toml
[ddsonic]
netease_lyrics = true
```

With it on, ddsonic asks NetEase after any LRCLIB lookup that fails: when LRCLIB answers with an error, cannot be reached, or has no lyrics for the track.

**Privacy.** The NetEase lookup uses plain HTTP, not HTTPS. The track's artist and title are sent to music.163.com unencrypted, and the lyrics come back the same way: anyone on the network path, such as a public Wi-Fi operator or your network provider, can see which song you are playing and could change the lyrics shown. Leave the setting off (the default) to keep lyrics lookups encrypted.
