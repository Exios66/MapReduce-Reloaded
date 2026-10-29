# Data

The inputs every test, benchmark and experiment runs on, and how each one
relates to the inputs of the 2004 paper. In short, the paper's §5
measurements need no new data: the benchmark generators reproduce its record
formats. The §2.3 example programs also run on real datasets, fetched rather
than committed, and give the same output in Go and Python as an independent
oracle.

## What the paper used

The paper measured two programs on its 1,800-machine cluster (§5):

- **Grep (§5.2):** 10^10 100-byte records (1 TB), searched for a rare
  three-character pattern that 92,337 records contain.
- **Sort (§5.3):** the same 10^10 100-byte records, sorted by their first 10
  bytes, in the format of the TeraSort benchmark. The backup-task (§5.4) and
  machine-failure (§5.5) runs use this sort.

§2.3 describes the example programs only by what they compute. Their inputs
were Google's production data: crawled documents, web request logs and link
graphs. These are not public, so no run here can repeat them exactly.

## How ours compare

| Paper | Here | Committed? | How it matches |
|---|---|---|---|
| §5.2 grep records | [`bench/gen-grep.py`](../bench/gen-grep.py) | generated | The same record size and match rate, so a grep's output has an exact number of lines |
| §5.3 sort records | [`bench/gen-sort.py`](../bench/gen-sort.py) | generated | gensort-style ASCII records with 10-byte keys. The keys are random bytes, not printable ones, so distsort's range partitions are even |
| §5.4, §5.5 | Experiments E2 and E3 on the sort input | generated | [Evaluation](evaluation.md) |
| §2.3 word count | [`testdata/corpus`](../testdata/corpus/), three Gutenberg texts, 337 KB | yes | A golden output from an independent oracle |
| §2.3 word count, real text at size | War and Peace, 3.2 MB | fetched | Below |
| §2.3 word count, skewed keys | [`bench/gen-zipf.py`](../bench/gen-zipf.py) | generated | Zipf frequencies with an exponent E1 varies |
| §2.3 grep, inverted index, distributed sort | [`testdata/examples`](../testdata/examples/) | yes | Golden outputs, checked in both languages |
| §2.3 URL access frequency | NASA's web log, July 1995 | fetched | Below. `bench/gen-weblog.py` makes logs of any size |
| §2.3 reverse web-link graph | Google's 2002 web graph | fetched | Below. `bench/gen-links.py` makes graphs of any size |
| §2.3 term vector per host | `testdata/examples/termvector` | yes | Small and synthetic. No real collection yet |

What sets the comparison apart is scale, not data. The paper ran 1 TB on
about 1,800 machines; here 1–10 GB runs on 4 workers sharing one VM, so
[Evaluation](evaluation.md) compares the shapes of the results, not their
numbers. More data would not change that. More machines would.

## Real datasets

`make real-data` fetches three datasets into `data/input/`
([`scripts/fetch-data.sh`](../scripts/fetch-data.sh)). Every container sees
that directory at `/data/input`, so cluster jobs can read them too. Then it
runs each one through its example program, in Go (`mr run -local`) and in
Python (`python -m mapreduce_reloaded run --local`), with M = 16 and R = 4,
and compares both with an oracle
([`scripts/check-real-data.py`](../scripts/check-real-data.py)). The oracle
is stdlib Python written from the programs' documented behavior, without
their code, as [`make-corpus-expected.py`](../scripts/make-corpus-expected.py)
is for the golden corpus. A test checks that it reproduces the committed
golden outputs of word count, urlfreq and reverselinks. Each download is
pinned by its SHA-256 sum, so a file that changes upstream fails the fetch
rather than changing the results.

| Dataset | Program | Input | Keys out | Go | Python |
|---|---|---:|---:|---|---|
| War and Peace (Project Gutenberg #2600) | `wordcount` | 3.2 MB, 563,296 words | 41,631 | same, 0.5 s | same, 2.6 s |
| NASA Kennedy Space Center web log, July 1995 | `urlfreq` | 205 MB, 1,891,714 requests | 21,668 | same, 2.7 s | same, 13.5 s |
| Google programming contest web graph, 2002 (SNAP) | `reverselinks` | 75 MB, 5,105,039 links | 714,545 | same, 5.2 s | same, 47.8 s |

"Same" means byte for byte the oracle's output. These are times on the
course VM at the commit that added the script, in local mode, in one
process. The whole run takes about 1.5 minutes and peaks at 1.1 GB of memory.

The real data tests what the synthetic inputs cannot:

- **War and Peace** is real English prose at ten times the corpus's size,
  with LF line endings (the corpus is all CRLF). It has a real vocabulary:
  22,167 of its 41,631 words occur once.
- **The NASA log** has the irregularities a generator leaves out. Eleven
  requests hold bytes outside ASCII, and one line has too few fields, which
  urlfreq skips as documented.
- **The web graph** has 714,545 distinct targets, so reverselinks' reduce
  tasks hold many short value lists, unlike `gen-links.py`'s 100,000 pages.

### Sources and terms

- **War and Peace,** translated by Louise and Aylmer Maude, via the
  [GITenberg](https://github.com/GITenberg/War-and-Peace_2600) mirror of
  [Project Gutenberg](https://www.gutenberg.org/ebooks/2600). It is in the
  public domain in the United States. As with the corpus, only the text of
  the work is kept, without Project Gutenberg's header, footer or licence.
- **NASA-HTTP,** from the
  [Internet Traffic Archive](https://ita.ee.lbl.gov/html/contrib/NASA-HTTP.html):
  "The traces may be freely redistributed." The archive asks that analyses
  stay with general traffic patterns, which a count of requests per URL does.
- **web-Google,** from the
  [Stanford Large Network Dataset Collection](https://snap.stanford.edu/data/web-Google.html).
  It states no license, so it is fetched and never committed. Cite it as:
  J. Leskovec, K. Lang, A. Dasgupta and M. Mahoney, "Community Structure in
  Large Networks: Natural Cluster Sizes and the Absence of Large
  Well-Defined Clusters", *Internet Mathematics* 6(1), 29–123, 2009.

## What is left

- **A real document collection** for the inverted index and term vectors.
  Several dozen Gutenberg books, one document per chapter, would do.
- **The golden corpus** has no non-English text, so nothing tests multibyte
  UTF-8 words against a golden output.
- **Timing curves on real text.** War and Peace is about 3 MB, so a cluster
  job over it ends in about a second, too fast for the data-rate plots of
  §5. A gigabyte-scale input built from the book's lines would give word
  count on real English the timing curves that E1 gives the Zipf inputs.
