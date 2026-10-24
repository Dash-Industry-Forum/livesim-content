#!/bin/sh

# unzip all .zip files
for f in *.zip; do
    unzip "$f"
done

# run the postunpack.sh script of the asset, if any
if [ -f postunpack.sh ]; then
    sh postunpack.sh
fi