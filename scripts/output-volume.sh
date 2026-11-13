#!/bin/sh
# Prints this checkout's output volume name and records it in
# deploy/compose/.env, which every `docker compose -f deploy/compose/...`
# reads, so Make, the bench scripts, the notebook and a command typed by hand
# all mount the same volume.
#
# Every checkout on a machine shares the Compose project `mapreduce`, so a
# volume named after the project would be shared too, and `make cluster-down`
# (down -v) in any checkout would delete another's write-ahead log and
# experiments. The name is the checkout's directory name and a hash of its
# path instead: stable for one checkout, different for two. A checkout that
# is moved gets a new, empty volume; the old one stays until removed by hand.
set -eu
root=$(cd "$(dirname "$0")/.." && pwd -P)
if command -v sha256sum >/dev/null 2>&1; then
    hash=$(printf '%s' "$root" | sha256sum)
else
    hash=$(printf '%s' "$root" | shasum -a 256)
fi
hash=$(printf '%s' "$hash" | cut -c1-8)
# Docker volume names allow [a-zA-Z0-9_.-]; anything else becomes _.
base=$(basename "$root" | tr -c 'A-Za-z0-9_.\n-' '_')
name="mapreduce_output_${base}_${hash}"

env="$root/deploy/compose/.env"
line="MR_OUTPUT_VOLUME=$name"
if [ "$(cat "$env" 2>/dev/null || true)" != "$line" ]; then
    printf '%s\n' "$line" > "$env"
fi
printf '%s\n' "$name"
