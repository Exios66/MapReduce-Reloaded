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
| §2.3 inverted index, real text | War and Peace's 365 chapters as files | fetched | Below. One book cut at its own headings, not independent documents |
| §2.3 term vector per host | `testdata/examples/termvector` | yes | Small and synthetic |
| §2.3 term vector per host, real pages | A Common Crawl WET file, February 2024 | fetched | Below: crawled pages with their real URLs and hosts |

What sets the comparison apart is scale, not data. The paper ran 1 TB on
about 1,800 machines; here 1–10 GB runs on 4 workers sharing one VM, so
[Evaluation](evaluation.md) compares the shapes of the results, not their
numbers. More data would not change that. More machines would.

## Real datasets

`make real-data` fetches four datasets into `data/input/`
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
| War and Peace's chapters, one file each | `invertedindex` | 365 files, 3.2 MB | 41,599 | same, 1.1 s | same, 5.5 s |
| Common Crawl CC-MAIN-2024-10, one WET file | `termvector` | 286 MB, 34,250 pages | 30,991 hosts | same, 32.6 s | same, 110.9 s |

"Same" means byte for byte the oracle's output. These are times on the
course VM at the commit that added the script, in local mode, in one
process. The first three take about 1.5 minutes and peak at 1.1 GB of memory, and the last two about 2.7 minutes and 1.8 GB.

The real data tests what the synthetic inputs cannot:

- **War and Peace** is real English prose at ten times the corpus's size,
  with LF line endings (the corpus is all CRLF). It has a real vocabulary:
  22,167 of its 41,631 words occur once.
- **The NASA log** has the irregularities a generator leaves out. Eleven
  requests hold bytes outside ASCII, and one line has too few fields, which
  urlfreq skips as documented.
- **The web graph** has 714,545 distinct targets, so reverselinks' reduce
  tasks hold many short value lists, unlike `gen-links.py`'s 100,000 pages.
- **The chapters** give the inverted index 365 input files, each a map
  task's file name, where the golden test has two. They are one book's
  chapters, so the index says which chapters a word occurs in. They are not
  independent documents.
- **The Common Crawl pages** are the input §2.3 describes for term vectors:
  crawled pages, in many languages, grouped by their real hosts (30,991 of
  them). [`scripts/wet-to-tsv.py`](../scripts/wet-to-tsv.py) makes each
  page a line, its URL, a tab and its text.

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
- **Common Crawl,** one WET file of the
  [CC-MAIN-2024-10](https://data.commoncrawl.org/crawl-data/CC-MAIN-2024-10/wet.paths.gz)
  crawl, used under Common Crawl's
  [terms of use](https://commoncrawl.org/terms-of-use), which grant a
  limited, non-transferable license. The pages belong to their sites and
  are the unfiltered web, so they are fetched, never committed, and only
  aggregate results, such as the counts above, are recorded.

## What is left

- **Timing curves on real text.** War and Peace is about 3 MB, so a cluster
  job over it ends in about a second, too fast for the data-rate plots of
  §5. Branch `book-scale` adds `bench/gen-book.py`, which builds a
  gigabyte-scale input from the book's lines, and experiment E6.
- **The golden corpus** has no non-English text. Branch `corpus-utf8` adds
  a German text in UTF-8.
