#!/bin/sh

# unzip and remove all .zip files
for f in *.zip; do
    unzip "$f"
    rm "$f"
done