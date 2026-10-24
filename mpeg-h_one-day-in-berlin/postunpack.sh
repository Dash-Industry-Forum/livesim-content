#!/bin/sh
#
# Writes manifest.mpd from manifest.mpd.orig, the MPD as Fraunhofer IIS made
# it. The AudioChannelConfiguration of the audio AdaptationSet is moved before
# the SegmentTemplate, where the DASH schema wants it. unpack_all.sh and
# tools/unpack.sh run this after unzipping.

cd "$(dirname "$0")" || exit 1
awk '
/<SegmentTemplate/ { held = $0; next }
held != "" && /<AudioChannelConfiguration/ { print; print held; held = ""; next }
held != "" { print held; held = "" }
{ print }
END { if (held != "") print held }
' manifest.mpd.orig > manifest.mpd
