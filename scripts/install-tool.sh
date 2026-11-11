#!/bin/sh
# Installs a pinned Go tool into GOBIN, unless GOBIN already holds that exact
# build: `make tools` runs it once per pin (package@version).
#
# A binary is kept only if `go version -m` reports that it was built from
# the pinned package at the pinned module version. Anything else (missing,
# another version, another package, unreadable) is rebuilt with go install.
# CI restores .tools/bin from a cache, and go install rebuilds a tool from
# source whenever Go's build cache is cold, which took buf alone about two
# minutes per run; this check makes a cache hit skip that.
#
# Usage: GOBIN=/abs/dir scripts/install-tool.sh example.com/mod/cmd/tool@v1.2.3
set -eu
pin=${1:?usage: GOBIN=dir install-tool.sh package@version}
pkg=${pin%@*}
version=${pin##*@}
[ "$pkg" != "$pin" ] && [ -n "$pkg" ] && [ -n "$version" ] || { echo "install-tool.sh: want package@version, got $pin" >&2; exit 2; }
: "${GOBIN:?install-tool.sh: set GOBIN}"
bin="$GOBIN/${pkg##*/}"

if [ -x "$bin" ] && info=$(go version -m "$bin" 2>/dev/null); then
    # `path` is the main package; `mod` is its module and version.
    built_pkg=$(echo "$info" | awk '$1 == "path" { print $2; exit }')
    built_version=$(echo "$info" | awk '$1 == "mod" { print $3; exit }')
    if [ "$built_pkg" = "$pkg" ] && [ "$built_version" = "$version" ]; then
        echo "install-tool.sh: $bin is $pin already"
        exit 0
    fi
fi
echo "GOBIN=$GOBIN go install $pin"
go install "$pin"
