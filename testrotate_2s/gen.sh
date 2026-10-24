#!/bin/bash
#
# gen.sh - Generate the "testrotate_2s" DASH VoD asset for livesim2.
#
# Based on the moqlivemock contentgen test content
# (utils/contentgen in the moqlivemock repo): a testsrc pattern with a
# rotating logo and burned-in text (bitrate, resolution, time, frame number),
# plus beep audio tracks. Here it is packaged as a livesim2 VoD asset with
# 2-second CMAF segments (init.mp4 + 1.m4s .. N.m4s per representation).
#
# Video (H.264/AVC, 25fps, closed 2s GOP, no B-frames):
#   V1200  1280x720  1200 kbps  (orange text)
#   V800    960x540   800 kbps  (yellow text)
#   V400    640x360   400 kbps  (white text)
# Audio (AAC-LC, 48kHz stereo, 64 kbps CBR via libfdk_aac):
#   Amon   monotonic 880Hz beep once per second   (lang "mon")
#   Asca   C-major scale, one note per second      (lang "sca")
#
# Usage:
#   ./gen.sh [DURATION_SECONDS]      (default 3600 = 1 hour)
#
# Requirements (binaries are taken from PATH; override with the env vars below):
#   FFMPEG      - ffmpeg with libx264 + drawtext + rotate, for video + segmenting
#                 (default: "ffmpeg" on PATH; override: FFMPEG=/path/to/ffmpeg)
#   FFMPEG_AAC  - ffmpeg with libfdk_aac, for the CBR audio encode
#                 (default: same as FFMPEG; override: FFMPEG_AAC=/path/to/ffmpeg)
#   AAC_CODEC   - AAC encoder (default: libfdk_aac; CBR when -b:a is set, no -vbr)
#   mp4ff-pslister on PATH (to read the avc1 codec string for the MPD)
#   go          - builds ./audioseg, which strips AAC priming/elst and
#                 re-segments audio onto the 2s grid (clean looping)
#
# If no single ffmpeg has every feature (e.g. one build has libx264+drawtext but
# not the non-free libfdk_aac), point the two env vars at different builds, e.g.:
#   FFMPEG=/opt/homebrew/opt/ffmpeg-full/bin/ffmpeg \
#   FFMPEG_AAC=/usr/local/bin/ffmpeg ./gen.sh

set -euo pipefail

DURATION="${1:-3600}"
FPS=25
SEG=2          # segment duration in seconds
AAC_RATE=48000 # audio sample rate
AAC_KBPS=64    # audio bitrate (kbps), CBR
# Encode an exact whole number of AAC frames (1024 samples) so there is no
# tiny trailing "flush" segment. For 120s and 3600s this is exact.
AAC_FRAMES=$(( DURATION * AAC_RATE / 1024 ))

FFMPEG="${FFMPEG:-ffmpeg}"
FFMPEG_AAC="${FFMPEG_AAC:-$FFMPEG}"
AAC_CODEC="${AAC_CODEC:-libfdk_aac}"

# Validate the chosen ffmpeg binaries up front with actionable errors.
# Capture the listing first (a piped `grep -q` would SIGPIPE ffmpeg, which under
# `set -o pipefail` would be misread as "feature missing"). $2: encoders|filters
ff_lists() {
  local out
  out=$("$1" -hide_banner "-$2" 2>/dev/null) || true
  grep -qw "$3" <<<"$out"
}
require() {
  command -v "$FFMPEG" >/dev/null 2>&1 || {
    echo "error: FFMPEG '$FFMPEG' not found on PATH. Set FFMPEG=/path/to/ffmpeg (needs libx264 + drawtext)." >&2; exit 1; }
  ff_lists "$FFMPEG" encoders libx264 || {
    echo "error: $FFMPEG has no libx264 encoder. Set FFMPEG to a build with libx264." >&2; exit 1; }
  ff_lists "$FFMPEG" filters drawtext || {
    echo "error: $FFMPEG has no drawtext filter (needs libfreetype). Set FFMPEG to a suitable build." >&2; exit 1; }
  command -v "$FFMPEG_AAC" >/dev/null 2>&1 || {
    echo "error: FFMPEG_AAC '$FFMPEG_AAC' not found on PATH. Set FFMPEG_AAC=/path/to/ffmpeg (needs $AAC_CODEC)." >&2; exit 1; }
  ff_lists "$FFMPEG_AAC" encoders "$AAC_CODEC" || {
    echo "error: $FFMPEG_AAC has no $AAC_CODEC encoder. Set FFMPEG_AAC to a build with $AAC_CODEC (or AAC_CODEC=aac for non-CBR audio)." >&2; exit 1; }
  command -v mp4ff-pslister >/dev/null 2>&1 || {
    echo "error: mp4ff-pslister not found on PATH (needed for the avc1 codec string)." >&2; exit 1; }
}
require

