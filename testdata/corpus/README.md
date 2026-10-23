# Golden word-count corpus

Input and expected output for the local-mode golden tests (#12).

| File | Work | Author | Project Gutenberg eBook |
|---|---|---|---|
| `alice.txt` | Alice's Adventures in Wonderland | Lewis Carroll | #11 |
| `jekyll.txt` | The Strange Case of Dr. Jekyll and Mr. Hyde | Robert Louis Stevenson | #42 |
| `raven.txt` | The Raven | Edgar Allan Poe | #17192 |

The works are in the public domain in the United States. The files contain
only the text of each work: `scripts/fetch-corpus.sh` fetched them from the
GITenberg mirror and removed the Project Gutenberg header, footer and
licence. They keep their original CRLF line endings.

`expected/` is word count with R = 4, written by the independent oracle
`scripts/make-corpus-expected.py`. Words are maximal runs of bytes that
are not ASCII whitespace.
