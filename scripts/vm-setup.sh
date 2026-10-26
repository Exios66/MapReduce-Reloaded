#!/bin/sh
# Checks, and optionally prepares, a Linux machine to build and run the
# MapReduce cluster. Written for the course VM (see docs/cluster-vm.md),
# but it works on any Linux machine.
#
#   scripts/vm-setup.sh            check only: report what is missing, change nothing
#   scripts/vm-setup.sh --install  also install Go, the Python venv and the
#                                  tools make check uses, inside $HOME
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
# The linter make go-lint runs; keep in step with .github/workflows/ci.yml.
GOLANGCI_VERSION=v2.14.0
TOOLS_BIN="$HOME/.local/bin"

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

    # Each of the four workers is limited to one CPU (docs/cluster-vm.md).
    cpus=$(nproc)
    if [ "$cpus" -ge 4 ]; then
        report OK cpus "$cpus"
    else
        report WARN cpus "only $cpus; the four workers are meant to get one CPU each"
    fi

    # /proc/meminfo reports kB. The cluster's limits add up to about 3.7 GB
    # (4.2 GB with the monitoring profile) on a VM with 8 GB of RAM. The
    # course VM reports 7,940 MB, so warn below 7.5 GB.
    total_mb=$(awk '/^MemTotal:/ {print int($2 / 1024)}' /proc/meminfo)
    avail_mb=$(awk '/^MemAvailable:/ {print int($2 / 1024)}' /proc/meminfo)
    if [ "$total_mb" -ge 7500 ]; then
        report OK ram "${total_mb} MB total"
    else
        report WARN ram "only ${total_mb} MB total; the VM is meant to have 8 GB"
    fi
    if [ "$avail_mb" -ge 4096 ]; then
        report OK memory "${total_mb} MB total, ${avail_mb} MB available"
    else
        report WARN memory "${total_mb} MB total, only ${avail_mb} MB available (cluster needs ~3.7 GB)"
    fi

    # Images, Go, the venv and job data need a few GB. The VM's 35 GB disk
    # (a 33 GB root filesystem) is shared with the OS and other coursework,
    # so 5 GB free is the most to ask.
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

# Every cluster command in docs/cluster-vm.md is a make target, and the
# course VM does not ship with make.
check_make() {
    if command -v make >/dev/null 2>&1; then
        report OK make "$(make --version | head -1)"
    else
        report MISSING make "install make (sudo apt install make)"
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

# python_ok: true if python3 is 3.11+ with venv support. When it is not,
# check_python reports why.
python_ok() {
    python3 -c 'import sys, ensurepip, venv; sys.exit(sys.version_info < (3, 11))' >/dev/null 2>&1
}

install_venv() {
    if [ -x "$VENV/bin/python" ] && "$VENV/bin/python" -c 'import mapreduce_reloaded, pytest' 2>/dev/null; then
        return 0
    fi
    python3 -m venv "$VENV"
    "$VENV/bin/pip" install --quiet -e "$REPO/python[dev]"
    echo "created $VENV (activate with: source python/.venv/bin/activate)"
}

# --- Tools for make check -----------------------------------------------------
# The cluster runs without these; make check (docs/design/0002) needs them.
# They are WARN, not MISSING, so a machine that only runs the cluster is Ready.
golangci_ok() {
    command -v golangci-lint >/dev/null 2>&1 || [ -x "$TOOLS_BIN/golangci-lint" ]
}

check_devtools() {
    if golangci_ok; then
        report OK lint "golangci-lint"
    else
        report WARN lint "golangci-lint missing, needed by make check (run with --install)"
    fi
    if [ -x "$REPO/.tools/bin/buf" ]; then
        report OK proto "buf and the code generators in .tools/bin"
    else
        report WARN proto "code generators missing, needed by make check (run with --install)"
    fi
    # go test -race, in make go-test, needs cgo and so a C compiler.
    if command -v gcc >/dev/null 2>&1; then
        report OK gcc "$(gcc --version | head -1)"
    else
        report WARN gcc "no C compiler; make check's go test -race needs one (sudo apt install gcc)"
    fi
}

install_devtools() {
    go_ok || return 0
    gobin=$(find_go)
    if ! golangci_ok; then
        echo "installing golangci-lint $GOLANGCI_VERSION into $TOOLS_BIN ..."
        mkdir -p "$TOOLS_BIN"
        GOBIN="$TOOLS_BIN" GOTOOLCHAIN=local "$gobin" install \
            "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_VERSION"
        echo "add this line to ~/.bashrc if ~/.local/bin is not on PATH:  export PATH=\"\$HOME/.local/bin:\$PATH\""
    fi
    if [ ! -x "$REPO/.tools/bin/buf" ] && command -v make >/dev/null 2>&1; then
        echo "installing the code generators into .tools/bin ..."
        # Some pinned tools need a newer Go than go.mod's; as in CI, the go
        # command downloads that toolchain itself (GOTOOLCHAIN=auto).
        PATH="$(dirname "$gobin"):$PATH" make -C "$REPO" --no-print-directory tools
    fi
}

# --- Main ---------------------------------------------------------------------
check_machine
if [ "$MODE" = install ]; then
    install_go
    python_ok && install_venv
    install_devtools
    echo
fi
check_git
check_make
check_docker
check_go
check_python
check_devtools

echo
echo "===== vm-setup summary ($MODE) ====="
printf '%s' "$SUMMARY"
echo "===================================="
if [ "$MISSING" -gt 0 ]; then
    echo "$MISSING item(s) missing; fix them and run this script again."
    exit 1
fi
echo "Ready."
