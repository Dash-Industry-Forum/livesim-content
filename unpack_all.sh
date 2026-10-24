#!/bin/sh

# unzip all .zip files at any depth, each in its own directory
find . -name .git -prune -o -name '*.zip' -type f \
    -exec sh -c 'cd "$(dirname "$1")" && unzip "$(basename "$1")"' sh {} \;

# run the postunpack.sh scripts of the assets
find . -name .git -prune -o -name postunpack.sh -type f -exec sh {} \;
