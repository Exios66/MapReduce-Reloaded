# Explainer — MRKV Reader and Writer (#8)

Spec: [file-format.md](../file-format.md). Format background:
[04-file-format.md](04-file-format.md).

## What it is

The code that turns `(key, value)` records into an MRKV file and back, in
both languages. Every later piece (map output, sort, shuffle, reduce input)
reads and writes data through it.

| | Go (`go/internal/formats/kv`) | Python (`mapreduce_reloaded.formats.kv`) |
|---|---|---|
| Write | `w := kv.NewWriter(f)`, `w.Write(k, v)`, `w.Close()` | `with kv.Writer(f) as w: w.write(k, v)` |
| Read | `for r.Next() { r.Key(), r.Value() }`, then `r.Err()` | `for k, v in kv.read(f):` |
| Errors | `errors.Is(err, kv.ErrChecksumMismatch)` | `FormatError` with `.kind == "checksum_mismatch"` |

## Design choices

- **One block in memory at a time.** The writer buffers about 64 KB, then
  writes a block; the reader loads one block, checks its checksum, then
  hands out its records. Memory use stays flat however big the file is.
- **Checksum before use.** No record is returned from a block until that
  block's CRC-32C matches.
- **A 64 MiB block limit**, added to the spec. Without it, a corrupted
  length field could make a reader try to allocate up to 4 GB.
- **Go reader is scanner-style** (like `bufio.Scanner`): `Key()` and
  `Value()` point into the reader's buffer, so reading allocates nothing
  per record. Callers copy what they keep.
- **Sticky errors in Go:** after a failure, every later call returns the
  same error, so a caller can check once at the end.
- **Python's writer skips the footer if an exception escapes the `with`
  block.** A half-written file then reads as `truncated` instead of looking
  complete.

## How we know it's right

- **Golden files:** both languages read all 9 files in `testdata/format/`
  and get the expected records or error, and both writers reproduce the 4
  valid files **byte for byte**. Since both match the same bytes, a file
  written by Go is readable by Python and vice versa.
- **Hand-made bad files:** 16 malformed inputs (bad version, zero record
  count, leftover bytes, oversized block, short footer, ...) cover every
  error kind the golden files don't, identically in both languages.
- **Random testing:**
  - Go fuzzers check round trips and that arbitrary input never crashes
    the reader.
  - Python (`hypothesis`) checks that the writer's output equals the
    reference encoder's, and that a file with one corrupted byte gets the
    same result as from the reference decoder.
- **Proof the tests can fail:** disabling the checksum check, the
  leftover-bytes check, the footer count check or the trailing-data check
  fails both suites. Go coverage is 97%.

## Likely review questions

- **Why not `iter.Seq2` in Go?** It would hide the fact that keys and
  values are reused buffers, or force a copy per record.
- **Why is Python's CRC-32C hand-written?** The standard library only has
  plain CRC-32. It is slow in pure Python, which is acceptable for the
  reference implementation.
- **Why does the reference decoder still exist?** It is the independent
  answer key: the production code is written separately and checked
  against it, so one misreading of the spec can't hide in both.
