#!/bin/sh
# Checks, and optionally prepares, a Linux machine to build and run the
# MapReduce cluster. Written for the CS544 course VM (see docs/cluster-vm.md),
# but it works on any Linux machine.
#
#   scripts/vm-setup.sh            check only: report what is missing, change nothing
#   scripts/vm-setup.sh --install  also install Go and the Python venv, inside $HOME
#
# The script never uses sudo and never installs or reconfigures Docker. When
# something needs root, it prints the command for you to run instead.
set -eu

cd "$(dirname "$0")/.."
REPO=$(pwd)

MODE=check
case "${1:-}" in
    "" | --check) MODE=check ;;
    --install) MODE=install ;;
    *) echo "usage: $0 [--check | --install]" >&2; exit 2 ;;
esac

# Where Go is installed, and where its download and checksum list come from.
# GO_DL_BASE can point at a mirror (or a local test server).
GO_HOME="$HOME/.local/go"
GO_DL_BASE=${GO_DL_BASE:-https://go.dev/dl}
VENV="$REPO/python/.venv"

# --- Reporting helpers --------------------------------------------------------
# Each check adds one line to the summary printed at the end: OK, WARN (works,
# but worth knowing) or MISSING (must be fixed).
SUMMARY=""
MISSING=0
report() { # report STATUS "what" "detail"
    SUMMARY="$SUMMARY$(printf '%-8s %-10s %s' "$1" "$2" "$3")
"
    [ "$1" != MISSING ] || MISSING=$((MISSING + 1))
}

# version_ge A B: true if version A >= version B (e.g. 1.26.1 >= 1.26.0).
version_ge() {
    [ "$(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1)" = "$2" ]
}

# --- Machine ------------------------------------------------------------------
check_machine() {
    os=$(uname -s)
    case "$(uname -m)" in
        x86_64) GOARCH=amd64 ;;
        aarch64 | arm64) GOARCH=arm64 ;;
        *) GOARCH=unsupported ;;
    esac
    if [ "$os" = Linux ] && [ "$GOARCH" != unsupported ]; then
        report OK machine "Linux $(uname -m)"
    else
        report MISSING machine "need Linux on x86_64 or arm64, found $os $(uname -m)"
    fi

    # /proc/meminfo reports kB. The cluster's limits add up to about 3.8 GB.
    total_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
    avail_mb=$(awk '/^MemAvailable:/ {print int($2 / 1024)}' /proc/meminfo)
    if [ "$avail_mb" -ge 4096 ]; then
        report OK memory "${total_mb} MB total, ${avail_mb} MB available"
    else
        report WARN memory "${total_mb} MB total, only ${avail_mb} MB available (cluster needs ~3.8 GB)"
    fi

    # Images, Go, the venv and job data need a few GB.
    free_mb=$(df -Pk "$HOME" | awk 'NR == 2 {print int($4 / 1024)}')
    if [ "$free_mb" -ge 5120 ]; then
        report OK disk "${free_mb} MB free in \$HOME"
    else
        report WARN disk "only ${free_mb} MB free in \$HOME (5 GB recommended)"
    fi
}

check_git() {
    if command -v git >/dev/null 2>&1; then
        report OK git "$(git --version)"
    else
        report MISSING git "install git (sudo apt install git)"
    fi
}

# --- Docker -------------------------------------------------------------------
check_docker() {
    if ! command -v docker >/dev/null 2>&1; then
        report MISSING docker "docker is not installed"
        return
    fi
    if docker compose version >/dev/null 2>&1; then
        report OK compose "$(docker compose version --short 2>/dev/null)"
    else
        report MISSING compose "need Docker Compose v2 ('docker compose')"
    fi
    # Running without sudo needs membership of the docker group. A
    # "permission denied" error means the group is missing; anything else
    # usually means the Docker daemon is not running.
    if err=$(docker ps 2>&1 >/dev/null); then
        report OK docker "usable without sudo"
    elif echo "$err" | grep -qi 'permission denied'; then
        report MISSING docker "run: sudo usermod -aG docker \$USER, then log out and back in"
    else
        report MISSING docker "daemon not reachable: $(echo "$err" | head -n1)"
    fi
}

# --- Go -----------------------------------------------------------------------
# The required version is the 'go' line in go/go.mod, e.g. "go 1.26.0".
GO_WANT=$(awk '$1 == "go" {print $2; exit}' "$REPO/go/go.mod")

