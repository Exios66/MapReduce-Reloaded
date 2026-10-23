# Explainer — The MRKV File Format (#4)

Full spec: [file-format.md](../file-format.md). Rationale:
[ADR 0004](../design/0004-intermediate-file-format.md).

## What it is for

When a map task finishes, it saves its output as files on disk. Reducers
later download those files and read them. Go and Python must both read and
write these files the same way, and a damaged file must be **caught, not
silently used**. MRKV ("MapReduce key/value") is that file format. If #3 was
the format of the *messages*, this is the format of the *saved data*.

## The shape of a file

```
[header 8 bytes] [block] [block] ... [block] [footer 24 bytes]
```

- **Header:** the letters `MRKV`, a version number and a compression flag.
  A reader can tell straight away that this is one of our files.
- **Blocks:** groups of records, about 64 KB each. A record is a key and a
  value, each prefixed with its length:

  ```
  [5]hello[1]1   →   key "hello", value "1"
  ```

  Each block starts with its size, how many records it holds, and a
  **checksum**.
- **Footer:** "this file has N records in B blocks", with its own checksum.

## Why blocks and checksums?

- **Checksum (CRC-32C):** a 4-byte fingerprint of the block's bytes. If
  even one bit flips because of a bad disk or a network glitch, the
  fingerprint no longer matches and the reader stops with
  `checksum_mismatch` instead of passing on wrong data.
- **One checksum per block** means a reader can check and use a block
  without holding the whole file in memory.
- **Blocks are also where compression will go** (#42), and a natural place
  to resume an interrupted download.
- **The footer** catches the two failures checksums can't:
  - a file that was cut short (no footer);
  - a block that went missing (the counts don't add up).

## How we know the spec is right

- `testdata/format/` holds 9 small **golden files**:
  - 4 valid: empty, one record, many records, binary keys;
  - 5 deliberately broken: a corrupted byte, a cut-off file, a missing
    block, a wrong magic word, extra bytes at the end.

  `vectors.json` says what a correct reader must return for each: the
  records, or the exact error.
- `scripts/mrkv_reference.py` is a short, line-by-line translation of the
  spec, used to generate and check those files.
- **Python** decodes all 9 and gets the expected records or errors.
- **Go** checks that its own checksum function gives exactly the values
  stored in the files, so files written by Python are readable from Go.
- **Proof the tests can fail:** flipping one bit in `one_record.mrkv` fails
  both languages' tests, and swapping in the wrong checksum algorithm fails
  the Go test.

The real readers and writers (#8) are written separately and must pass the
same 9 files. That's the point: two independent implementations agreeing
with one fixed answer key.

## Likely review questions

- **Why not an existing format?** See the ADR's comparison table. In short,
  SequenceFile is tied to Java, Avro needs a schema and a heavy library,
  Parquet is built for analytics rather than streaming, and plain
  length-delimited protobuf has no checksums or footer.
- **Why CRC-32C rather than plain CRC-32?** Modern CPUs compute it in
  hardware, and it is the standard choice in storage systems.
- **What does "keys sort as raw bytes" mean?** Keys are compared byte by
  byte, like a dictionary that doesn't care about language rules. Go and
  Python then agree on the order without any extra rules.
