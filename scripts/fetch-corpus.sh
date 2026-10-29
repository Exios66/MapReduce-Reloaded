#!/bin/sh
# Fetches the golden word-count corpus into testdata/corpus/.
#
# Public-domain texts from Project Gutenberg, via the GITenberg mirror. Only
# the text between the "*** START OF" line and the "End of ... Project
# Gutenberg" line is kept, so no Project Gutenberg licence or trademark text
# is included. Line endings are kept as published (CRLF), except in
# verwandlung.txt: it is published in ISO-8859-1, and is converted to UTF-8
# with LF line endings, so the corpus has multibyte words and both kinds of
# line ending.
set -eu

out=$(dirname "$0")/../testdata/corpus
base=https://raw.githubusercontent.com/GITenberg
tmp=$(mktemp)
trap 'rm -f "$tmp" "$tmp.conv"' EXIT

fetch() { # name repo/file [latin1-lf]
    # Download first: in a pipeline, sh would only see awk's exit status.
    curl -fsS -o "$tmp" "$base/$2"
    if [ "${3:-}" = latin1-lf ]; then
        iconv -f ISO-8859-1 -t UTF-8 "$tmp" >"$tmp.conv"
        tr -d '\r' <"$tmp.conv" >"$tmp"
    fi
    awk '/^\*\*\* *START OF/ { on = 1; next } /^End of .*Project Gutenberg/ { on = 0 } on' \
        "$tmp" >"$out/$1"
}

fetch alice.txt Alice-s-Adventures-in-Wonderland_11/master/11.txt
fetch jekyll.txt The-Strange-Case-Of-Dr.-Jekyll-And-Mr.-Hyde_42/master/42.txt
fetch raven.txt The-Raven_17192/master/17192.txt
fetch verwandlung.txt Die-Verwandlung_22367/master/22367-8.txt latin1-lf
