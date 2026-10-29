#!/bin/sh
# Fetches real datasets for the paper's example programs (§2.3) into
# data/input/, the shared storage every container sees at /data/input
# (ADR 0016). None is committed: they are 3 MB to 205 MB, and two are not
# ours to redistribute in the repository. See docs/data.md.
#
# Usage: scripts/fetch-data.sh [warpeace | nasa | webgoogle | commoncrawl | crawl | all]
#        (default all, which fetches every dataset but crawl: that one is
#        E6's 1.08 GB download, so it is opt-in)
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
# - crawl: the first ten WET files of the same crawl segment, the first
#   being commoncrawl's, 1.08 GB as downloaded, kept compressed as they
#   are. bench/gen-crawl.py reads them for E6, word count over about 2.9 GB
#   of crawled text, in every language or one. Not part of all: E6 only.
#
# Each download's SHA-256 sum is checked against the pin below, so a
# changed file upstream fails here instead of changing results. A dataset
# already fetched is not fetched again, unless it changed since: each
# file's own SHA-256 is kept next to it as FILE.sha256 and checked.
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
# The crawl target's files: NAME SHA-256, each under the segment's wet/.
CRAWL_BASE=https://data.commoncrawl.org/crawl-data/CC-MAIN-2024-10/segments/1707947473347.0/wet
CRAWL_FILES="
CC-MAIN-20240220211055-20240221001055-00000.warc.wet.gz $COMMONCRAWL_SHA256
CC-MAIN-20240220211055-20240221001055-00001.warc.wet.gz 5460f2da1bd3cdf04a3fe96475f44da74e5f9ae84132195952f7692280956f7b
CC-MAIN-20240220211055-20240221001055-00002.warc.wet.gz 2f603820bec8f0db4bdf0a6606beebde0e74526b4986cfab0496e31eb4b69848
CC-MAIN-20240220211055-20240221001055-00003.warc.wet.gz db690cf1a4e87060a57beda5c2173033bb3de77be7db49858d5c729e7f5aaa66
CC-MAIN-20240220211055-20240221001055-00004.warc.wet.gz 6e1a9669aa7d4052e11613ad1eab1a78f708643075c9f875e219fb75033e65e4
CC-MAIN-20240220211055-20240221001055-00005.warc.wet.gz 70b083b862b8d7b97b627d8085648eedcf027f19a6139fa771146a3a86ce8577
CC-MAIN-20240220211055-20240221001055-00006.warc.wet.gz aa51788e34d4e87f1a10d9a58169139fead75fbbb11c7f3112c036085c42a460
CC-MAIN-20240220211055-20240221001055-00007.warc.wet.gz 25209884b7b40ce462ec8afd885456ed6213164fff9bb42a70a8f62f4f064ce9
CC-MAIN-20240220211055-20240221001055-00008.warc.wet.gz 8af6243eefbda06fdfc407ba0cd640d87e6c91109cf4c897f58744ea69be0801
CC-MAIN-20240220211055-20240221001055-00009.warc.wet.gz 7b23a6d82ded237bd486b6a979ac9c3b8795b1c18eb86e17b4b8e4c0250b24a5
"

sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

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
# download, through FILTER, unless it is there and unchanged.
fetch() {
    out=$dest/$1/$2
    if [ -f "$out" ] && [ -f "$out.sha256" ] && [ "$(sha256 "$out")" = "$(cat "$out.sha256")" ]; then
        echo "fetch-data: $out is already there"
        return 0
    fi
    [ ! -f "$out" ] || echo "fetch-data: $out changed since it was written; fetching it again" >&2
    download "$3" "$4"
    mkdir -p "$dest/$1"
    # Filter to a temporary file next to the output first: in a pipeline, sh
    # would only see the last command's exit status, and a half-written file
    # would look done. In the same directory, mv is an atomic rename.
    $5 <"$tmp" >"$out.part"
    sha256 "$out.part" >"$out.sha256"
    mv "$out.part" "$out"
    echo "fetch-data: wrote $out ($(wc -c <"$out" | tr -d ' ') bytes)"
}

gutenberg() { awk '/^\*\*\* *START OF/ { on = 1; next } /^End of .*Project Gutenberg/ { on = 0 } on'; }
nasa() { gzip -dc; }
# grep would hide a failed gzip, so the archive is tested first.
snap() { gzip -t "$tmp" && gzip -dc | grep -v '^#'; }
wet() { python3 "$root/scripts/wet-to-tsv.py"; }

what=${1:-all}
case $what in
warpeace | all) fetch warpeace warpeace.txt "$WARPEACE_URL" "$WARPEACE_SHA256" gutenberg ;;
nasa | webgoogle | commoncrawl | crawl) ;;
*)
    echo "usage: $0 [warpeace | nasa | webgoogle | commoncrawl | crawl | all]" >&2
    exit 2
    ;;
esac
# War and Peace's chapters, as files (scripts/split-book.py), which renames
# the directory into place only when every chapter is written.
case $what in warpeace | all)
    if [ ! -d "$dest/warpeace-chapters" ]; then
        python3 "$root/scripts/split-book.py" "$dest/warpeace/warpeace.txt" "$dest"
    fi
    ;;
esac
case $what in nasa | all) fetch nasa access.log "$NASA_URL" "$NASA_SHA256" nasa ;; esac
case $what in webgoogle | all) fetch webgoogle links.tsv "$WEBGOOGLE_URL" "$WEBGOOGLE_SHA256" snap ;; esac
case $what in commoncrawl | all) fetch commoncrawl pages.tsv "$COMMONCRAWL_URL" "$COMMONCRAWL_SHA256" wet ;; esac
case $what in crawl)
    echo "$CRAWL_FILES" | while read -r name sum; do
        [ -z "$name" ] || fetch crawl "$name" "$CRAWL_BASE/$name" "$sum" cat
    done
    ;;
esac
