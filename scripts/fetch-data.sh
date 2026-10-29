#!/bin/sh
# Fetches real datasets for the paper's example programs (§2.3) into
# data/input/, the shared storage every container sees at /data/input
# (ADR 0016). None is committed: they are 3 MB to 205 MB, and two are not
# ours to redistribute in the repository. See docs/data.md.
#
# Usage: scripts/fetch-data.sh [warpeace | nasa | webgoogle | commoncrawl | all]
#        (default all)
#
# - warpeace: War and Peace (Project Gutenberg #2600, via the GITenberg
#   mirror), 3.2 MB, for word count. As in fetch-corpus.sh, only the text
#   between the "*** START OF" line and the "End of ... Project Gutenberg"
#   line is kept. scripts/split-book.py then splits it into its 365
#   chapters, as files for invertedindex (warpeace-chapters/).
# - nasa: every HTTP request to NASA Kennedy Space Center's web server in
#   July 1995 (Internet Traffic Archive), 1.9 million lines in the Common
#   Log Format, 205 MB, for urlfreq. "The traces may be freely
#   redistributed."
# - webgoogle: the web graph of Google's 2002 programming contest (SNAP),
#   875,713 pages and 5,105,039 links, one per line as "from<TAB>to", for
#   reverselinks. SNAP's header lines, which start with "#", are dropped:
#   one holds a tab, which reverselinks would read as a link.
# - commoncrawl: one WET file of Common Crawl's February 2024 crawl, the text
#   of tens of thousands of web pages as crawled, for termvector, which the
#   paper ran on crawled pages (§2.3). scripts/wet-to-tsv.py makes each page
#   a line: its URL, a tab and its text. The pages are the unfiltered web,
#   in many languages, and are the sites' own; Common Crawl's terms of use
#   apply. Only aggregate results are recorded (docs/data.md).
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
COMMONCRAWL_URL=https://data.commoncrawl.org/crawl-data/CC-MAIN-2024-10/segments/1707947473347.0/wet/CC-MAIN-20240220211055-20240221001055-00000.warc.wet.gz
COMMONCRAWL_SHA256=7ab8c43bc1918d50ab52f8379cd31911705609d964c74acc680eb5f6960c2063

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
wet() { python3 "$root/scripts/wet-to-tsv.py"; }

what=${1:-all}
case $what in
warpeace | all) fetch warpeace warpeace.txt "$WARPEACE_URL" "$WARPEACE_SHA256" gutenberg ;;
nasa | webgoogle | commoncrawl) ;;
*)
    echo "usage: $0 [warpeace | nasa | webgoogle | commoncrawl | all]" >&2
    exit 2
    ;;
esac
# War and Peace's chapters, as files and as documents (scripts/split-book.py).
case $what in warpeace | all)
    if [ ! -d "$dest/warpeace-chapters" ]; then
        python3 "$root/scripts/split-book.py" "$dest/warpeace/warpeace.txt" "$dest"
    fi
    ;;
esac
case $what in nasa | all) fetch nasa access.log "$NASA_URL" "$NASA_SHA256" nasa ;; esac
case $what in webgoogle | all) fetch webgoogle links.tsv "$WEBGOOGLE_URL" "$WEBGOOGLE_SHA256" snap ;; esac
case $what in commoncrawl | all) fetch commoncrawl pages.tsv "$COMMONCRAWL_URL" "$COMMONCRAWL_SHA256" wet ;; esac
