#!/usr/bin/env bash
# Runs every Go fuzz target for FUZZTIME (default 20s) each, one at a time
# (make fuzz, #152). make check runs the targets only on their stored seeds;
# this explores new input. A failure stops it, and Go keeps the input that
# failed under the package's testdata/fuzz/, to be committed with the fix
# so make check runs it from then on.
set -euo pipefail
cd "$(dirname "$0")/../go"
time=${FUZZTIME:-20s}

# go test -list prints a package's target names, then its "ok" line.
# Listed first, so that set -e stops on a package that does not build
# rather than fuzzing only the targets of the others.
list=$(go test -list '^Fuzz' ./...)
targets=()
names=()
while read -r first second _; do
    case $first in
    Fuzz*) names+=("$first") ;;
    ok)
        for name in ${names[@]+"${names[@]}"}; do targets+=("$second $name"); done
        names=()
        ;;
    esac
done <<< "$list"
[ ${#targets[@]} -gt 0 ] || { echo "fuzz: no fuzz targets found" >&2; exit 1; }

for target in "${targets[@]}"; do
    read -r pkg name <<< "$target"
    echo "fuzz: $name in $pkg for $time"
    go test -run '^$' -fuzz "^$name\$" -fuzztime "$time" "$pkg"
done
echo "fuzz: ${#targets[@]} targets, $time each, found nothing"
