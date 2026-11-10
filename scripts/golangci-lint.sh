#!/bin/sh
# Runs golangci-lint at the version pinned in scripts/vm-setup.sh, built by
# a Go release no older than this module's, so `make lint` (and the pre-push hook)
# give the same result on every machine.
#
# A golangci-lint built by an older Go cannot load a module that needs a
# newer one; it fails with "the Go language version ... used to build
# golangci-lint is lower than the targeted Go version". So, like CI
# (.circleci/continue-config.yml), the binary on PATH is used only if it
# reports the pinned version and was built by a Go release no older than
# this module's (`go env GOVERSION` in go/, after any GOTOOLCHAIN switch).
# Otherwise the pinned version is built once into .tools/bin (git-ignored)
# with that Go release, and used from there.
#
# Usage, from the repository root or go/: scripts/golangci-lint.sh run ./...
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
want=$(sed -n 's/^GOLANGCI_VERSION=//p' "$root/scripts/vm-setup.sh")
[ -n "$want" ] || { echo "golangci-lint.sh: no GOLANGCI_VERSION in scripts/vm-setup.sh" >&2; exit 1; }
gover=$(cd "$root/go" && go env GOVERSION)

# Prints the path of the first usable binary among the arguments, if any.
usable() {
    for bin in "$@"; do
        [ -x "$bin" ] || continue
        version=$("$bin" version 2>&1 || true)
        case "$version" in *"version ${want#v} "*) ;; *) continue ;; esac
        built=$(echo "$version" | sed -n 's/.*built with \(go[0-9.]*\).*/\1/p')
        [ -n "$built" ] || continue
        # The older of the two sorts first; usable if that is the module's.
        oldest=$(printf '%s\n%s\n' "$gover" "$built" | sort -V | head -n 1)
        if [ "$oldest" = "$gover" ]; then
            echo "$bin"
            return 0
        fi
    done
    return 1
}

local_bin="$root/.tools/bin/golangci-lint"
if ! bin=$(usable "$(command -v golangci-lint || true)" "$local_bin"); then
    echo "golangci-lint.sh: building golangci-lint $want with $gover into .tools/bin ..." >&2
    (cd "$root/go" && GOBIN="$root/.tools/bin" GOTOOLCHAIN="$gover+auto" go install \
        "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$want")
    bin=$(usable "$local_bin") || {
        echo "golangci-lint.sh: the build in .tools/bin does not report $want built with $gover or newer" >&2
        exit 1
    }
fi
exec "$bin" "$@"