cd "$(dirname "$0")"
ASSET_DIR="$(pwd)"
FONT="resources/RobotoSlab-Regular.ttf"  # labels
MONO="resources/Go-Mono.ttf"             # counters (fixed width, no digit jitter)
LOGOPNG="resources/logo.png"
TMP="$(pwd)/.tmp"
rm -rf "$TMP"; mkdir -p "$TMP"

echo "Generating testrotate_2s, duration=${DURATION}s, ${SEG}s segments"

# ---------------------------------------------------------------------------
# Video representations: "RepID WIDTH HEIGHT BITRATE_KBPS TEXTCOLOR"
# ---------------------------------------------------------------------------
VIDEO_REPS=(
  "V1200 1280 720 1200 orange"
  "V800  960  540  800  yellow"
  "V400  640  360  400  white"
)

gen_video() {
  local rep=$1 w=$2 h=$3 br=$4 color=$5
  local fs sp mg bb logo y0 y1 y2 bigfs bx by bw bh tcx tcy
  fs=$((h/20)); sp=$((fs*14/10)); mg=$((h*3/100)); bb=$((fs/7))
  [ "$bb" -lt 2 ] && bb=2
  logo=$((h/2))
  y0=$mg; y1=$((mg+sp)); y2=$((mg+2*sp))
  # Big MM:SS.mmm clock that covers (hides) the testsrc seconds counter, which
  # is right-of-center and vertically centered. The opaque box spans the whole
  # counter region for up to 4 digits at all resolutions; the clock is centered
  # in it. The testsrc seconds counter stays underneath but is not visible.
  bigfs=$((h/6))
  bx=$((w*45/100)); by=$((h*31/100)); bw=$((w*48/100)); bh=$((h*36/100))
  tcx=$((w*69/100)); tcy=$((h*49/100))

  # Rotating logo (full turn in 10s) + burned-in text overlays. The angle is
  # wrapped with mod() so it stays in [0,2*PI): an unbounded angle (2*PI*n/250)
  # grows to thousands of radians and the rotate filter stops resolving distinct
  # angles (logo freezes ~angle 2042 rad, i.e. ~segment 1630 of a 1h asset).
  local vf period=$((FPS*10))
  vf="[1:v]scale=${logo}:${logo},format=rgba,rotate='2*PI*mod(n\\,${period})/${period}':c=none:ow=rotw(iw):oh=roth(ih)[logo];"
  vf="${vf}[0:v][logo]overlay=x=${mg}:y=main_h-overlay_h-${mg}:shortest=1[bg];"
  vf="${vf}[bg]drawtext=fontfile=${FONT}:text='Bitrate\\: ${br} kbps':fontcolor=${color}:fontsize=${fs}:box=1:boxcolor=black@0.5:boxborderw=${bb}:x=${mg}:y=${y0},"
  vf="${vf}drawtext=fontfile=${FONT}:text='Resolution\\: ${w} x ${h}':fontcolor=${color}:fontsize=${fs}:box=1:boxcolor=black@0.5:boxborderw=${bb}:x=${mg}:y=${y1},"
  vf="${vf}drawtext=fontfile=${MONO}:text='Frame\\: %{frame_num}':fontcolor=${color}:fontsize=${fs}:box=1:boxcolor=black@0.5:boxborderw=${bb}:x=${mg}:y=${y2},"
  vf="${vf}drawbox=x=${bx}:y=${by}:w=${bw}:h=${bh}:color=black:t=fill,"
  # Frame-based MM:SS;FF clock (integer math, exact per-frame, no ms float jitter),
  # in a fixed-width font so the centered digits never shift.
  vf="${vf}drawtext=fontfile=${MONO}:text='%{eif\\:floor(n/$((FPS*60)))\\:d\\:2}\\:%{eif\\:floor(mod(n\\,$((FPS*60)))/${FPS})\\:d\\:2};%{eif\\:mod(n\\,${FPS})\\:d\\:2}':fontcolor=${color}:fontsize=${bigfs}:x=${tcx}-text_w/2:y=${tcy}-text_h/2[v]"

  echo "  video ${rep} ${w}x${h} @ ${br}kbps ..."
  rm -rf "$rep"; mkdir -p "$rep"
  "$FFMPEG" -hide_banner -loglevel warning -y \
    -f lavfi -i "testsrc=size=${w}x${h}:rate=${FPS}:duration=${DURATION}:decimals=0" \
    -loop 1 -framerate ${FPS} -i "$LOGOPNG" \
    -filter_complex "$vf" -map "[v]" \
    -c:v libx264 -preset medium -profile:v main \
    -x264opts "keyint=$((FPS*SEG)):min-keyint=$((FPS*SEG)):scenecut=0:bframes=0:force-cfr=1" \
    -pix_fmt yuv420p -b:v "${br}k" -an \
    -force_key_frames "expr:gte(t,n_forced*${SEG})" \
    -f dash -seg_duration ${SEG} -use_template 1 -use_timeline 0 \
    -init_seg_name 'init.mp4' -media_seg_name '$Number$.m4s' -start_number 1 \
    "${rep}/manifest.mpd"
  rm -f "${rep}/manifest.mpd"
}

