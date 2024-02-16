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

## Download assets

To download the assets, you need [git-lfs][git-lfs] installed.

On Linux, use your package manager to install it like:

```sh
> apt install git-lfs
```

On Mac, you can analogously run

```sh
> brew install git-lfs
```

After that, it should be fine to run

```sh
> git clone VoD source for DASH-IF livesim2.git
```

### Unpacking the content

You finally need to unpack the content.

ALl content can be unzipped using the top-level command

```sh
> unpack_all.sh
```

that iterates through the directories and unzips all zip files.

You can also unpack a single asset like `testpic_2s` using

```sh
> cd testpic_2s
> ../tools/unpack.sh
```

to unpack all the zip-files.
If you want to remove the `.zip` files, run `tools/unpack_and_clean.sh` instead.

You can remove the zip-files, but it may be good to keep them so that an update
via git only downloads new assets. After a git update, check if any zip-file has
been updated and run the unpack tool on that specific directory.

The asset should now be in a format that can be read and processed by `livesim2`,
provided that it is in a subdirectory under `--vodroot`.

## Assets

The assets available are:

### testpic_2s

A 1-hour long asset with 2s segments with 30fps video and many subtitle options.

The MPDs that can be played live are:

| Name | Specifics  | livesim2 status |
| ---- | ---------- | --------------- |
| Manifest.mpd | base simple MPD using SegmentTemplate with `$Number$`| OK |
| Manifest_avc3.mpd | same as Manifest.mpd but with avc3 codec signaling | Bad (init_avc3.mp4 missing) |
| Manifest_dvb.mpd | same as Manifest.mpd, but adding DVB profiles | OK |
| Manifest_stpp.mpd | Manifest.mpd extended with stpp subtitles | OK |
| Manifest_thumbs.mpd | Manifest.mpd extended with DASH-IF image subtitle track | OK |
| Manifest_trickmode.mpd | Manifest.mpd extended with DASH trick mode track | OK |
| audio.mpd | audio only | OK |
| cea608.mpd | video includes two CEA-608 caption tracks | OK |
| cea608_and_segs.mpd | same as cea608.mpd extended with stpp subtitle track | OK |
| multi_subs.mpd | Manifest.mpd extended with 5 subtitle and caption tracks in stpp format | OK |
| stpp_one_region.mpd | Manifest.mpd extended with `S1_one_region` stpp subtitles | OK |
| stpp_two_regions.mpd | Manifest.mpd extended with `S1_two_regions` stpp subtitles | OK |
| stpp_two_regions_multi_color.mpd | Manifest.mpd extended with `S1_two_regions_multi_colors` stpp subtitles | OK |

In addition, there is a couple of MPDs that can only be played as VoD because of side-loaded subtitle files or too short
subtitle track.

| Name | Specifics  | livesim2 status |
| ---- | ---------- | --------------- | 
| img_subs.mpd | Manifest.mpd extended with image subtitles in side-loaded TTML format (not supported by livesim) | N/A |
| ttml_subs.mpd | Manifest.mpd extended with side-loaded TTML files (1h) (not supported by livesim) | N/A |
| imsc1_img.mpd | Manifest.mpd with properly signaled IMSC-1 stpp image subtitles. Only one minute | Does not play |

### testpic_6s

A 1-hour long asset with 6s segments with 30fps video and two audio tracks.

The MPDs are:

| Name | Specifics  | livesim2 status |
| ---- | ---------- | --------------- |  
| Manifest.mpd | base simple MPD using SegmentTemplate with `$Number$`| OK |
| multiaudio.mpd | same as Manifest.mpd but with two audio tracks (the second is a copy of the first via a symbolic link) | OK |
| mixeddur.mpd | combination of 6s audio segments with 2s video segments (the latter from testpic_2s) | OK |

### testpic4_8s

A 1-hour long asset with 8s segments with 30fps video at three bitrates (300kbps, 600kbps, and 1200kbps) and MPDs that contain different subsets.

The MPDs are:

| Name | Specifics  | livesim2 status |
| ---- | ---------- | --------------- |  
| Manifest.mpd | base simple MPD using SegmentTemplate with `$Number$` and all bitrates | OK |
| Manifest_300.mpd | same as Manifest.mpd but limited to 300kbps video | OK |
| Manifest_600.mpd | same as Manifest.mpd but limited to 300kbps and 600kbs video | OK |

[livesim]: https://livesim.dashif.org
[livesim2]: https://github.com/Dash-Industry-Forum/livesim2
[git-lfs]: https://git-lfs.com
