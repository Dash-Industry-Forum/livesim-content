#!/bin/sh

# zip all directories
for f in *; do
    if [ -h "$f" ]; then
        continue
    fi
    if [ -d "$f" ]; then
        zip -r ${f}.zip $f
    fi
done