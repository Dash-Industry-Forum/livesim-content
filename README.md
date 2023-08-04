# livesim-content

This repo contains test content from the DASH-IF online service [https://livesim.dashif.org][livesim].
All content is DASH VoD assets in live profile.

The goal is to replace that service with a new more
restricted service base on [livesim2][livesim2] by
the end of October 2023, but also make it much simpler
to set up you own local or cloud service for testing.

As part of this transition, it is important that all relevant
test assets are available for download to an alternative
server.

There is various ways of doing that, but here we try to
make each asset easy to download from github by
employing a mixed storage format where each representation
directory with hundreds of files is made into an archive
(in zip format) while top-level files are kept separated.

This allows for simple changes to the MPDs, while making
the downloads more efficient. Since github has a file size
limit of 100MB, [git-lfs][git-lfs] is used.

The assets available are:

## testpic_6s

A 1-hour long asset with 6s segments with 30fps video and two audio tracks.

The MPDs are:

* Manifest.mpd - a simple MPD using SegmentTemplate with `$Number$``
* multiaudio.mpd - same as Manifest.mpd but with two audio tracks
* mixeddur.mpd - an MPD combining 6s audio segments with 2s video segments (the latter from testpic_2s)


[livesim]: https://livesim.dashif.org
[livesim2]: https://github.com/Dash-Industry-Forum/livesim2
[git-lfs]: https://git-lfs.com


