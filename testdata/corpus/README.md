# Golden word-count corpus

Four public-domain books, and the exact output word count must write over
them. Every way of running a job, in both languages, is checked against
`expected/` byte for byte (below), so any change in how words are split,
counted, partitioned, sorted or written shows up here first.

| File | Work | Author | Project Gutenberg eBook | Bytes | Words | Distinct words |
|---|---|---|---|---:|---:|---:|
| `alice.txt` | Alice's Adventures in Wonderland | Lewis Carroll | #11 | 147,800 | 26,449 | 5,298 |
| `jekyll.txt` | The Strange Case of Dr. Jekyll and Mr. Hyde | Robert Louis Stevenson | #42 | 143,270 | 25,740 | 6,187 |
| `raven.txt` | The Raven | Edgar Allan Poe | #17192 | 46,567 | 7,183 | 2,731 |
| `verwandlung.txt` | Die Verwandlung (German) | Franz Kafka | #22367 | 124,189 | 19,286 | 5,117 |
| All four | | | | 461,826 | 78,658 | 16,851 |

The works are in the public domain in the United States. The files contain
only the text of each work: `scripts/fetch-corpus.sh` fetched them from the
GITenberg mirror and removed the Project Gutenberg header, footer and
licence. The English texts keep their original CRLF line endings.
`verwandlung.txt` was published in ISO-8859-1. The script converts it to
UTF-8 with LF line endings, so the corpus has words of multibyte
characters (1,229 of its 5,117 distinct words) and both kinds of line
ending.

## `expected/`

Word count with R = 4, written by `scripts/make-corpus-expected.py`, an
oracle independent of both implementations: standard library only, with
the partition hash written out from [the spec](../../docs/file-format.md).
Words are maximal runs of bytes that are not ASCII whitespace, so
case and punctuation count: `the`, `The` and `THE` are three words, and
`said` and `said,` are two.

- `part-r-0000N` holds the words whose FNV-1a 64 hash, mod 4, is N, one
  `word<TAB>count` line each, in byte order.
- The four parts hold almost the same number of distinct words, but not of
  occurrences: the most frequent words decide that. `the` (3,444), `of`,
  `to` and `a` all hash to part 0.

| Part | Distinct words | Occurrences | Bytes |
|---|---:|---:|---:|
| `part-r-00000` | 4,198 | 26,911 | 43,666 |
| `part-r-00001` | 4,242 | 17,467 | 44,191 |
| `part-r-00002` | 4,206 | 18,953 | 44,088 |
| `part-r-00003` | 4,205 | 15,327 | 44,053 |
| All four | 16,851 | 78,658 | 175,998 |

`python3 scripts/make-corpus-expected.py` rewrites `expected/` and prints
the totals; on an unchanged corpus it changes nothing.
`python/tests/test_corpus_readme.py` checks every number on this page
against the files.

## What is checked against it

| Where it runs | Test |
|---|---|
| Local mode, Go, for several values of M, and with input and output in S3 | `go/internal/local/local_test.go`, `s3_test.go` |
| Local mode, Python | `python/tests/test_local.py` |
| The `mr` command, and the job file `examples/wordcount.go` interpreted | `go/cmd/mr/main_test.go`, `go/internal/jobfile/jobfile_test.go` |
| A coordinator and workers in one process | `go/internal/worker/cluster_test.go` |
| Coordinator and workers as processes, one of them killed | `go/internal/e2e/process_test.go` |
| The Python coordinator with Go workers | `python/tests/test_coordinator_e2e.py` (`make py-e2e`) |
| The Compose cluster | `scripts/e2e.sh` (`make e2e`), tested by `python/tests/test_e2e.py` |
| The demo | [`docs/demo.md`](../../docs/demo.md) and section 13 of [`examples/demo.ipynb`](../../examples/demo.ipynb) |
