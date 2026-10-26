# Scripts

| Script | What it does |
|---|---|
| [`ci-report.sh`](ci-report.sh) | Runs `make check` on a clean clone of `HEAD` and posts a `local-ci/make-check` commit status to GitHub (needs `GITHUB_TOKEN`). `make ci-report`; see ADR 0014. |
| [`gen-proto.sh`](gen-proto.sh) | Generates the Go and Python code from [`proto/`](../proto/), with the `protoc` bundled in `grpcio-tools`. Run it with `make proto`. |
| [`e2e.sh`](e2e.sh) | The end-to-end test on the running Compose cluster: word count over the corpus must equal the golden output, no container may be OOM-killed, and `/metrics` must show the job done. `make e2e`, or `make e2e-s3` for the S3 store. |
| [`vm-setup.sh`](vm-setup.sh) | Checks (`make vm-check`) and with `--install` prepares a Linux machine to build and run the cluster. |
| [`fetch-corpus.sh`](fetch-corpus.sh) | Fetches the public-domain texts of the golden corpus into `testdata/corpus/`. |
| [`make-corpus-expected.py`](make-corpus-expected.py) | Regenerates the corpus's expected word count with an independent oracle. |
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
