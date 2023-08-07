#!/bin/sh

# unzip all .zip files
for f in *.zip; do
    unzip "$f"
done