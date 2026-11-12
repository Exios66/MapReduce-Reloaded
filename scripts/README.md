# Scripts

| Script | What it does |
|---|---|
| [`ci-install-go.sh`](ci-install-go.sh) | Installs the Go release named in `go/go.mod` for CircleCI, checking the archive's SHA-256 against go.dev's release list. See `docs/ci.md`. |
| [`ci-report.sh`](ci-report.sh) | Runs `make check` on a clean clone of `HEAD` and posts a `local-ci/make-check` commit status to GitHub (needs `GITHUB_TOKEN`). `make ci-report`; see ADR 0014. |
| [`gen-proto.sh`](gen-proto.sh) | Generates the Go and Python code from [`proto/`](../proto/), with the `protoc` bundled in `grpcio-tools`. Run it with `make proto`. |
| [`chaos.sh`](chaos.sh) | Seeded chaos on the running Compose cluster (#29): during word count jobs it kills workers, pauses them past and short of the failure timeout, and crashes the coordinator, and every job must give the undisturbed run's output. `make chaos RUNS=5 SEED=1`; a failing run prints its seed. |
| [`e2e.sh`](e2e.sh) | The end-to-end test on the running Compose cluster: word count over the corpus must equal the golden output, no container may be OOM-killed, and `/metrics` must show the job done. `make e2e`, or `make e2e-s3` for the S3 store. |
| [`cover.py`](cover.py) | Checks each Go and Python package's statement coverage against its floor in `go/cover-floors.txt` or `python/cover-floors.txt`; `make check` runs it after the tests, and `make cover` prints the table (#161, [testing](../docs/testing.md)). |
| [`fuzz.sh`](fuzz.sh) | Runs every Go fuzz target for `FUZZTIME` (default 20s) each; `make fuzz` (#152). |
| [`soak.sh`](soak.sh) | The soak run: 1 GiB with the combiner off on two workers limited to 256 MB, which must finish with no container killed for memory (#160, #47). `make soak`; `SIZE` and `WORKERS` override the defaults. |
| [`perf.sh`](perf.sh), [`perf-compare.py`](perf-compare.py) | Run the Go benchmarks and compare their median time per operation with `bench/baseline.txt`, failing on a slowdown over `THRESHOLD` percent (default 20) when the CPU lines match; on a different CPU the comparison is printed but cannot fail. `make perf`; `PERF_UPDATE=1` rewrites the baseline. |
| [`check-links.py`](check-links.py) | Checks that every relative link and anchor in the tracked Markdown files resolves. Run by `make md-lint` (#159). |
| [`check-dark-charts.py`](check-dark-charts.py) | Checks that every bench chart a Markdown page shows has a dark variant that exists. Run by `make md-lint` (#349). |
| [`check-figures.py`](check-figures.py) | Checks the evaluation archives against their `SHA256SUMS`, redraws every figure in `bench/evaluation/` from them, and compares each with the committed PNG pixel by pixel. Needs matplotlib, so `make check` does not run it: `make figures-check PLOT_PYTHON=...` on the experiment VM. |
| [`ci-go-job.sh`](ci-go-job.sh) | Runs one CircleCI node's share of the `go` job, which runs on three nodes (ADR 0024), in full or in the narrow mode's parts (ADR 0025). |
| [`golangci-lint.sh`](golangci-lint.sh) | Runs golangci-lint at the version pinned in `vm-setup.sh`, built by a Go release no older than the module's, so `make go-lint` and the pre-push hook give the same result on every machine; builds it into `.tools/bin` when the one on `PATH` does not qualify. |
| [`install-tool.sh`](install-tool.sh) | Installs a pinned Go tool for `make tools`, keeping a cached binary that `go version -m` shows was built from the pin (ADR 0024). |
| [`ci-params.py`](ci-params.py) | Selects the CI jobs a branch's changed files implicate, from `.circleci/mapping.txt`, and checks that selection. See [CI](../docs/ci.md) and ADR 0020. |
| [`vm-setup.sh`](vm-setup.sh) | Checks (`make vm-check`) and with `--install` prepares a Linux machine to build and run the cluster. |
| [`demo-check.py`](demo-check.py) | Checks the running cluster against what the demo needs: four running one-CPU, 768 MB workers, a 256 MB coordinator with its write-ahead log, live heartbeats on the status page, disk, `bin/mr` and the inputs. `make demo-check`; see [docs/demo.md](../docs/demo.md). |
| [`fetch-corpus.sh`](fetch-corpus.sh) | Fetches the public-domain texts of the golden corpus into `testdata/corpus/`. |
| [`capture-status-gifs.py`](capture-status-gifs.py) | Captures the status page while one whole real job runs, into dark and light `docs/assets/status-*.gif` with a provenance sidecar per run (commit, machine, job, input, timings). Needs a Chrome-family browser and ffmpeg/ffprobe on `PATH` and a running cluster; see [docs/demo.md](../docs/demo.md). |
| [`fetch-data.sh`](fetch-data.sh) | Fetches real datasets for the example programs (War and Peace and its chapters, NASA's 1995 web log, Google's 2002 web graph, a Common Crawl WET file) into `data/input/`, each pinned by SHA-256. `make real-data`; see [Data](../docs/data.md). |
| [`check-real-data.py`](check-real-data.py) | Runs word count, urlfreq, reverselinks, invertedindex and termvector over those datasets in Go and Python and compares both with an independent oracle. |
| [`make-corpus-expected.py`](make-corpus-expected.py) | Regenerates the corpus's expected word count with an independent oracle. |
| [`make-examples-expected.py`](make-examples-expected.py) | Regenerates `testdata/examples/`, the inputs and expected outputs of the example programs other than word count, with an independent oracle. |
| [`split-book.py`](split-book.py), [`wet-to-tsv.py`](wet-to-tsv.py) | Prepare real data for `make real-data`: War and Peace split into chapter files for the inverted index, and a Common Crawl WET file converted to `termvector` input ([Data](../docs/data.md)). |
| [`make-format-vectors.py`](make-format-vectors.py), [`mrkv_reference.py`](mrkv_reference.py) | Regenerate the MRKV golden vectors, with a small reference encoder written straight from the spec. |
| [`make-hash-vectors.py`](make-hash-vectors.py) | Regenerates the partition-hash vectors. |
| [`make-golden.py`](make-golden.py) | Regenerates the binary protobuf goldens from their text form. |

The `make-*` scripts write the files under [`testdata/`](../testdata/), and are
needed only after changing the format, the hash, a message or the corpus. The
corpus, format and hash scripts are written straight from the spec, without the
Go or Python code they check, so a misreading of the spec cannot hide in both
implementations. `make-golden.py` is the exception: it serializes with the
generated Python bindings, and both test suites check that their own
serialization matches its output byte for byte.
