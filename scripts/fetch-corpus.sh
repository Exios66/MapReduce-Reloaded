#!/bin/sh
# Fetches the golden word-count corpus into testdata/corpus/.
#
# Public-domain texts from Project Gutenberg, via the GITenberg mirror. Only
# the text between the "*** START OF" line and the "End of ... Project
# Gutenberg" line is kept, so no Project Gutenberg licence or trademark text
# is included. Line endings are kept as published (CRLF).
set -eu

out=$(dirname "$0")/../testdata/corpus
base=https://raw.githubusercontent.com/GITenberg

fetch() { # name repo/file
    curl -fsS "$base/$2" |
        awk '/^\*\*\* *START OF/ { on = 1; next } /^End of .*Project Gutenberg/ { on = 0 } on' \
            >"$out/$1"
}

fetch alice.txt Alice-s-Adventures-in-Wonderland_11/master/11.txt
fetch jekyll.txt The-Strange-Case-Of-Dr.-Jekyll-And-Mr.-Hyde_42/master/42.txt
fetch raven.txt The-Raven_17192/master/17192.txt
