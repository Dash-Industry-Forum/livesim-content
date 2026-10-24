# testrotate_2s generation resources

Assets used by `../gen.sh` to burn overlays into the video. They are only
needed at generation time, not for playback.

| File | Use | Source / License |
| ---- | --- | ---------------- |
| `RobotoSlab-Regular.ttf` | overlay labels (Bitrate, Resolution) | [Roboto Slab](https://fonts.google.com/specimen/Roboto+Slab), Apache License 2.0 |
| `Go-Mono.ttf` | fixed-width counters (clock, Frame) | "Go Mono" by Bigelow & Holmes for the Go project — see [`Go-Mono-LICENSE.txt`](Go-Mono-LICENSE.txt) |
| `logo.png` | rotating logo overlay | Eyevinn logo, from [moqlivemock](https://github.com/Eyevinn/moqlivemock) `utils/contentgen/resources` |

`Go-Mono.ttf` is used for the counters so the digits are monospaced and do not
shift horizontally as the values change.
