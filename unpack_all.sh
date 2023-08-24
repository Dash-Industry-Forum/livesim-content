#!/bin/sh

# unzip all .zip files in all directories
for d in */; do
    cd "$d"
    for f in *.zip; do
        unzip "$f"
    done
    cd ..
done