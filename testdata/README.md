# Test data

Inputs and golden outputs shared by the Go and Python tests. A change to one
implementation that breaks a file here is a change in behavior.

| Directory | Contents | Regenerate with |
|---|---|---|
| [`corpus/`](corpus/) | Three public-domain texts and the expected word count over them with R = 4 (see its README) | `scripts/fetch-corpus.sh`, `scripts/make-corpus-expected.py` |
| `format/` | MRKV files, good and corrupt, for every codec, and `vectors.json`, which says what a correct reader returns for each | `scripts/make-format-vectors.py` |
| `hash/` | `vectors.json`: keys and the reduce task the partition hash must give each | `scripts/make-hash-vectors.py` |
| `proto/` | Messages in text form (`.txtpb`) and their exact binary encoding (`.binpb`) | `scripts/make-golden.py` |

`format/go/` holds the same records as the Go writer compresses them, which
the Python reader must read. See [the file format](../docs/file-format.md).
