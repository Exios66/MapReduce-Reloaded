#!/usr/bin/env bash
# Install the Go release named by go/go.mod into a cache directory and print
# its bin directory, for CircleCI (.circleci/config.yml, docs/ci.md).
#
# The archive's SHA-256 is checked against go.dev's published release list,
# not against the download itself. A directory restored from a cache is used
# only if it reports exactly the wanted version; otherwise it is replaced.
#
# Usage: scripts/ci-install-go.sh [--version]
#   --version  print the wanted Go version and stop
# Environment: GO_DL_BASE (https only), GO_CACHE_DIR (default ~/.cache/go-toolchain)
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
GO_DL_BASE=${GO_DL_BASE:-https://go.dev/dl}
GO_CACHE_DIR=${GO_CACHE_DIR:-$HOME/.cache/go-toolchain}

# The toolchain line wins over the go line, as it does for the go command.
version=$(awk '$1 == "toolchain" {sub(/^go/, "", $2); t = $2} $1 == "go" {g = $2} END {print t != "" ? t : g}' "$REPO/go/go.mod")
case "$version" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) echo "ci-install-go: go/go.mod names '$version'; need a full version like 1.26.0" >&2; exit 1 ;;
esac
if [ "${1:-}" = --version ]; then
    echo "$version"
    exit 0
fi

case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "ci-install-go: unsupported CPU $(uname -m)" >&2; exit 1 ;;
esac
case "$GO_DL_BASE" in
    https://*) ;;
    *) echo "ci-install-go: GO_DL_BASE must be an https:// URL, not $GO_DL_BASE" >&2; exit 1 ;;
esac

dest="$GO_CACHE_DIR/go$version-$arch"
# Run through the dest's own go, so a go on the PATH cannot vouch for it.
if [ -x "$dest/bin/go" ] && [ "$(GOTOOLCHAIN=local "$dest/bin/go" env GOVERSION)" = "go$version" ]; then
    echo "$dest/bin"
    exit 0
fi

file="go$version.linux-$arch.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --proto '=https' --proto-redir '=https' -o "$tmp/$file" "$GO_DL_BASE/$file"
curl -fsSL --proto '=https' --proto-redir '=https' -o "$tmp/releases.json" "$GO_DL_BASE/?mode=json&include=all"
want=$(python3 -I - "$tmp/releases.json" "$file" <<'PY'
import json
import sys

releases = json.load(open(sys.argv[1]))
print(next(f["sha256"] for r in releases for f in r["files"] if f["filename"] == sys.argv[2]))
PY
)
got=$(sha256sum "$tmp/$file" | awk '{print $1}')
if [ -z "$want" ] || [ "$got" != "$want" ]; then
    echo "ci-install-go: checksum mismatch for $file: expected '$want', got $got" >&2
    exit 1
fi

# Unpack, then swap in, so a failure never leaves half a toolchain behind.
tar -C "$tmp" -xzf "$tmp/$file"
mkdir -p "$GO_CACHE_DIR"
rm -rf "$dest"
mv "$tmp/go" "$dest"
echo "$dest/bin"
