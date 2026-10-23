# ADR 0004 — A custom block format for intermediate data (MRKV)

**Status:** accepted · **Phase:** 0 · **Issue:** #4

## Context

Map tasks write R partitioned, sorted files to local disk. Reducers stream
those files over the network, merge them, and write output (§3.1). The format
must:

- be byte-identical between Go and Python;
- detect corruption, since the paper notes that bad disks and memory were a
  real cause of failures (§3.3, §7);
- stream with bounded memory, and allow compression later (#42);
- support a resumed fetch from a known position (`FetchPartition.offset`,
  ADR 0003);
- be simple enough to specify completely on one page.

## Decision

Use a small custom format, specified in [file-format.md](../file-format.md):

- an 8-byte header with a version and codec byte;
- a sequence of **blocks**, each holding varint-length-prefixed key/value
  records and protected by a CRC-32C;
- a footer with total record and block counts, protected by its own CRC.

The parts work together:

- **Blocks** are the unit of checksumming, of compression, and of resuming a
  fetch.
- **The footer** turns two silent failures into loud ones: a file cut short
  has no footer, and a lost block makes the counts disagree.
- **CRC-32C** was chosen over CRC-32/IEEE because it is hardware-accelerated
  on modern CPUs, which is why Go's `hash/crc32` and most storage systems use
  it.
- **Keys sort as raw bytes.** No collation or type rules, so both languages
  agree trivially.
- **Golden vectors and a reference codec** (`scripts/mrkv_reference.py`)
  pin the spec down. The production code (#8) is written separately and must
  match them.

## Alternatives considered

| Option | Why not |
|---|---|
| Hadoop SequenceFile | Tied to Java types. Its sync-marker scheme is more complex than we need. There is no maintained Go/Python library that writes it byte-exactly. |
| Avro container files | Needs a schema for opaque byte pairs. The library dependency is heavier, and byte-exact output across languages is not guaranteed. |
| Parquet | Columnar and built for analytical scans, not append-only streaming of sorted pairs. Writing small runs costs too much. It will be supported as a user *input/output* format in Phase 6 (#51, stretch). |
| Length-delimited protobuf messages | Simple, but has no block checksums, no compression unit, and no footer to detect truncation. |
| Plain text (tab-separated) | Cannot hold arbitrary binary keys, and has no corruption detection. |

## Consequences

- Readers and writers in both languages are small (#8), but they must pass
  every vector in `testdata/format/`.
- Compression (#42) only fills in codec IDs 1 and 2. The format version
  stays 1.
- A format change needs a new `version` value and new vectors. Readers
  reject versions they don't know rather than guessing.
