# MRKV File Format, version 1

MRKV ("MapReduce key/value") is the on-disk format for intermediate data (map
output, sorted runs) and for key/value job output. Go and Python must read and
write it byte-for-byte identically. Golden test files are in
[`testdata/format/`](../testdata/format/). The design rationale is in
[ADR 0004](design/0004-intermediate-file-format.md).

Paper reference: §3.1 (map output is written to local disk, partitioned into
R regions) and §4.4 (input and output types).

## Conventions

- All fixed-width integers are **unsigned, little-endian**: `u8`, `u32`,
  `u64`.
- `uvarint` is the unsigned LEB128 varint used by protobuf and Go's
  `encoding/binary`:
  - 7 bits per byte, least-significant group first;
  - the high bit means "more bytes follow";
  - at most 10 bytes.
- `crc32c` is CRC-32 with the **Castagnoli** polynomial (reflected
  `0x82F63B78`), initial value `0xFFFFFFFF` and final XOR `0xFFFFFFFF`.
  - Check value: `crc32c("123456789") = 0xE3069283`.
  - This is Go's `crc32.MakeTable(crc32.Castagnoli)`. It is **not** the
    zlib/IEEE CRC-32.

## Layout

```
file   := header block* footer
header := magic version codec reserved
block  := stored_len record_count checksum payload
footer := end_marker total_records block_count footer_checksum
```

### Header (8 bytes)

| Offset | Size | Field | Value |
|---|---|---|---|
| 0 | 4 | `magic` | ASCII `MRKV` (`4D 52 4B 56`) |
| 4 | 1 | `version` | `0x01` |
| 5 | 1 | `codec` | Compression of block payloads; see [Codecs](#codecs) |
| 6 | 2 | `reserved` | `00 00`. Readers reject other values. |

### Block

| Size | Field | Meaning |
|---|---|---|
| 4 | `stored_len` (`u32`) | Bytes of `payload` as stored. Always ≥ 1, because 0 marks the footer. |
| 4 | `record_count` (`u32`) | Records in this block. Always ≥ 1. |
| 4 | `checksum` (`u32`) | `crc32c(payload)`, computed over the **stored** (possibly compressed) bytes |
| `stored_len` | `payload` | The records, compressed with the header's codec |

Once decompressed, the payload is `record_count` records back to back:

```
record := uvarint(len(key)) key uvarint(len(value)) value
```

Rules:
- Keys and values are arbitrary bytes. Either may be empty.
- **A record never spans two blocks.**
- Writers append each record to the current block, then close the block once
  its decompressed payload is at least 64 KiB (65,536 bytes). If a record
  would take the payload past 64 MiB, they close the current block before
  appending it. The Go and Python writers follow this rule exactly, so they
  write identical bytes; readers must not rely on it.
- A block's payload is at most **64 MiB** (`stored_len` ≤ 67,108,864).
  Readers reject a larger block as `bad_block` without allocating it, so a
  corrupt length field cannot exhaust memory. Writers refuse a record that
  would not fit.
- Readers must not assume any particular block size.
- The decompressed payload must contain exactly `record_count` records, with
  no leftover bytes.

### Footer (24 bytes)

| Size | Field | Meaning |
|---|---|---|
| 4 | `end_marker` (`u32`) | `0`. Tells the reader that the footer starts here, not another block. |
| 8 | `total_records` (`u64`) | Sum of `record_count` over all blocks |
| 8 | `block_count` (`u64`) | Number of blocks |
| 4 | `footer_checksum` (`u32`) | `crc32c` of the 16 bytes `total_records ‖ block_count` |

No bytes may follow the footer.

## Codecs

| ID | Codec | Status |
|---|---|---|
| 0 | none | Version 1 |
| 1 | zstd (one frame per block) | Reserved, issue #42 |
| 2 | snappy (block format) | Reserved, issue #42 |

Readers reject codec IDs they don't support.

## Ordering

Intermediate files (map output and sorted runs) store records in ascending
key order. Keys are compared as **raw bytes**, lexicographically
(`memcmp`/`bytes.Compare`): a shorter key that is a prefix of a longer one
sorts first. Records with equal keys keep the order in which they were
written. Job output files have no ordering requirement from the format,
although reduce output is sorted in practice (§4.2).

## Partitioning

A map task writes each record to one of R partitions, one per reduce task.
The default partition of a key is (§3.1, §4.1):

```
partition(key, R) = fnv1a64(key) mod R        R ≥ 1
```

`fnv1a64` is the 64-bit FNV-1a hash over the key's **raw bytes**. All
arithmetic is on unsigned 64-bit integers and wraps around (mod 2^64):

```
h = 0xcbf29ce484222325            # offset basis
for each byte b of key:
    h = h XOR b
    h = h * 0x00000100000001b3    # FNV prime, wrapping
return h
```

- The result of `mod R` is in `0..R-1`. R = 0 is an error.
- Keys are never decoded or normalised before hashing. A UTF-8 string and the
  same text in another encoding are different keys.
- Reference values: `fnv1a64("") = cbf29ce484222325`,
  `fnv1a64("a") = af63dc4c8601ec8c`, `fnv1a64("foobar") = 85944171f73967e8`.
  More vectors are in `testdata/hash/vectors.json`.

Why a fixed, specified hash: every map task, in either language, must send a
given key to the same reducer, or that reducer would see only part of the key's
values. The languages' built-in hashes cannot do this. Go's `hash/maphash`
and Python's `hash()` are randomly seeded per process, on purpose, to resist
hash-flooding attacks. FNV-1a is short enough to implement identically
everywhere and fast for short keys. It is not cryptographic, and does not
need to be: it only spreads keys across partitions.

A job can replace this function with a custom partitioner (§4.1, paper
feature F14).

## Errors a reader must detect

| Error kind | Condition |
|---|---|
| `bad_magic` | The first 4 bytes are not `MRKV` |
| `unsupported_version` | `version` is not 1 |
| `unsupported_codec` | `codec` is unknown or not supported |
| `bad_header` | `reserved` is not zero |
| `checksum_mismatch` | A block's or the footer's CRC does not match |
| `bad_block` | Records don't exactly fill the payload, or `record_count` is 0 |
| `truncated` | The file ends before a complete footer |
| `count_mismatch` | The footer's totals differ from the blocks actually read |
| `trailing_data` | Bytes follow the footer |

Checks run in file order, so the first problem found is the one reported.
Within a block, the checksum is verified before the records are decoded.

A reader may return a block's records only after its checksum has been
verified. A streaming reader can therefore pass along records from good
blocks before it discovers a later corrupt block. Callers that need
all-or-nothing behaviour must wait for the footer before trusting the file.

## Worked example

One record, key `"hello"`, value `"1"`, no compression:

```
4D 52 4B 56 01 00 00 00     header: "MRKV", version 1, codec 0, reserved
08 00 00 00                 block: stored_len = 8
01 00 00 00                        record_count = 1
59 25 69 78                        crc32c(payload) = 0x78692559
05 68 65 6C 6C 6F 01 31            payload: len 5, "hello", len 1, "1"
00 00 00 00                 footer: end marker
01 00 00 00 00 00 00 00             total_records = 1
01 00 00 00 00 00 00 00             block_count = 1
33 EA 40 F9                         crc32c(previous 16 bytes) = 0xF940EA33
```

The exact bytes are in `testdata/format/one_record.mrkv`.
