# contentgen

`contentgen` generates DASH VoD test assets for [livesim2][livesim2] from a
small YAML config. The config is kept next to the generated asset, so the
asset can always be regenerated, at any duration.

## What the content looks like

Video is the ffmpeg `testsrc2` pattern. Its moving, detailed content makes
the encoders use their bitrate, so the quality differs visibly between
representations. On top of it:

* labels: codec and bitrate, resolution, frame rate and frame structure
  (`IP`, `IPB`, or `RA` for AV1 random access), representation id, segment
  number, and frame number. They are made from the config values, so they
  always match the encoding.
* a large `HH:MM:SS:FF` timecode, computed from the frame number. At 29.97
  and 59.94 fps it is a SMPTE drop-frame timecode (`HH:MM:SS;FF`), which
  follows the clock.
* a rotating disc, one turn every 10 s
* the DASH-IF logotype at the bottom right, on a white plate, at most at its
  original size of 283x100 pixels
* a black square at the top right that is white for the first 100 ms of every
  second, from the first frame at or after the second

Audio has a 0.5 s tone every second, on top of a faint noise
floor (default −50 dBFS) so that the encoders use their bitrate. The tone is
either `beep` (880 Hz, 1760 Hz at every full minute) or `scale` (a C major
scale, one note per second). The tone starts with the white square, on the
first video frame at or after the second (which is at the second for integer
frame rates), so A/V sync is easy to check.

## Requirements

* Go 1.25 or later
* ffmpeg with `drawtext` (libfreetype), `geq` and `rotate`, plus the encoders
  of the config: `libx264` (avc), `libx265` (hevc), `libsvtav1` (av1),
  `aac`, `libopus` and `ac3`. Homebrew's `ffmpeg-full` has all of them.

If one ffmpeg lacks an encoder, for example the non-free `libfdk_aac`, give
that encoder in the config and point `-ffmpeg-audio` to a build that has it.

## Usage

```sh
> go -C tools/contentgen build
> tools/contentgen/contentgen -config myasset/contentgen.yaml
```

This writes the asset next to the config: `Manifest.mpd`, and one directory
per representation with `init.mp4` and the segments. Options:

| Option | Meaning |
| ------ | ------- |
| `-config` | the config file (required) |
| `-out` | output directory, default the directory of the config |
| `-duration` | duration in seconds, overriding the config |
| `-suggest` | print the durations near this many seconds that loop seamlessly, and exit |
| `-ffmpeg` | ffmpeg for video, default `$FFMPEG` or `ffmpeg` |
| `-ffmpeg-audio` | ffmpeg for audio, default `$FFMPEG_AUDIO` or the video one |
| `-keep` | keep the intermediate files in `<out>/.contentgen-tmp` |
| `-force` | overwrite representation directories and MPDs not made by contentgen |

When the output is not the config's directory, the settings that were used
are written to `<out>/contentgen.yaml`. Representation directories get a
`.contentgen` marker file; other directories, and MPDs without the
contentgen `Source`, are only overwritten with `-force`.

The [examples](examples) directory has configs for an AVC ladder with two
audio bitrates, two audio languages (a monotonic beep and rising tones), all
codecs, B-frames, SegmentTimeline with `$Number$`, and 29.97 and 59.94 fps.
They are about two minutes long; use `-duration` for other lengths. The asset
does not have to be long to measure latency: the timecode, and livesim2's
generated time subtitles, show the time. The config of the `testpic4_8s`
asset, [`testpic4_8s/contentgen.yaml`](../../testpic4_8s/contentgen.yaml), is
a full example with 8 s segments, 1 s GOPs and extra MPDs.

## Config

```yaml
name: ladder_2s            # used in the MPD title
title: "..."               # optional MPD title
duration: 3600             # seconds, a whole number of segments (e.g. 3587.584)
segmentDuration: 2         # seconds, a whole number of frames (e.g. 2.002)
addressing: number         # number | timeline-time | timeline-number
startNumber: 1             # default 1
minBufferTime: 2           # seconds, default the segment duration
mpdName: Manifest.mpd      # default Manifest.mpd
mpds:                      # optional extra MPDs with some of the representations
  - {name: Manifest400.mpd, title: "...", representations: [V400, A128]}
video:
  fps: 25                  # an integer, a ratio like 30000/1001, or 23.976, 29.97, 59.94
  gop: 1                   # seconds between keyframes, default the segment duration
  bframes: 0               # 0 for I and P frames only
  noise: 0                 # temporal noise 0-50 for even harder content
  representations:
    - {id: V400, codec: avc, width: 640, height: 360, bitrate: 400, color: white}
audio:
  priming: strip           # strip | elst
  representations:
    - {id: A128, codec: aac, bitrate: 128, signal: beep, lang: en, label: "..."}
```

`codec` is `avc`, `hevc` or `av1` for video, and `aac`, `opus` or `ac3` for
audio. An audio representation can also set `encoder` (an ffmpeg encoder,
e.g. `libfdk_aac`) and `noise` (dBFS, 0 for none). The default `label` is the
signal and codec, like `beep aac`.