# Prefer our own install in ~/.local/go, then any go on PATH.
find_go() {
    if [ -x "$GO_HOME/bin/go" ]; then
        echo "$GO_HOME/bin/go"
    elif command -v go >/dev/null 2>&1; then
        command -v go
    fi
}

go_ok() {
    gobin=$(find_go)
    [ -n "$gobin" ] || return 1
    # 'go env GOVERSION' prints e.g. "go1.26.0". GOTOOLCHAIN=local stops a
    # newer go.mod from making the go command download a toolchain itself.
    have=$(GOTOOLCHAIN=local "$gobin" env GOVERSION 2>/dev/null | sed 's/^go//')
    [ -n "$have" ] && version_ge "$have" "$GO_WANT"
}

check_go() {
    if go_ok; then
        report OK go "$(GOTOOLCHAIN=local "$(find_go)" env GOVERSION) at $(find_go)"
    else
        report MISSING go "need Go $GO_WANT+ (run with --install)"
    fi
}

install_go() {
    go_ok && return 0
    [ "$GOARCH" != unsupported ] || { echo "cannot install Go on this CPU" >&2; return 1; }
    file="go$GO_WANT.linux-$GOARCH.tar.gz"
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT

    echo "downloading $file ..."
    curl -fsSL -o "$tmp/$file" "$GO_DL_BASE/$file"

    # The expected SHA-256 comes from go.dev's published list of releases,
    # not from the download itself, so a corrupted or tampered file is caught.
    curl -fsSL -o "$tmp/releases.json" "$GO_DL_BASE/?mode=json&include=all"
    want=$(python3 -I - "$tmp/releases.json" "$file" <<'EOF'
import json, sys
releases = json.load(open(sys.argv[1]))
print(next(f["sha256"] for r in releases for f in r["files"] if f["filename"] == sys.argv[2]))
EOF
    )
    got=$(sha256sum "$tmp/$file" | awk '{print $1}')
    if [ "$got" != "$want" ]; then
        echo "checksum mismatch for $file: expected $want, got $got" >&2
        return 1
    fi

    # Unpack next to the final location, then swap it in, so a failure never
    # leaves a half-written ~/.local/go behind.
    mkdir -p "$HOME/.local"
    tar -C "$tmp" -xzf "$tmp/$file"
    rm -rf "$GO_HOME"
    mv "$tmp/go" "$GO_HOME"
    echo "installed Go $GO_WANT in $GO_HOME"
    echo "add this line to ~/.bashrc:  export PATH=\"\$HOME/.local/go/bin:\$PATH\""
}

# --- Python -------------------------------------------------------------------
check_python() {
    if ! command -v python3 >/dev/null 2>&1; then
        report MISSING python "install Python 3.11+"
        return
    fi
    pyver=$(python3 -c 'import sys; print("%d.%d" % sys.version_info[:2])')
    if ! version_ge "$pyver" 3.11; then
        report MISSING python "need Python 3.11+, found $pyver"
        return
    fi
    # Debian/Ubuntu ship venv support as a separate package.
    if ! python3 -c 'import ensurepip, venv' >/dev/null 2>&1; then
        report MISSING python "Python $pyver lacks venv: sudo apt install python$pyver-venv"
        return
    fi
    report OK python "Python $pyver"

    if [ -x "$VENV/bin/python" ] && "$VENV/bin/python" -c 'import mapreduce_reloaded, pytest' 2>/dev/null; then
        report OK venv "$VENV"
    else
        report MISSING venv "project venv not set up (run with --install)"
    fi
}

install_venv() {
    if [ -x "$VENV/bin/python" ] && "$VENV/bin/python" -c 'import mapreduce_reloaded, pytest' 2>/dev/null; then
        return 0
    fi
    python3 -m venv "$VENV"
    "$VENV/bin/pip" install --quiet -e "$REPO/python[dev]"
    echo "created $VENV (activate with: source python/.venv/bin/activate)"
}

# --- Main ---------------------------------------------------------------------
check_machine
if [ "$MODE" = install ]; then
    install_go
    command -v python3 >/dev/null 2>&1 && install_venv
    echo
fi
check_git
check_docker
check_go
check_python

echo
echo "===== vm-setup summary ($MODE) ====="
printf '%s' "$SUMMARY"
echo "===================================="
if [ "$MISSING" -gt 0 ]; then
    echo "$MISSING item(s) missing; fix them and run this script again."
    exit 1
fi
echo "Ready."
