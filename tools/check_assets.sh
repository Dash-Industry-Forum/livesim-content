#!/bin/sh
#
# check_assets.sh checks the MPDs of all unpacked assets and all their
# segments with dashcheck (https://github.com/tobbee/dashcheck), and compares
# the errors and warnings with the known ones in check_assets_known.txt.
#
# It is a manual check. Run unpack_all.sh first. A CI job would download all
# the LFS content on every run.
#
# Usage: tools/check_assets.sh [-update]
#   -update  write the current findings to check_assets_known.txt
#
# DASHCHECK sets the dashcheck binary (default: dashcheck on the PATH).
# The exit code is 1 if there are findings that are not known, else 0.

set -eu

DASHCHECK="${DASHCHECK:-dashcheck}"
cd "$(dirname "$0")/.."
KNOWN=tools/check_assets_known.txt

command -v "$DASHCHECK" >/dev/null 2>&1 || {
    echo "error: $DASHCHECK not found. Install it with" >&2
    echo "  go install github.com/tobbee/dashcheck/cmd/dashcheck@latest" >&2
    echo "or set DASHCHECK=/path/to/dashcheck." >&2
    exit 2
}

# Every zip file must be unpacked, or dashcheck reports missing segments.
missing=$(find . -name .git -prune -o -name '*.zip' -type f -print | sort | while read -r z; do
    first=$(unzip -Z1 "$z" | head -1)
    [ -e "$(dirname "$z")/$first" ] || echo "  $z"
done)
if [ -n "$missing" ]; then
    echo "error: these zip files are not unpacked (run unpack_all.sh):" >&2
    echo "$missing" >&2
    exit 2
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

mpds=$(find . -name .git -prune -o -name tools -prune -o -name '*.mpd' -type f -print | sed 's#^\./##' | sort)
"$DASHCHECK" -version 2>&1 | head -1
# shellcheck disable=SC2086
"$DASHCHECK" -short -all -level warning $mpds > "$tmp/report.txt" 2>&1 || true
tail -1 "$tmp/report.txt"

# One line per finding: MPD, severity, rule.
awk '/^(OK|WARN|FAIL) / {mpd = $2; next}
     /^ +(error|warning) / {rule = $2; sub(/:$/, "", rule); print mpd, $1, rule}' \
    "$tmp/report.txt" | sort -u > "$tmp/current.txt"
grep -v '^#' "$KNOWN" | sed '/^$/d' | sort -u > "$tmp/known.txt"

if [ "${1:-}" = "-update" ]; then
    {
        sed -n '/^#/p' "$KNOWN"
        cat "$tmp/current.txt"
    } > "$tmp/new_known.txt"
    mv "$tmp/new_known.txt" "$KNOWN"
    echo "Wrote $(wc -l < "$tmp/current.txt" | tr -d ' ') findings to $KNOWN"
    exit 0
fi

comm -13 "$tmp/known.txt" "$tmp/current.txt" > "$tmp/new.txt"
comm -23 "$tmp/known.txt" "$tmp/current.txt" > "$tmp/gone.txt"
if [ -s "$tmp/gone.txt" ]; then
    echo "Known findings that are gone (remove them from $KNOWN):"
    sed 's/^/  /' "$tmp/gone.txt"
fi
if [ -s "$tmp/new.txt" ]; then
    echo "New findings:"
    sed 's/^/  /' "$tmp/new.txt"
    echo "Details:"
    cut -f1 -d' ' "$tmp/new.txt" | sort -u | while read -r mpd; do
        "$DASHCHECK" -short -all -level warning "$mpd" 2>&1 | sed -n '1,/^$/p' | sed '/^$/d'
    done
    exit 1
fi
echo "No new findings."
