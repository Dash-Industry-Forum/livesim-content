# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `tools/elstpatch`: checks where a representation's presentation starts and fixes it with a CMAF edit list in `init.mp4`

### Removed

- testpic_2s/Manifest_avc3.mpd, since it defined the V300 Representation differently from the other MPDs of the asset,
  which livesim2 does not support. livesim2 will instead offer avc3 signaling as a URL option.

### Fixed

- testpic_2s and testpic_6s: edit lists in the video and audio `init.mp4` make the first picture start at time 0 and remove the AAC priming
- README file mess
- An extra backtick in testpic_2s/Manifest.mpd

## [1.0.0] - 2024-02-16

### Added

- First tagged release
- Updated README to describe the deployed livesim2 service and the representation metadata files.
- Updated ProgramInformation
- Updated compressed representation data files (json.gz) to livesim2 v 1.2.0.

[Unreleased]: https://github.com/Dash-Industry-Forum/livesim-content/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Dash-Industry-Forum/livesim-content/releases/tag/v1.0.0
