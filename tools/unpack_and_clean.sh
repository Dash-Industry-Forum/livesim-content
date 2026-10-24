#!/bin/sh

# unzip and remove all .zip files
for f in *.zip; do
    unzip "$f"
    rm "$f"
done

# run the postunpack.sh script of the asset, if any
if [ -f postunpack.sh ]; then
    sh postunpack.sh
fi