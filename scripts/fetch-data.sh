#!/bin/sh
# Fetches real datasets for the paper's example programs (§2.3) into
# data/input/, the shared storage every container sees at /data/input
# (ADR 0016). None is committed: they are 3 MB to 205 MB, and two are not
# ours to redistribute in the repository. See docs/data.md.
#
# Usage: scripts/fetch-data.sh [warpeace | nasa | webgoogle | all]  (default all)
#
# - warpeace: War and Peace (Project Gutenberg #2600, via the GITenberg
#   mirror), 3.2 MB, for word count. As in fetch-corpus.sh, only the text
#   between the "*** START OF" line and the "End of ... Project Gutenberg"
#   line is kept.
# - nasa: every HTTP request to NASA Kennedy Space Center's web server in
#   July 1995 (Internet Traffic Archive), 1.9 million lines in the Common
#   Log Format, 205 MB, for urlfreq. "The traces may be freely
#   redistributed."
# - webgoogle: the web graph of Google's 2002 programming contest (SNAP),
#   875,713 pages and 5,105,039 links, one per line as "from<TAB>to", for
#   reverselinks. SNAP's header lines, which start with "#", are dropped:
#   one holds a tab, which reverselinks would read as a link.
#
# Each download's SHA-256 sum is checked against the pin below, so a
# changed file upstream fails here instead of changing results. A dataset
# already fetched is not fetched again.
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dest=$root/data/input

WARPEACE_URL=https://raw.githubusercontent.com/GITenberg/War-and-Peace_2600/master/2600.txt
WARPEACE_SHA256=31b5d23be25fe9ad27eca1e78f9f449ae2e17adf07ce62238a79425c53a96646
NASA_URL=https://ita.ee.lbl.gov/traces/NASA_access_log_Jul95.gz
NASA_SHA256=199109ed0f273e095da6ccd5fc9dc4cd8bb58daa06d62135e62090fea9d27488
WEBGOOGLE_URL=https://snap.stanford.edu/data/web-Google.txt.gz
WEBGOOGLE_SHA256=bcac0af0471d749f4a8c010bca92b61cf2868a0570741de06892fc062f265ea6

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

tmp=$(mktemp)
trap 'rm -f "$tmp" "$tmp.out"' EXIT

# download URL SHA256: fetches URL into $tmp and checks its sum.
download() {
    echo "fetch-data: downloading $1"
    curl -fsSL --proto '=https' --proto-redir '=https' -o "$tmp" "$1"
    got=$(sha256 "$tmp")
    if [ "$got" != "$2" ]; then
        echo "fetch-data: $1 has SHA-256 $got, want $2" >&2
        exit 1
    fi
}

# fetch NAME FILE URL SHA256 FILTER: writes data/input/NAME/FILE from the
# download, through FILTER, unless it is there.
fetch() {
    out=$dest/$1/$2
    if [ -f "$out" ]; then
        echo "fetch-data: $out is already there"
        return 0
    fi
    download "$3" "$4"
    mkdir -p "$dest/$1"
    # Filter to a temporary file first: in a pipeline, sh would only see the
    # last command's exit status, and a half-written file would look done.
    $5 <"$tmp" >"$tmp.out"
    mv "$tmp.out" "$out"
    echo "fetch-data: wrote $out ($(wc -c <"$out" | tr -d ' ') bytes)"
}

gutenberg() { awk '/^\*\*\* *START OF/ { on = 1; next } /^End of .*Project Gutenberg/ { on = 0 } on'; }
nasa() { gzip -dc; }
snap() { gzip -dc | grep -v '^#'; }

what=${1:-all}
case $what in
warpeace | all) fetch warpeace warpeace.txt "$WARPEACE_URL" "$WARPEACE_SHA256" gutenberg ;;
nasa | webgoogle) ;;
*)
    echo "usage: $0 [warpeace | nasa | webgoogle | all]" >&2
    exit 2
    ;;
esac
case $what in nasa | all) fetch nasa access.log "$NASA_URL" "$NASA_SHA256" nasa ;; esac
case $what in webgoogle | all) fetch webgoogle links.tsv "$WEBGOOGLE_URL" "$WEBGOOGLE_SHA256" snap ;; esac