The content is multi-bitrate: video representations of the same codec share
an Adaptation Set, and so do audio representations with the same codec,
signal, language and label. A player can then switch between their bitrates.
Give audio representations different labels to put them in separate
Adaptation Sets. The representations of an Adaptation Set must have the same
segment timing, which contentgen checks.

The main MPD has all representations. Each entry in `mpds` writes another MPD
with only the listed ones, for example a single bitrate, and its own title
(default the asset title). `maxWidth` and `maxHeight` follow the
representations of each MPD.

Every representation is a CMAF track, with the `cmfc` brand in its
`init.mp4`, so the MPDs signal the DASH profile for CMAF content
(`urn:mpeg:dash:profile:cmaf:2019`) besides the live profile. If all codecs
of an MPD are in DASH-IF IOP simple (AVC Main or High up to level 4.0, HEVC
Main up to level 4.1, and AAC), it also signals
`http://dashif.org/guidelines/dash-if-simple`. dashcheck then applies the
CMAF and IOP checks to the asset, and not only to low-latency output.

## Timing

* Each segment starts with a keyframe exactly on the segment grid, and the
  asset is an exact number of segments. Every video segment has the same
  structure. With `gop`, a segment has several closed GOPs; `gop` must divide
  the segment duration into whole frames.
* Video needs no edit list. The composition time offsets are shifted so that
  the first picture of a segment, which is first in decode order, has offset
  0, so the earliest presentation time of every segment is its `tfdt`, and
  the presentation starts at 0. With B-frames, the pictures that are
  presented before a picture decoded earlier get negative offsets, in `trun`
  version 1, as CMAF prefers. ffmpeg-based players show such video late by
  the largest negative offset, since ffmpeg shifts the presentation times to
  keep them after the decode times. AV1 has no reordering in the container,
  even in random-access mode.
* `trun` is version 0 when no offset is negative (audio, and video without
  B-frames), since CMAF does not allow version 1 together with an edit list.
* The video is converted to YUV with the BT.709 matrix, and the `colr` box of
  the sample entry signals BT.709, as CMAF requires for HEVC.
* Audio encoders add priming. With `priming: strip`, the whole priming frames
  are dropped and any rest (Opus 312, AC-3 256 samples) goes in an edit list.
  With `priming: elst`, all of it stays and is signalled with an edit list.
  `strip` needs no edit list for AAC, but the first decoded frame lacks its
  lead-in, so the first tone starts a few ms late.
* Every audio segment starts with the first frame at or after the start of
  the corresponding video segment. The audio loops seamlessly if the
  duration is a whole number of audio frames. That holds for any duration
  with Opus, multiples of 4 s with AC-3, and multiples of 8 s with AAC (or
  durations like 48 s with 1.92 s segments). contentgen warns otherwise, and
  `-suggest` lists the durations that work.
* With SegmentTimeline, the times are presentation times after the edit list,
  as livesim2 expects. With `$Time$`, the segment files are named by them.
  With an audio edit list, the first segment starts before 0, so it is
  listed from 0 with only its presented part as duration, as in the CTA WAVE
  content.
* `@bandwidth` is the lowest rate for which the DASH rule holds: delivered at
  that rate from any segment start, playout can start after `minBufferTime`
  without stalling. Since livesim2 loops the asset, it is at least the
  average rate.

## 29.97 and 59.94 fps

At 30000/1001 and 60000/1001 fps, a frame is 1001/30000 or 1001/60000 s, so a
whole number of seconds is a whole number of frames only every 1001 s. The
asset does not need whole seconds, though: livesim2 counts the loop in
milliseconds. Use segments of whole frames that are whole milliseconds, like
2.002 s (60 or 120 frames), and a duration that is a whole number of
segments and audio frames. `-suggest` finds these:

```sh
> contentgen -config examples/ntsc_2s.yaml -suggest 3600
Durations that are whole segments, video frames and milliseconds, and audio frames of aac, at 30000/1001 fps with 2.002 s segments:
  shortest: 64.064 s (1:04.064, 32 segments)
  near 3600 s: 3587.584 s (59:47.584, 1792 segments), 3651.648 s (1:00:51.648, 1824 segments)
```

Good settings with 2.002 s segments, at 29.97 or 59.94 fps:

| Audio | Shortest loop | Close to an hour |
| ----- | ------------- | ---------------- |
| AAC | 64.064 s (32 segments) | 3587.584 s (1792 segments) |
| AC-3, or AAC and AC-3 | 32.032 s, or 64.064 s | 3587.584 s |
| Opus | 20.02 s (10 segments) | 3603.6 s (1800 segments) |
| AAC, Opus and AC-3 | 320.32 s (160 segments) | 3523.52 s (1760 segments) |

The video timescale is then 30000 or 60000, with a frame duration of 1001,
and the MPD has `frameRate="30000/1001"`. To show the absolute time on top of
the loop, add livesim2's generated time subtitles, like
`/livesim2/timesubsstpp_en/<asset>/Manifest.mpd`.

livesim2 does not yet read VoD content with SegmentTimeline and `$Number$`
([livesim2 #152](https://github.com/Dash-Industry-Forum/livesim2/issues/152)).

[livesim2]: https://github.com/Dash-Industry-Forum/livesim2