# ---------------------------------------------------------------------------
# Audio: build a 10s loopable beep wav, then loop+encode+segment per track.
# ---------------------------------------------------------------------------
# $1 = output wav, $2.. = 10 frequencies (Hz)
gen_beep_wav() {
  local out=$1; shift
  local freqs=("$@")
  "$FFMPEG" -hide_banner -loglevel error -y -f lavfi \
    -i "anullsrc=r=48000:cl=stereo:d=10" -c:a pcm_s16le "$TMP/base.wav"
  local inputs="-i $TMP/base.wav" amix="[0:0]"
  local i
  for i in {0..9}; do
    "$FFMPEG" -hide_banner -loglevel error -y -f lavfi \
      -i "sine=frequency=${freqs[$i]}:duration=0.5" \
      -af "volume=0.8,afade=t=out:st=0.3:d=0.2,adelay=$((i*1000))|$((i*1000))" \
      "$TMP/beep$i.wav"
    inputs="$inputs -i $TMP/beep$i.wav"
    amix="$amix[$((i+1)):0]"
  done
  # shellcheck disable=SC2086
  "$FFMPEG" -hide_banner -loglevel error -y $inputs \
    -filter_complex "${amix}amix=inputs=11:duration=longest:normalize=0" \
    -c:a pcm_s16le "$out"
}

gen_audio() {
  local rep=$1 wav=$2 lang=$3
  echo "  audio ${rep} (lang ${lang}) ..."
  rm -rf "$rep"
  # Loop the 10s wav and encode (AAC_FRAMES + margin) frames as one continuous
  # CBR stream, frag-every-frame (1 sample/fragment). libfdk_aac is CBR when
  # -b:a is given and -vbr is omitted. The margin covers the priming frames
  # that audioseg drops.
  "$FFMPEG_AAC" -hide_banner -loglevel warning -y \
    -stream_loop -1 -i "$wav" -frames:a $((AAC_FRAMES + 8)) \
    -c:a "$AAC_CODEC" -b:a "${AAC_KBPS}k" -ar "$AAC_RATE" -ac 2 \
    -movflags "cmaf+separate_moof+delay_moov+frag_every_frame" \
    -metadata:s:a:0 language="$lang" "$TMP/${rep}.mp4"
  # Drop priming + elst, keep exactly AAC_FRAMES content frames, and re-segment
  # onto the 2s grid (each segment starts at/after the matching video boundary,
  # tfdt anchored at 0). Total audio = AAC_FRAMES frames; exact for multiples
  # of 8s at 48kHz.
  go -C audioseg run . -in "$TMP/${rep}.mp4" -out "$ASSET_DIR/${rep}" \
    -frames "$AAC_FRAMES" -segsamples $((AAC_RATE * SEG))
}

# ---------------------------------------------------------------------------
# Generate media
# ---------------------------------------------------------------------------
# avc1 codec string from a representation's init.mp4 (bash 3.2: no assoc arrays)
vcodec_of() {
  mp4ff-pslister -i "$1/init.mp4" 2>/dev/null \
    | grep -i "Codecs parameter" | sed -E 's/.*: *//' | tr 'A-F' 'a-f'
}

for spec in "${VIDEO_REPS[@]}"; do
  # shellcheck disable=SC2086
  set -- $spec
  gen_video "$1" "$2" "$3" "$4" "$5"
done

gen_beep_wav "$TMP/mono.wav" 880 880 880 880 880 880 880 880 880 880
gen_beep_wav "$TMP/scale.wav" 261.63 293.66 329.63 349.23 392.00 440.00 493.88 523.25 587.33 659.25
gen_audio Amon "$TMP/mono.wav"  mon
gen_audio Asca "$TMP/scale.wav" sca

