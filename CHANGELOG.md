# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `testrotate_2s/gen.sh`: generates the testrotate_2s asset (rotating logo, AVC at 400/800/1200 kbps, two AAC beep
  tracks, 2 s segments). Only the generator is in the repo, not the media.
- The assets that livesim2.dashif.org serves but the repo lacked: `ads`, `dolby-ac4`, `mpeg-h_one-day-in-berlin`,
  `testpic_2s_av1`, `testpic_2s_low_delay`, `WAVE` and `testpic_2s/webvtt_subs.mpd`. The `dolby-ac4` duration and
  the `WAVE/av` codecs are fixed.
- `tools/check_assets.sh`: checks all unpacked assets with dashcheck and reports findings that are not in
  `tools/check_assets_known.txt`

### Changed

- `unpack_all.sh` unpacks zip files at any depth, as the new assets need. It, `tools/unpack.sh` and
  `tools/unpack_and_clean.sh` then run the asset's `postunpack.sh`, if any.

### Fixed

- `tools/contentgen`: B-frame video has negative composition time offsets instead of an edit list, `trun` is version 0
  where possible, the video is BT.709 with a `colr` box, and `@maxSegmentDuration` is rounded up, so that the content
  passes the CMAF checks
- MPDs that failed dashcheck: element order in `testpic_6s/mixeddur.mpd` and in the MPD of `testrotate_2s/gen.sh`,
  the language tag in `testpic_2s/img_subs.mpd`, and the missing DVB live profile in `testpic_2s/Manifest_dvb.mpd`
- dashcheck errors in content: the `trex` track ID of the testpic_2s `S1*` subtitle init segments, the profile of the
  side-loaded subtitle MPDs (full instead of live), and the MPEG-H MPD, which `postunpack.sh` fixes when unpacking

### Removed

- `V300/init_avc3.mp4` (and its `~` backup) from `testpic_2s/V300.zip`, an outdated avc3 init left from
  `Manifest_avc3.mpd`; the livesim2 URL option `inbandps_1` gives avc3

## [2.0.0] - 2026-10-22

### Added

- `tools/contentgen`: generates test assets from a YAML config, with AVC/HEVC/AV1 video, AAC/Opus/AC-3 audio, and `$Number$` or SegmentTimeline MPDs, also at 29.97 and 59.94 fps
- `tools/elstpatch`: checks where a representation's presentation starts and fixes it with a CMAF edit list in `init.mp4`

### Changed

- Representation data files (`*_data.json.gz`) regenerated in livesim2 format version 2, which adds the SAP grid of video,
  so newer livesim2 versions do not rebuild them at start. livesim2 v1.14.0 still reads them.

### Removed

- testpic_2s/Manifest_avc3.mpd, since it defined the V300 Representation differently from the other MPDs of the asset,
  which livesim2 does not support. livesim2 will instead offer avc3 signaling as a URL option.

### Fixed

- testpic_2s and testpic_6s: edit lists in the video and audio `init.mp4` make the first picture start at time 0 and remove the AAC priming
- README file mess
- An extra backtick in testpic_2s/Manifest.mpd
- The clone command in README, and how to set up and fetch LFS content

## [1.0.0] - 2024-02-16

### Added

- First tagged release
- Updated README to describe the deployed livesim2 service and the representation metadata files.
- Updated ProgramInformation
- Updated compressed representation data files (json.gz) to livesim2 v 1.2.0.

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim-content/compare/v2.0.0...HEAD
[2.0.0]: https://github.com/Dash-Industry-Forum/livesim-content/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/Dash-Industry-Forum/livesim-content/releases/tag/v1.0.0