# ---------------------------------------------------------------------------
# Write Manifest.mpd
# ---------------------------------------------------------------------------
iso_dur() {
  local s=$1 h m sec out="PT"
  h=$((s/3600)); m=$(((s%3600)/60)); sec=$((s%60))
  [ "$h"   -gt 0 ] && out="${out}${h}H"
  [ "$m"   -gt 0 ] && out="${out}${m}M"
  [ "$sec" -gt 0 ] && out="${out}${sec}S"
  [ "$out" = "PT" ] && out="PT0S"
  echo "$out"
}
MPDDUR=$(iso_dur "$DURATION")

# Actual delivered bandwidth (bps) per audio rep, from segment bytes.
abw_of() { echo $(( $(cat "$1"/*.m4s | wc -c) * 8 / DURATION )); }
AMON_BW=$(abw_of Amon)
ASCA_BW=$(abw_of Asca)

{
cat <<EOF
<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns="urn:mpeg:dash:schema:mpd:2011" xsi:schemaLocation="urn:mpeg:dash:schema:mpd:2011 DASH-MPD.xsd" profiles="urn:mpeg:dash:profile:isoff-live:2011,http://dashif.org/guidelines/dash-if-simple" maxSegmentDuration="PT${SEG}S" minBufferTime="PT${SEG}S" type="static" mediaPresentationDuration="${MPDDUR}">
   <ProgramInformation moreInformationURL="https://github.com/dash-Industry-Forum/livesim-content">
      <Title>Rotating-logo testsrc, 720p/540p/360p AVC and beep audio with 2s segments</Title>
      <Source>VoD source for DASH-IF livesim2</Source>
   </ProgramInformation>
   <Period id="P0" start="PT0S">
      <AdaptationSet contentType="audio" mimeType="audio/mp4" lang="mon" segmentAlignment="true" startWithSAP="1">
         <Role schemeIdUri="urn:mpeg:dash:role:2011" value="main"/>
         <Label>Monotonic 880Hz beep</Label>
         <SegmentTemplate startNumber="1" initialization="\$RepresentationID\$/init.mp4" duration="${SEG}" media="\$RepresentationID\$/\$Number\$.m4s"/>
         <Representation id="Amon" codecs="mp4a.40.2" bandwidth="${AMON_BW}" audioSamplingRate="48000">
            <AudioChannelConfiguration schemeIdUri="urn:mpeg:dash:23003:3:audio_channel_configuration:2011" value="2"/>
         </Representation>
      </AdaptationSet>
      <AdaptationSet contentType="audio" mimeType="audio/mp4" lang="sca" segmentAlignment="true" startWithSAP="1">
         <Role schemeIdUri="urn:mpeg:dash:role:2011" value="alternate"/>
         <Label>C-major scale</Label>
         <SegmentTemplate startNumber="1" initialization="\$RepresentationID\$/init.mp4" duration="${SEG}" media="\$RepresentationID\$/\$Number\$.m4s"/>
         <Representation id="Asca" codecs="mp4a.40.2" bandwidth="${ASCA_BW}" audioSamplingRate="48000">
            <AudioChannelConfiguration schemeIdUri="urn:mpeg:dash:23003:3:audio_channel_configuration:2011" value="2"/>
         </Representation>
      </AdaptationSet>
      <AdaptationSet contentType="video" mimeType="video/mp4" segmentAlignment="true" startWithSAP="1" par="16:9" minWidth="640" maxWidth="1280" minHeight="360" maxHeight="720" maxFrameRate="${FPS}">
         <Role schemeIdUri="urn:mpeg:dash:role:2011" value="main"/>
         <SegmentTemplate startNumber="1" initialization="\$RepresentationID\$/init.mp4" duration="${SEG}" media="\$RepresentationID\$/\$Number\$.m4s"/>
EOF
for spec in "${VIDEO_REPS[@]}"; do
  # shellcheck disable=SC2086
  set -- $spec
  echo "         <Representation id=\"$1\" codecs=\"$(vcodec_of "$1")\" bandwidth=\"$(($4*1000))\" width=\"$2\" height=\"$3\" frameRate=\"${FPS}\" sar=\"1:1\"/>"
done
cat <<EOF
      </AdaptationSet>
   </Period>
</MPD>
EOF
} > Manifest.mpd

rm -rf "$TMP"
echo "Done. Wrote Manifest.mpd and representations: V1200 V800 V400 Amon Asca"
